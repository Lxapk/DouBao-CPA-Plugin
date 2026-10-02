package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Account management.
//
// The credentials live in CPA's auth store, not in the plugin, so everything here
// goes through the host: host.auth.list to enumerate, host.auth.get to resolve a file
// path, and host.auth.save to write one back.
//
// Enable/disable is the one operation the plugin performs on a credential. It
// matters for this plugin more than for most: the two upstreams hold separate
// accounts and reject each other's sessions with a country error, so a disabled
// account is not just deprioritised — it is the mechanism that keeps the wrong
// region out of the candidate pool.

// hostAuthEntry is one credential as the panel needs it.
type hostAuthEntry struct {
	AuthIndex     string
	ID            string
	Name          string
	Path          string
	Label         string
	Realm         realm
	Disabled      bool
	Status        string
	StatusMessage string
	SecUserID     string
	ExpiresAt     time.Time
}

// accountSnapshot caches the host listing briefly.
//
// The panel polls, and every entry needs a follow-up call to learn its file path and
// realm. A short cache keeps a page refresh from fanning out into a burst of host
// RPCs, while a write invalidates it so a toggle is visible immediately.
type accountCache struct {
	mu      sync.Mutex
	entries []hostAuthEntry
	at      time.Time
}

var accounts = &accountCache{}

const accountCacheTTL = 5 * time.Second

func (c *accountCache) invalidate() {
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
}

func (c *accountCache) get() []hostAuthEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) < accountCacheTTL && c.entries != nil {
		return c.entries
	}
	entries := loadAccounts()
	c.entries = entries
	c.at = time.Now()
	return entries
}

// loadAccounts enumerates this plugin's credentials.
//
// The host answers with {"files":[...]} — not "auths", which is the name the
// management API uses. Decoding into the wrong key yields an empty list rather than an
// error, so a mismatch here looks exactly like "no accounts exist".
func loadAccounts() []hostAuthEntry {
	var resp struct {
		Files []pluginapi.HostAuthFileEntry `json:"files"`
	}
	if errList := callHostInto("host.auth.list", map[string]any{}, &resp); errList != nil {
		logError("doubao: 读取账号列表失败：" + errList.Error())
		return nil
	}

	var out []hostAuthEntry
	for _, e := range resp.Files {
		// Only this plugin's credentials. Another provider's entry has its own
		// shape and would be misreported here.
		if !isOurAuthEntry(e) {
			continue
		}
		entry := hostAuthEntry{
			AuthIndex:     e.AuthIndex,
			ID:            e.ID,
			Name:          e.Name,
			Label:         e.Label,
			Disabled:      e.Disabled,
			Status:        e.Status,
			StatusMessage: e.StatusMessage,
		}
		// The listing carries the index but not the file path or the realm, so
		// each entry is resolved once. Doing it here rather than per request keeps
		// the cost to one extra call per credential per refresh.
		enrichAuthEntry(&entry)
		out = append(out, entry)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Realm != out[j].Realm {
			return out[i].Realm < out[j].Realm
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// isOurAuthEntry reports whether a host entry belongs to this plugin.
func isOurAuthEntry(e pluginapi.HostAuthFileEntry) bool {
	// The provider key CPA derives from the auth file's "type" field is the plugin
	// name, which is also the doubao realm id; a dola credential may carry either.
	for _, candidate := range []string{e.Provider, e.Type} {
		switch strings.ToLower(strings.TrimSpace(candidate)) {
		case pluginName, string(realmDola):
			return true
		}
	}
	// A credential written by this plugin always names its realm in the label.
	if strings.Contains(e.Label, "豆包") || strings.Contains(e.Label, "Dola") {
		return true
	}
	// Fall back to the file name prefix, which the auth provider sets.
	n := strings.ToLower(e.Name)
	return strings.HasPrefix(n, pluginName+"-") || strings.HasPrefix(n, string(realmDola)+"-")
}

// enrichAuthEntry fills in the path, realm and account from the credential file.
func enrichAuthEntry(entry *hostAuthEntry) {
	if entry.AuthIndex == "" {
		return
	}
	var got pluginapi.HostAuthGetResponse
	if errGet := callHostInto("host.auth.get",
		map[string]any{"auth_index": entry.AuthIndex}, &got); errGet != nil {
		return
	}
	entry.Path = got.Path
	if entry.Name == "" {
		entry.Name = got.Name
	}

	// The stored document is flat: the same object the auth provider produced.
	var stored struct {
		Realm     string `json:"realm"`
		Type      string `json:"type"`
		SecUserID string `json:"sec_user_id"`
		Label     string `json:"label"`
		Nickname  string `json:"nickname"`
		Disabled  *bool  `json:"disabled"`
	}
	if len(got.JSON) > 0 && json.Unmarshal(got.JSON, &stored) == nil {
		if r := normalizeRealm(stored.Realm); r != "" {
			entry.Realm = r
		} else if r := normalizeRealm(stored.Type); r != "" {
			entry.Realm = r
		}
		if entry.SecUserID == "" {
			entry.SecUserID = stored.SecUserID
		}
		if entry.Label == "" {
			entry.Label = firstNonEmpty(stored.Label, stored.Nickname, entry.Name)
		}
		// The file's own flag is authoritative: CPA's listing reflects it, but a
		// just-written value may not have propagated to the cached listing yet.
		if stored.Disabled != nil {
			entry.Disabled = *stored.Disabled
		}
	}
	if entry.Realm == "" {
		entry.Realm = inferRealmFromName(entry.Name)
	}
}

// inferRealmFromName reads the realm out of an auth file name.
func inferRealmFromName(name string) realm {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, string(realmDola)):
		return realmDola
	case strings.Contains(n, string(realmDoubao)):
		return realmDoubao
	}
	return ""
}

// setAuthDisabled enables or disables one credential.
//
// The write goes to the credential file, which is what CPA reads when it builds its
// candidate list — a flag held only in the plugin would not stop CPA from routing to
// the account.
//
// The file is located by *name*, not by auth index: CPA groups a provider's
// credentials under one index, so several accounts share it. Resolving by index
// therefore returns the first file of the group every time, and disabling N accounts
// would rewrite one file N times while the rest stayed enabled.
func setAuthDisabled(entry hostAuthEntry, disabled bool) error {
	path := strings.TrimSpace(entry.Path)
	if path == "" {
		path = resolveAuthPath(entry)
	}
	if path == "" {
		return fmt.Errorf("宿主未提供账号文件路径（%s）", entry.displayLabel())
	}

	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		return fmt.Errorf("读取账号文件失败：%w", errRead)
	}

	// Decode into a map so unknown keys survive the round trip.
	var doc map[string]any
	if errUnmarshal := json.Unmarshal(raw, &doc); errUnmarshal != nil {
		return fmt.Errorf("账号文件不是合法 JSON：%w", errUnmarshal)
	}
	if cur, ok := doc["disabled"].(bool); ok && cur == disabled {
		return nil // already in the requested state
	}
	doc["disabled"] = disabled

	updated, errMarshal := json.MarshalIndent(doc, "", "  ")
	if errMarshal != nil {
		return errMarshal
	}
	if errWrite := atomicWriteFile(path, updated); errWrite != nil {
		return fmt.Errorf("写入账号文件失败：%w", errWrite)
	}
	accounts.invalidate()
	return nil
}

// resolveAuthPath finds the credential file for an entry.
//
// The auth directory is derived from the host's config summary, and the file is
// matched by the name the listing reported. This is the only reliable key when several
// credentials share an auth index.
func resolveAuthPath(entry hostAuthEntry) string {
	name := strings.TrimSpace(entry.Name)
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		if entry.ID != "" {
			name = entry.ID + ".json"
		}
	}
	if name == "" {
		return ""
	}

	dir := authDirFromHost()
	if dir == "" {
		return ""
	}
	candidate := filepath.Join(dir, filepath.Base(name))
	if _, errStat := os.Stat(candidate); errStat == nil {
		return candidate
	}
	return ""
}

// authDirFromHost returns the directory holding the auth files.
//
// The value is captured from the host's config summary during auth parsing; the
// conventional location beside the working directory is the fallback, which is where
// CPA keeps them by default.
func authDirFromHost() string {
	if cached := cachedAuthDir(); cached != "" {
		return cached
	}
	if wd, errWd := os.Getwd(); errWd == nil && wd != "" {
		return filepath.Join(wd, "auths")
	}
	return ""
}

// hostConfig caches the host's configuration summary.
//
// The summary arrives on every host call and carries the resolved auth directory, which
// is the only way to locate a credential file by name — the listing reports names but
// not paths, and CPA groups several credentials under one auth index.
type hostConfigCache struct {
	mu  sync.RWMutex
	dir string
}

var hostConfig = &hostConfigCache{}

// rememberAuthDir stores the auth directory reported by the host.
func rememberAuthDir(dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	hostConfig.mu.Lock()
	hostConfig.dir = dir
	hostConfig.mu.Unlock()
}

// cachedAuthDir returns the remembered auth directory.
func cachedAuthDir() string {
	hostConfig.mu.RLock()
	defer hostConfig.mu.RUnlock()
	return hostConfig.dir
}

// atomicWriteFile writes a file through a temp file and a rename.
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, errCreate := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if errCreate != nil {
		return errCreate
	}
	tmpName := tmp.Name()
	if _, errWrite := tmp.Write(data); errWrite != nil {
		tmp.Close()
		os.Remove(tmpName)
		return errWrite
	}
	if errSync := tmp.Sync(); errSync != nil {
		tmp.Close()
		os.Remove(tmpName)
		return errSync
	}
	if errClose := tmp.Close(); errClose != nil {
		os.Remove(tmpName)
		return errClose
	}
	if errChmod := os.Chmod(tmpName, 0o600); errChmod != nil {
		// The credential is a session token; a mode failure is worth reporting but
		// not worth discarding a successful write over.
		logError("doubao: 设置账号文件权限失败：" + errChmod.Error())
	}
	return os.Rename(tmpName, path)
}

// ---------------------------------------------------------------------------
// Realm switching
// ---------------------------------------------------------------------------

// applyRealmIsolation disables the accounts of the realm that is not selected.
//
// This is the point of the switch. The two upstreams reject each other's sessions
// with a country error, so leaving the other realm's accounts enabled means CPA keeps
// offering them to the router and every call that lands on one fails — which surfaces
// as a confusing "upstream error" rather than as a misconfiguration.
//
// Accounts of the selected realm are enabled again, so switching back restores the
// previous state. An account the operator disabled by hand stays disabled: the switch
// only re-enables what it disabled itself, tracked in the state file.
func applyRealmIsolation(target realm) (disabledOther int, reenabled int, err error) {
	tracked := loadRealmIsolation()

	for _, entry := range accounts.get() {
		if entry.Realm == "" {
			continue // unknown realm: leave the operator's choice alone
		}

		if entry.Realm == target {
			// Re-enable only if this switch is what disabled it.
			if entry.Disabled && tracked.isTracked(entry) {
				if errSet := setAuthDisabled(entry, false); errSet != nil {
					err = errSet
					continue
				}
				tracked.untrack(entry)
				reenabled++
			}
			continue
		}

		// The other realm must not stay routable.
		if !entry.Disabled {
			if errSet := setAuthDisabled(entry, true); errSet != nil {
				err = errSet
				continue
			}
			tracked.track(entry)
			disabledOther++
		}
	}

	if errSave := tracked.save(); errSave != nil && err == nil {
		err = errSave
	}
	return disabledOther, reenabled, err
}

// realmIsolation records which accounts the switch disabled.
type realmIsolation struct {
	mu      sync.Mutex
	Indexes []string `json:"disabled_indexes"`
}

func (r *realmIsolation) isTracked(e hostAuthEntry) bool {
	key := e.AuthIndex
	if key == "" {
		key = e.Path
	}
	for _, k := range r.Indexes {
		if k == key {
			return true
		}
	}
	return false
}

func (r *realmIsolation) track(e hostAuthEntry) {
	key := e.AuthIndex
	if key == "" {
		key = e.Path
	}
	if key == "" || r.isTracked(e) {
		return
	}
	r.Indexes = append(r.Indexes, key)
}

func (r *realmIsolation) untrack(e hostAuthEntry) {
	keys := map[string]bool{e.AuthIndex: true, e.Path: true}
	out := r.Indexes[:0]
	for _, k := range r.Indexes {
		if !keys[k] {
			out = append(out, k)
		}
	}
	r.Indexes = out
}

var realmIsolationMu sync.Mutex

func loadRealmIsolation() *realmIsolation {
	realmIsolationMu.Lock()
	defer realmIsolationMu.Unlock()
	r := &realmIsolation{}
	raw, errRead := os.ReadFile(realmIsolationPath())
	if errRead == nil {
		_ = json.Unmarshal(raw, r)
	}
	return r
}

func (r *realmIsolation) save() error {
	realmIsolationMu.Lock()
	defer realmIsolationMu.Unlock()
	raw, errMarshal := json.MarshalIndent(r, "", "  ")
	if errMarshal != nil {
		return errMarshal
	}
	dir := pluginStateDir()
	if errMkdir := os.MkdirAll(dir, 0o755); errMkdir != nil {
		return errMkdir
	}
	return atomicWriteFile(realmIsolationPath(), raw)
}

func realmIsolationPath() string {
	return filepath.Join(pluginStateDir(), "realm-isolation.json")
}
