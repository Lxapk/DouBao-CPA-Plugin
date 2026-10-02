package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Authorising an account from the plugin's own panel.
//
// CPA's management UI turns every plugin login into an OAuth-shaped flow: it shows a
// single box labelled for a callback URL, parses what the user pastes with url.Query()
// and rejects the request outright when it cannot find a "state" parameter —
// "state is required" comes from CPA, before the plugin is consulted at all.
//
// A Doubao credential is a cookie, not an OAuth code. Pasting it into that box fails,
// and the only way to satisfy CPA is to hand the user a URL-encoding exercise:
// "?state=…&code=<url-encoded cookie>", where every '&' and '=' inside the cookie has
// to be escaped. That is not an acceptable thing to ask for a routine sign-in.
//
// So the panel does the whole thing itself: the user pastes the cookie into the
// plugin's own field, the plugin validates it against the live upstream, and it writes
// the credential file directly. CPA picks the file up from its auth directory the same
// way it picks up one written by its own login flow — no patch to CPA, no callback box.
//
// The auth provider flow is left in place so the credential can still be re-parsed and
// refreshed through the host; it is simply no longer the path a user is sent down.

// authorizeRequest is the panel's paste target.
type authorizeRequest struct {
	// Cookies is the pasted value: a raw Cookie header, or a "k=v; k=v" fragment.
	Cookies string `json:"cookies"`
	// Realm optionally pins the upstream; empty means infer from the cookie, then
	// fall back to the configured default.
	Realm string `json:"realm"`
	// Label is an optional display name for the account list.
	Label string `json:"label"`
}

// authorizeResult reports what happened, in enough detail to act on.
type authorizeResult struct {
	OK          bool     `json:"ok"`
	Realm       string   `json:"realm,omitempty"`
	RealmName   string   `json:"realm_name,omitempty"`
	Label       string   `json:"label,omitempty"`
	SecUserID   string   `json:"sec_user_id,omitempty"`
	ModelCount  int      `json:"model_count,omitempty"`
	ExpiresAt   string   `json:"expires_at,omitempty"`
	Path        string   `json:"path,omitempty"`
	Error       string   `json:"error,omitempty"`
	ErrorHint   string   `json:"error_hint,omitempty"`
	AlreadyHeld bool     `json:"already_held,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// authorizeAccount validates a pasted cookie and stores it as an auth record.
func authorizeAccount(body []byte) pluginapi.ManagementResponse {
	var req authorizeRequest
	if errUnmarshal := json.Unmarshal(body, &req); errUnmarshal != nil {
		return errorJSON(http.StatusBadRequest, "无法解析请求："+errUnmarshal.Error())
	}

	cookies := normaliseCookieInput(req.Cookies)
	if cookies == "" {
		return errorJSON(http.StatusBadRequest, "请先粘贴 Cookie。")
	}

	// The realm decides which host is probed, so it has to be settled before the
	// network call rather than after.
	r := normalizeRealm(req.Realm)
	if r == "" {
		r = inferRealmFromCookies(cookies)
	}
	if r == "" {
		r = state.settings.get().RealmDefault
	}
	if r == "" {
		r = realmDoubao
	}

	creds := &credentials{Realm: r, Cookies: cookies}
	probe, errProbe := probeCredential(creds.withDefaults())
	if errProbe != nil {
		res := authorizeResult{
			OK:    false,
			Error: errProbe.Error(),
		}
		if hint := authorizeHintFor(errProbe); hint != "" {
			res.ErrorHint = hint
		}
		res.Realm = string(r)
		res.RealmName = profileFor(r).DisplayName
		// 400 rather than 502: the request is well-formed, the credential is not
		// usable, and the user needs to fix the input rather than retry.
		return jsonResponseWithStatus(http.StatusBadRequest, res)
	}

	creds.Realm = r
	creds.applyLaunch(probe)

	path, errSave := writeAuthRecord(creds, probe, req.Label)
	if errSave != nil {
		return errorJSON(http.StatusInternalServerError, "账号已通过校验，但写入失败："+errSave.Error())
	}

	accounts.invalidate()

	res := authorizeResult{
		OK:         true,
		Realm:      string(r),
		RealmName:  profileFor(r).DisplayName,
		Label:      accountLabelFor(creds, probe, req.Label),
		SecUserID:  probe.SecUserID,
		ModelCount: len(probe.Models),
		Path:       path,
	}
	if !creds.ExpiresAt.IsZero() {
		res.ExpiresAt = creds.ExpiresAt.Format(time.RFC3339)
	}
	// A model list that is empty makes the account useless even though the probe
	// succeeded, which is worth saying plainly rather than leaving the user to
	// discover it when their first request fails.
	if len(probe.Models) == 0 {
		res.Warnings = append(res.Warnings,
			"该账号未返回任何模型，可能是账号状态异常或权限受限。")
	}
	return jsonResponse(res)
}

// normaliseCookieInput cleans what the user pasted.
//
// People paste the whole "Cookie:" header line, or a value wrapped in quotes, or
// something with the browser's line wrapping still in it. Normalising here turns those
// into a credential instead of an "invalid cookie" rejection that gives no clue.
func normaliseCookieInput(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	// "Cookie: a=b; c=d" — the header name is not part of the value.
	if idx := strings.IndexByte(v, ':'); idx > 0 {
		head := strings.ToLower(strings.TrimSpace(v[:idx]))
		if head == "cookie" {
			v = strings.TrimSpace(v[idx+1:])
		}
	}
	// A value copied from DevTools is sometimes quoted.
	v = strings.Trim(v, `"'`)
	// Continuation lines from a wrapped paste.
	v = strings.ReplaceAll(v, "\r", "")
	v = strings.ReplaceAll(v, "\n", " ")
	for strings.Contains(v, "  ") {
		v = strings.ReplaceAll(v, "  ", " ")
	}
	v = strings.TrimSpace(v)

	// A pasted URL (the shape CPA's box wants) still carries the cookie in code=.
	if strings.Contains(v, "code=") {
		v = decodeSubmittedValue(v)
	}
	return strings.TrimSpace(v)
}

// inferRealmFromCookies guesses the upstream from cookie names.
//
// Only a hint: the probe is what decides. A mis-guess costs one failed request and
// reports the wrong host, which the hint below turns into an actionable message.
func inferRealmFromCookies(cookies string) realm {
	lower := strings.ToLower(cookies)
	switch {
	case strings.Contains(lower, "dola_"):
		return realmDola
	case strings.Contains(lower, "flow_cur_user_sec_id"), strings.Contains(lower, "doubao"):
		return realmDoubao
	}
	return ""
}

// authorizeHintFor turns a probe failure into something the user can act on.
func authorizeHintFor(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "710022003"), strings.Contains(msg, "country"):
		return "这是另一侧上游的账号。请在上方把「授权归属」切换成正确的站点后重试。"
	case strings.Contains(msg, "未返回账号信息"), strings.Contains(msg, "登录态无效"):
		return "Cookie 里缺少 flow_cur_user_sec_id，或它已过期。请重新登录后再复制一次完整 Cookie。"
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return "探测上游超时，可能是网络问题。稍后重试。"
	}
	return ""
}

// writeAuthRecord stores the credential where CPA will load it.
//
// The file is named after the account handle so a re-authorisation of the same account
// overwrites its own record instead of piling up duplicates — which is what a
// timestamp- or random-suffixed name would do, and it would be invisible until the
// account list filled with the same login repeated.
func writeAuthRecord(creds *credentials, probe *launchResult, label string) (string, error) {
	dir := authDirFromHost()
	if dir == "" {
		return "", fmt.Errorf("无法确定 CPA 的 auth 目录")
	}
	// Refuse to write into a guessed directory. The file would be valid and CPA
	// would never read it, so the failure would surface as "the account did not
	// appear" with no indication that a file was written somewhere else.
	if !authDirIsTrusted() {
		return "", fmt.Errorf(
			"尚未获得 CPA 的 auth 目录（当前猜测为 %s）。请先重启 CPA 让插件完成注册，再重试授权。", dir)
	}
	if errMkdir := os.MkdirAll(dir, 0o700); errMkdir != nil {
		return "", errMkdir
	}

	handle := strings.TrimSpace(probe.SecUserID)
	if handle == "" {
		return "", fmt.Errorf("上游未返回账号标识")
	}

	record := map[string]any{
		"type":        pluginName,
		"realm":       string(creds.Realm),
		"cookies":     creds.Cookies,
		"sec_user_id": handle,
		"bot_id":      creds.BotID,
		"device_id":   creds.DeviceID,
		"web_id":      creds.WebID,
		"web_tab_id":  creds.WebTabID,
		"label":       accountLabelFor(creds, probe, label),
		"disabled":    false,
		"saved_at":    time.Now().UTC().Format(time.RFC3339),
	}
	if !creds.ExpiresAt.IsZero() {
		record["expires_at"] = creds.ExpiresAt.UTC().Format(time.RFC3339)
	}

	raw, errMarshal := json.MarshalIndent(record, "", "  ")
	if errMarshal != nil {
		return "", errMarshal
	}

	// The handle is server-issued but goes into a file name, so it is restricted to
	// characters that cannot escape the directory.
	safe := sanitiseForFileName(handle)
	if safe == "" {
		return "", fmt.Errorf("账号标识含非法字符")
	}
	path := filepath.Join(dir, pluginName+"-"+safe+".json")

	if errWrite := atomicWriteFile(path, raw); errWrite != nil {
		return "", errWrite
	}
	return path, nil
}

// accountLabelFor builds the display name shown in the account list.
func accountLabelFor(creds *credentials, probe *launchResult, label string) string {
	if v := strings.TrimSpace(label); v != "" {
		return v
	}
	profile := profileFor(creds.Realm)
	short := probe.SecUserID
	if len(short) > 8 {
		short = short[:8]
	}
	if short == "" {
		return profile.DisplayName + " · 账号"
	}
	return profile.DisplayName + " · " + short
}

// sanitiseForFileName keeps characters that are safe in a path segment.
func sanitiseForFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	out := b.String()
	// "." and ".." would be path-relative rather than a file name.
	if out == "." || out == ".." {
		return ""
	}
	return out
}

// jsonResponseWithStatus renders a JSON body with a non-200 status.
func jsonResponseWithStatus(status int, v any) pluginapi.ManagementResponse {
	raw, errMarshal := json.MarshalIndent(v, "", "  ")
	if errMarshal != nil {
		raw = []byte(`{"error":"marshal failed"}`)
	}
	return pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       raw,
	}
}
