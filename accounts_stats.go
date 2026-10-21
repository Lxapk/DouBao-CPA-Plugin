package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Per-account counters.
//
// The credential itself lives in CPA's auth store; this file only holds what the
// host does not track — how often each account has served a request, how it
// failed, and when it was last used. The 账号 and 统计 pages read it, and the
// by_usage routing strategy orders candidates by it.
//
// The store is persisted as accounts.json in the plugin's state directory so the
// numbers survive a restart. Keys are the account handle (the upstream
// sec_user_id), which is stable across re-authorisations; falling back to the
// label keeps pre-handle records readable.

// accountStats is one account's counters.
type accountStats struct {
	Key          string `json:"key"`
	Label        string `json:"label,omitempty"`
	Realm        string `json:"realm,omitempty"`
	CallCount    int64  `json:"call_count"`
	SuccessCount int64  `json:"success_count"`
	FailureCount int64  `json:"failure_count"`
	LastUsedUnix int64  `json:"last_used_unix,omitempty"`
	LastError    string `json:"last_error,omitempty"`
	// AuthRejected is advisory: it drives the panel's "需重新授权" badge and is
	// cleared by the next successful call. It never decides usability, because a
	// stale flag would make a healthy account unreachable.
	AuthRejected bool `json:"auth_rejected,omitempty"`
}

type statsStore struct {
	mu    sync.RWMutex
	items map[string]*accountStats
}

var stats = &statsStore{items: map[string]*accountStats{}}

func statsPath() string {
	return filepath.Join(pluginStateDir(), "accounts.json")
}

// loadStats rehydrates the counters. A missing or corrupt file is not an error:
// the counters are diagnostics and must never stop the plugin from serving.
func loadStats() {
	raw, errRead := os.ReadFile(statsPath())
	if errRead != nil {
		return
	}
	var items map[string]*accountStats
	if errUnmarshal := json.Unmarshal(raw, &items); errUnmarshal != nil || items == nil {
		return
	}
	stats.mu.Lock()
	stats.items = items
	stats.mu.Unlock()
}

func (s *statsStore) save() {
	s.mu.RLock()
	raw, errMarshal := json.MarshalIndent(s.items, "", "  ")
	s.mu.RUnlock()
	if errMarshal != nil {
		return
	}
	dir := pluginStateDir()
	if errMkdir := os.MkdirAll(dir, 0o755); errMkdir != nil {
		logError("doubao: 创建数据目录失败：" + errMkdir.Error())
		return
	}
	path := statsPath()
	tmp := path + ".tmp"
	if errWrite := os.WriteFile(tmp, raw, 0o644); errWrite != nil {
		return
	}
	if errRename := os.Rename(tmp, path); errRename != nil {
		_ = os.Remove(tmp)
	}
}

// statsKey is the identity used for a counter row.
func statsKey(creds *credentials) string {
	if creds == nil {
		return ""
	}
	if key := strings.TrimSpace(creds.SecUserID); key != "" {
		return key
	}
	if key := strings.TrimSpace(creds.UID); key != "" {
		return key
	}
	return strings.TrimSpace(creds.label())
}

// noteSuccess records a served request.
func (s *statsStore) noteSuccess(creds *credentials) {
	key := statsKey(creds)
	if key == "" {
		return
	}
	s.mu.Lock()
	row, ok := s.items[key]
	if !ok {
		row = &accountStats{Key: key}
		s.items[key] = row
	}
	row.Label = creds.label()
	row.Realm = string(creds.Realm)
	row.CallCount++
	row.SuccessCount++
	row.LastUsedUnix = time.Now().Unix()
	row.LastError = ""
	row.AuthRejected = false
	s.mu.Unlock()
	s.save()
}

// noteFailure records a failed request. kind is the classification from
// classifyFailure, so an auth rejection is surfaced while a timeout is not.
func (s *statsStore) noteFailure(creds *credentials, reason string, kind failureKind) {
	key := statsKey(creds)
	if key == "" {
		return
	}
	s.mu.Lock()
	row, ok := s.items[key]
	if !ok {
		row = &accountStats{Key: key}
		s.items[key] = row
	}
	row.Label = creds.label()
	row.Realm = string(creds.Realm)
	row.CallCount++
	row.FailureCount++
	row.LastError = reason
	if kind == failureAuth {
		row.AuthRejected = true
	}
	s.mu.Unlock()
	s.save()
}

// lookup returns a copy of one row, or nil.
func (s *statsStore) lookup(key string) *accountStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.items[strings.TrimSpace(key)]
	if !ok {
		return nil
	}
	copied := *row
	return &copied
}

// callCount is the by_usage ordering input; an unseen account counts as zero.
func (s *statsStore) callCount(keys ...string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if row, ok := s.items[key]; ok {
			return row.CallCount
		}
	}
	return 0
}

// forget drops one row, reporting whether there was anything to drop.
func (s *statsStore) forget(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	s.mu.Lock()
	_, ok := s.items[key]
	delete(s.items, key)
	s.mu.Unlock()
	if ok {
		s.save()
	}
	return ok
}
