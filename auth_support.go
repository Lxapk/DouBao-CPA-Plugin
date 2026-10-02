package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// pendingLogin is a login that has started but not finished.
type pendingLogin struct {
	Realm realm
	At    time.Time
}

// pendingLoginStore holds in-flight logins, keyed by state.
//
// Entries expire: a login that is never completed must not pin memory for the
// life of the process, and a stale state must not be usable later.
type pendingLoginStore struct {
	mu    sync.Mutex
	items map[string]*pendingLogin
}

func newPendingLoginStore() *pendingLoginStore {
	return &pendingLoginStore{items: make(map[string]*pendingLogin)}
}

const pendingLoginTTL = 30 * time.Minute

func (s *pendingLoginStore) put(state string, p *pendingLogin) {
	if state == "" {
		return
	}
	if p.At.IsZero() {
		p.At = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	s.items[state] = p
}

// take removes and returns the entry, or nil when it is absent or expired.
func (s *pendingLoginStore) take(state string) *pendingLogin {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	p, ok := s.items[state]
	if !ok {
		return nil
	}
	delete(s.items, state)
	return p
}

func (s *pendingLoginStore) expireLocked() {
	cutoff := time.Now().Add(-pendingLoginTTL)
	for k, v := range s.items {
		if v.At.Before(cutoff) {
			delete(s.items, k)
		}
	}
}

// credentialsFromRequest finds credentials inside an executor or refresh
// request.
//
// Precedence: the request's own auth material first, then the stored auth
// record CPA resolved for the request. The stored form is the common path; the
// inline form exists so a one-off call with explicit material works.
func credentialsFromRequest(request []byte) (*credentials, error) {
	if len(request) > 0 {
		var probe struct {
			Auth      *pluginapi.AuthData `json:"auth"`
			AuthIndex string              `json:"auth_index"`
			Storage   json.RawMessage     `json:"storage_json"`
		}
		if errUnmarshal := json.Unmarshal(request, &probe); errUnmarshal == nil {
			if probe.Auth != nil && len(probe.Auth.StorageJSON) > 0 {
				if creds, errCreds := credentialsFromStorage(probe.Auth.StorageJSON); errCreds == nil {
					return finishCredential(creds)
				}
			}
			if len(probe.Storage) > 0 {
				if creds, errCreds := credentialsFromStorage(probe.Storage); errCreds == nil {
					return finishCredential(creds)
				}
			}
		}
	}
	return nil, errors.New("请求中没有可用的凭据")
}

// probeCredential validates a credential against the live upstream.
//
// /alice/user/launch is the cheapest authenticated call: it needs no
// conversation, and a success proves the whole session works, because the
// gateway checks the same bindings it will check on a chat send.
func probeCredential(c *credentials) (*launchResult, error) {
	client := newClient(c)
	client.debug = debugLogger()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, errLaunch := client.launch(ctx)
	if errLaunch != nil {
		return nil, errLaunch
	}
	return result, nil
}

// debugLogger returns a logger that respects the debug setting.
func debugLogger() func(string, ...any) {
	if !state.settings.get().Debug {
		return nil
	}
	return func(format string, args ...any) {
		logInfo("doubao: " + fmt.Sprintf(format, args...))
	}
}

// logInfo emits a host log line.
//
// The host owns logging, so this goes through the host RPC rather than stdout:
// a plugin that writes to stdout would interleave with CPA's own output.
func logInfo(message string) {
	_, _ = callHost("host.log", map[string]any{"level": "info", "message": message})
}

// logError emits a host error line.
func logError(message string) {
	_, _ = callHost("host.log", map[string]any{"level": "error", "message": message})
}

// realmOf resolves the realm a request targets, honouring the credential.
func realmOf(c *credentials) realm {
	if c != nil && c.Realm != "" {
		return c.Realm
	}
	if r := state.settings.get().RealmDefault; r != "" {
		return r
	}
	return realmDoubao
}

// modelPrefixFor renders the client-facing prefix for a realm.
//
// The two upstreams expose identically named models, so a prefix is what keeps
// them distinguishable in /v1/models and lets a request pick a side. Requesting
// the bare name resolves to the default realm.
func modelPrefixFor(r realm) string {
	return string(r) + "/"
}

// stripModelPrefix removes a realm prefix, returning the bare model and the
// realm it named (empty when the name carried no prefix).
func stripModelPrefix(name string) (string, realm) {
	if idx := strings.Index(name, "/"); idx > 0 {
		if r := normalizeRealm(name[:idx]); r != "" {
			return name[idx+1:], r
		}
	}
	return name, ""
}
