package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// This file wires the doubao/dola credential handling into CPA's AuthProvider
// capability.
//
// CPA drives the flow through five RPCs:
//
//	auth.identifier   -> the provider key
//	auth.parse        -> turn pasted credential material into an AuthData
//	auth.login.start  -> begin a login, return a user-facing URL + state
//	auth.login.poll   -> poll until success, return the AuthData
//	auth.refresh      -> re-probe an existing credential
//
// Both realms sign in through a browser and authenticate with the resulting
// cookie jar. There is no OAuth token to exchange and no device-code flow, so
// login.start hands the user a URL and instructions, and login.poll accepts the
// cookies they paste back.
//
// auth.identifier returns a *provider key*, not a display name. CPA compares it
// against auth.Provider when deciding which models belong to which credential
// (pluginhost/adapters.go ModelsForAuth). A display spelling that does not match
// makes ModelsForAuth skip the plugin entirely: the account list, the per-auth
// model button and /v1/models all come back empty, with no error explaining
// why. It is therefore the plugin name, not the product name.

// pendingLogins tracks started-but-unfinished logins.
var pendingLogins = newPendingLoginStore()

// authIdentifier answers auth.identifier.
//
// Because one plugin serves two upstreams, the identifier names the plugin
// rather than a realm: the realm travels in the credential's own attributes.
// That keeps one auth page and one account list holding both kinds of account.
func authIdentifier() ([]byte, error) {
	return okEnvelope(identifierResponse{Identifier: pluginName})
}

// authParse answers auth.parse: accept pasted credential material and turn it
// into a CPA auth record.
//
// The expected input is either
//
//	the Cookie header value copied from a logged-in browser, or
//	the JSON blob this plugin writes back out (so an export re-imports)
//
// A bare cookie string is the common case, because a browser makes it easy to
// copy and hard to get wrong. The realm is inferred when it is not stated, since
// the cookies themselves carry the hint.
func authParse(request []byte) ([]byte, error) {
	var req pluginapi.AuthParseRequest
	if len(request) > 0 {
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}

	// The host reports its resolved auth directory here and nowhere else, and the
	// account panel needs it to locate a credential file by name when several
	// accounts share one auth index. Capturing it now means the panel works on the
	// first use rather than only after an unrelated call happens to populate it.
	rememberAuthDir(req.Host.AuthDir)

	// Provider filter: handle ours, let others pass untouched.
	if req.Provider != "" && !strings.EqualFold(req.Provider, pluginName) {
		return okEnvelope(map[string]any{"Handled": false})
	}

	creds, errExtract := extractCredentials(req)
	if errExtract != nil {
		return okEnvelope(map[string]any{
			"Handled": true,
			"Error":   errExtract.Error(),
		})
	}

	return okEnvelope(map[string]any{
		"Handled": true,
		"Auth":    authDataFor(creds),
	})
}

// extractCredentials pulls credentials out of whatever the host supplied.
//
// AuthParseRequest carries RawJSON plus file/path hints, so the material always
// arrives as bytes. Accepted shapes, in order of likelihood:
//
//  1. our own storage JSON (re-import, or the host echoing a saved record)
//  2. a wrapped {"cookies":"...","realm":"dola"} object
//  3. a bare Cookie header value
//  4. a "Cookie: a=b; c=d" line copied straight out of DevTools
func extractCredentials(req pluginapi.AuthParseRequest) (*credentials, error) {
	raw := strings.TrimSpace(firstNonEmpty(string(req.RawJSON), req.FileName))
	if raw == "" {
		return nil, errors.New("未提供任何凭据内容")
	}

	if strings.HasPrefix(raw, "{") {
		// 1) our own persisted shape
		if creds, errStorage := credentialsFromStorage([]byte(raw)); errStorage == nil && creds.Cookies != "" {
			return finishCredential(creds)
		}
		// 2) a hand-written wrapper
		var wrapper struct {
			Realm   string `json:"realm"`
			Cookies string `json:"cookies"`
			UID     string `json:"uid"`
			BotID   string `json:"bot_id"`
		}
		if errUnmarshal := json.Unmarshal([]byte(raw), &wrapper); errUnmarshal == nil && wrapper.Cookies != "" {
			return finishCredential(&credentials{
				Realm:   normalizeRealm(wrapper.Realm),
				Cookies: wrapper.Cookies,
				UID:     wrapper.UID,
				BotID:   wrapper.BotID,
			})
		}
		// Anything else that starts with "{" is treated as opaque, which
		// produces the "missing flow_cur_user_sec_id" error below and tells the
		// user what to paste instead.
	}

	// 3) / 4) a raw or prefixed Cookie header
	return finishCredential(&credentials{Cookies: stripCookiePrefix(raw)})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// stripCookiePrefix drops a leading "Cookie:" and unwraps quoting.
func stripCookiePrefix(raw string) string {
	s := raw
	if idx := strings.Index(strings.ToLower(s), "cookie:"); idx >= 0 && idx < 8 {
		s = s[idx+len("cookie:"):]
	}
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"")
	return strings.TrimSpace(s)
}

// finishCredential fills in the realm when it was not stated and validates.
func finishCredential(c *credentials) (*credentials, error) {
	// Normalise the cookie header once, at ingestion. A pasted value frequently
	// contains newlines, and Go's HTTP client refuses to send a header value
	// containing one — normalising here means a credential is either stored
	// clean or rejected, rather than failing later at request time.
	c.Cookies = normalizeCookieSeparators(c.Cookies)
	if c.Realm == "" {
		c.Realm = inferRealm(c)
	}
	if errValidate := c.validate(); errValidate != nil {
		return nil, errValidate
	}
	return c, nil
}

// inferRealm guesses the realm from the credential's own contents.
//
// The cookie jar names the realm implicitly: a doubao session carries keys a
// dola session does not, and vice versa. When the jar is ambiguous the
// configured default is used, which is why it is a setting and not a constant.
func inferRealm(c *credentials) realm {
	if c.cookie("bd_sso_hi3jfd") != "" {
		return realmDoubao
	}
	if c.cookie("__tea_session_id_495671") != "" {
		return realmDola
	}
	if r := state.settings.get().RealmDefault; r != "" {
		return r
	}
	return realmDoubao
}

// authDataFor converts a credential into the CPA auth record.
func authDataFor(creds *credentials) pluginapi.AuthData {
	filled := creds.withDefaults()
	storage, errStorage := filled.toStorage()
	if errStorage != nil {
		// A credential that cannot be persisted is still usable for this
		// request; CPA will re-parse it next time.
		storage = nil
	}

	attrs := map[string]string{
		"provider": string(filled.Realm),
		"realm":    string(filled.Realm),
	}
	if filled.SecUserID != "" {
		attrs["sec_user_id"] = filled.SecUserID
	}
	if filled.UID != "" {
		attrs["uid"] = filled.UID
	}

	return pluginapi.AuthData{
		Provider:    pluginName,
		ID:          authIDFor(filled),
		FileName:    authFileNameFor(filled),
		Label:       fmt.Sprintf("%s · %s", filled.profile().DisplayName, filled.label()),
		StorageJSON: storage,
		Attributes:  attrs,
		Metadata: map[string]any{
			"realm": string(filled.Realm),
		},
	}
}

// authIDFor builds the stable auth id.
//
// It is realm-prefixed so the same account signed in to both upstreams yields
// two distinguishable records; they are genuinely different sessions.
func authIDFor(c *credentials) string {
	suffix := c.SecUserID
	if suffix == "" {
		suffix = c.UID
	}
	if suffix == "" {
		suffix = randomNumericID()
	}
	return sanitizeID(string(c.Realm) + "-" + suffix)
}

// authFileNameFor names the persisted auth file.
func authFileNameFor(c *credentials) string {
	return authIDFor(c) + ".json"
}

// authLoginStart answers auth.login.start.
//
// Both realms authenticate by browser sign-in, so there is no callback to
// receive and no device code to display. The flow hands the user the sign-in URL
// and the steps for copying the cookie back; the state token ties the follow-up
// poll to this realm.
func authLoginStart(request []byte) ([]byte, error) {
	var req pluginapi.AuthLoginStartRequest
	if len(request) > 0 {
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}

	r := normalizeRealm(authVariantHint(req))
	if r == "" {
		r = state.settings.get().RealmDefault
	}
	profile := profileFor(r)

	// The state must satisfy CPA's ValidateOAuthState, which allows only
	// [A-Za-z0-9._-]. A separator such as ':' is rejected outright with
	// "invalid oauth state", so the realm is joined with a hyphen.
	loginState := string(r) + "-" + randomUUID()
	pendingLogins.put(loginState, &pendingLogin{Realm: r})

	host := strings.TrimPrefix(profile.CookieDomain, ".")
	return okEnvelope(pluginapi.AuthLoginStartResponse{
		Provider:  pluginName,
		URL:       profile.Host + "/chat/",
		State:     loginState,
		ExpiresAt: time.Now().Add(pendingLoginTTL),
		Metadata: map[string]any{
			"realm": string(r),
			"instructions": fmt.Sprintf(
				"1. 在浏览器中打开 %s 并完成登录。\n"+
					"2. 按 F12 打开开发者工具，切到 Network 并刷新页面。\n"+
					"3. 点击任意一条 %s 的请求，在 Request Headers 里找到 Cookie。\n"+
					"4. 复制 Cookie 的完整值（不要带 “Cookie:” 前缀），粘贴回这里。\n\n"+
					"注意：必须是登录后的完整 Cookie，其中要包含 flow_cur_user_sec_id 与 sessionid。",
				profile.Host, host,
			),
		},
	})
}

// authVariantHint reads a realm hint off the login request.
//
// CPA passes through whatever the panel sent, so both the free-form Metadata map
// and the Provider spelling are checked; the hint then survives either wiring.
func authVariantHint(req pluginapi.AuthLoginStartRequest) string {
	for _, key := range []string{"realm", "variant", "provider", "login_hint"} {
		if v, ok := req.Metadata[key]; ok {
			if s, isStr := v.(string); isStr {
				if r := normalizeRealm(s); r != "" {
					return string(r)
				}
			}
		}
	}
	if r := normalizeRealm(req.Provider); r != "" {
		return string(r)
	}
	if r := realmFromHost(req.BaseURL); r != "" {
		return string(r)
	}
	return ""
}

// authLoginPoll answers auth.login.poll.
//
// The pasted cookies arrive here, in Metadata. The credential is validated
// against the live upstream before it is accepted, so a stale or partial copy
// fails immediately with a specific message instead of producing an account that
// fails every later request.
func authLoginPoll(request []byte) ([]byte, error) {
	var req pluginapi.AuthLoginPollRequest
	if len(request) > 0 {
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}

	// The panel delivers the pasted value through Metadata; a few spellings are
	// accepted so the field name is not a silent failure mode.
	material := strings.TrimSpace(pollMaterial(req))
	if material == "" {
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "等待粘贴 Cookie…",
		})
	}

	pending := pendingLogins.take(req.State)

	creds, errExtract := extractCredentials(pluginapi.AuthParseRequest{RawJSON: []byte(material)})
	if errExtract != nil {
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: errExtract.Error(),
		})
	}
	if pending != nil && pending.Realm != "" {
		creds.Realm = pending.Realm
	}
	if creds.Realm == "" {
		creds.Realm = state.settings.get().RealmDefault
	}

	// Validate against the live upstream. This is what distinguishes "the user
	// pasted something cookie-shaped" from "the account actually works".
	probe, errProbe := probeCredential(creds)
	if errProbe != nil {
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "凭据校验失败：" + errProbe.Error(),
		})
	}

	creds.applyLaunch(probe)

	return okEnvelope(pluginapi.AuthLoginPollResponse{
		Status: pluginapi.AuthLoginStatusSuccess,
		Auth:   authDataFor(creds),
	})
}

// pollMaterial pulls the pasted cookie value out of the poll metadata.
func pollMaterial(req pluginapi.AuthLoginPollRequest) string {
	for _, key := range []string{"cookies", "cookie", "raw", "raw_json", "content", "value"} {
		if v, ok := req.Metadata[key]; ok {
			if s, isStr := v.(string); isStr && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	// Some panels echo the material back in the state field itself.
	if strings.Contains(req.State, "=") && strings.Contains(req.State, ";") {
		return req.State
	}
	return ""
}

// authRefresh answers auth.refresh.
//
// The session cookies are the whole credential and cannot be renewed without the
// user, so a refresh re-probes the upstream: it learns whether the session is
// still alive and picks up a rotated flow_cur_user_sec_id. A dead session
// reports an error so CPA marks the credential for re-authorisation instead of
// retrying it forever.
func authRefresh(request []byte) ([]byte, error) {
	var req pluginapi.AuthRefreshRequest
	if len(request) > 0 {
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}

	if len(req.StorageJSON) == 0 {
		return nil, errors.New("凭据内容为空，无法刷新")
	}
	creds, errCreds := credentialsFromStorage(req.StorageJSON)
	if errCreds != nil {
		return nil, errCreds
	}
	if errValidate := creds.validate(); errValidate != nil {
		return nil, errValidate
	}

	probe, errProbe := probeCredential(creds)
	if errProbe != nil {
		return nil, fmt.Errorf("凭据已失效，请重新授权：%w", errProbe)
	}

	creds.applyLaunch(probe)
	return okEnvelope(map[string]any{"Auth": authDataFor(creds)})
}

// applyLaunch folds the launch response back into the credential.
//
// The response is authoritative for the account handle and assistant id, and it
// may have rotated the session bindings since the credential was saved.
func (c *credentials) applyLaunch(r *launchResult) {
	if r == nil {
		return
	}
	if r.SecUserID != "" {
		c.SecUserID = r.SecUserID
	}
	if r.AssistantBotID != "" {
		c.BotID = r.AssistantBotID
	}
}

// saveAuthThroughHost writes an auth record via the host.
func saveAuthThroughHost(auth pluginapi.AuthData) error {
	return callHostInto("auth.save", map[string]any{"auth": auth}, nil)
}

// sanitizeID makes a string safe for use as a file name.
func sanitizeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 96 {
		out = out[:96]
	}
	if out == "" {
		out = pluginName + "-" + randomUUID()
	}
	return out
}
