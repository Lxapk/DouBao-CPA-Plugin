package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// The panel's HTTP surface.
//
// Two route kinds, and the distinction matters:
//
//	Resources  GET only, served without the management key. The page itself lives
//	           here so it renders inside the host's iframe without the user
//	           pasting a key anywhere.
//	Routes     full method support, behind the management middleware. Everything
//	           the page writes goes here.
//
// A write on a resource path silently does nothing: ServeResourceHTTP returns
// false for any method but GET, and the request then 404s. That is why every
// mutation is registered under Routes.

// managementRegistration declares the panel's routes.
func managementRegistration() managementRegistrationResponse {
	return managementRegistrationResponse{
		Resources: []pluginapi.ResourceRoute{
			{
				// The path must not be "/": CPA normalises a resource path with
				// strings.TrimRight(path, "/") and rejects the result when it
				// becomes empty, so a root resource is logged as
				// "declared invalid resource route /" and dropped — the menu
				// entry then never appears.
				Path:        "panel",
				Menu:        pluginDisplayName,
				Description: "账号、统计、能力说明与插件设置，全部集中在这一页。",
			},
		},
		Routes: []pluginapi.ManagementRoute{
			{Method: http.MethodGet, Path: "/" + pluginName + "/status", Description: "插件状态：上游、模型与当前设置。"},
			{Method: http.MethodGet, Path: "/" + pluginName + "/accounts", Description: "已授权账号，含上游、启用状态与调用次数。"},
			{Method: http.MethodPost, Path: "/" + pluginName + "/account/toggle", Description: "启用或禁用某个账号。"},
			{Method: http.MethodPost, Path: "/" + pluginName + "/account/remove", Description: "移除某个账号的本地记录。"},
			{Method: http.MethodPost, Path: "/" + pluginName + "/authorize", Description: "用粘贴的 Cookie 授权一个账号。"},
			{Method: http.MethodPost, Path: "/" + pluginName + "/realm/switch", Description: "切换默认上游并隔离另一侧账号。"},
			{Method: http.MethodGet, Path: "/" + pluginName + "/calls", Description: "最近的调用记录。"},
			{Method: http.MethodPost, Path: "/" + pluginName + "/calls/clear", Description: "清空调用记录。"},
			{Method: http.MethodGet, Path: "/" + pluginName + "/routing/status", Description: "当前路由策略与轮巡位置。"},
			{Method: http.MethodPost, Path: "/" + pluginName + "/routing/config", Description: "设置路由策略。"},
			{Method: http.MethodPost, Path: "/" + pluginName + "/routing/reset", Description: "重置轮巡位置。"},
			{Method: http.MethodGet, Path: "/" + pluginName + "/settings", Description: "当前设置 JSON。"},
			{Method: http.MethodPost, Path: "/" + pluginName + "/settings", Description: "更新设置。"},
			{Method: http.MethodGet, Path: "/" + pluginName + "/panel", Description: "豆包 / Dola 控制台页面。"},
		},
	}
}

// handleManagement answers management.handle.
//
// The request carries the path CPA already stripped of the plugin prefix, so the
// switch below matches on the tail only.
func handleManagement(request []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if len(request) > 0 {
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}
	return okEnvelope(handlePanelRoute(req))
}

// handlePanelRoute routes one panel request.
func handlePanelRoute(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	path := normalisePanelPath(req.Path)
	method := strings.ToUpper(strings.TrimSpace(req.Method))

	switch path {
	case "", "/", "/panel", "/home":
		return htmlResponse(renderMainPage())

	case "/status":
		return jsonResponse(statusDocument())

	case "/accounts":
		return jsonResponse(accountsPayload())

	case "/account/toggle":
		return toggleAccount(req.Body)

	case "/account/remove":
		return removeAccount(req.Body)

	case "/authorize":
		return authorizeAccount(req.Body)

	case "/realm/switch":
		return switchRealm(req.Body)

	case "/calls":
		return jsonResponse(map[string]any{"calls": state.logs.recent(200)})

	case "/calls/clear":
		state.logs.clear()
		return jsonResponse(map[string]any{"ok": true})

	case "/routing/status":
		result := routingStatusJSON()
		result["hint"] = routingNextHint()
		return jsonResponse(result)

	case "/routing/config":
		var body struct {
			Strategy string `json:"strategy"`
			Value    string `json:"value"`
		}
		if len(req.Body) > 0 {
			_ = json.Unmarshal(req.Body, &body)
		}
		strategy := firstNonEmpty(body.Strategy, body.Value)
		if strings.TrimSpace(strategy) == "" {
			return errorJSON(http.StatusBadRequest, "缺少 strategy")
		}
		result := applyRoutingConfig(strategy)
		result["hint"] = routingNextHint()
		return jsonResponse(result)

	case "/routing/reset":
		resetRotation()
		return jsonResponse(map[string]any{"ok": true})

	case "/settings":
		if method == http.MethodPost || method == http.MethodPut {
			return applySettingsPost(req.Body)
		}
		return jsonResponse(map[string]any{"settings": settingsJSON(state.settings.get())})

	default:
		// Unknown paths fall back to the page so a stale bookmark still lands
		// somewhere useful rather than on a bare 404.
		return htmlResponse(renderMainPage())
	}
}

// accountsPayload renders the account table.
//
// The host's auth list is the source of truth for what exists; the plugin's
// counters supply the usage numbers. Joining them here means the panel shows one
// row per credential even when that credential has never served a request.
func accountsPayload() map[string]any {
	entries := accounts.get()
	rows := make([]map[string]any, 0, len(entries))
	enabled, blocked := 0, 0

	for _, e := range entries {
		row := map[string]any{
			"auth_index": e.AuthIndex,
			"id":         e.ID,
			"name":       e.Name,
			"path":       e.Path,
			"label":      e.displayLabel(),
			"realm":      string(e.Realm),
			"realm_name": profileFor(e.Realm).DisplayName,
			"disabled":   e.Disabled,
			"status":     e.Status,
		}
		if e.StatusMessage != "" {
			row["status_message"] = e.StatusMessage
		}
		if e.SecUserID != "" {
			row["sec_user_id"] = e.SecUserID
		}

		record := stats.lookup(e.SecUserID)
		if record == nil {
			record = stats.lookup(e.displayLabel())
		}
		if record != nil {
			row["calls"] = record.CallCount
			if record.SuccessCount > 0 {
				row["success"] = record.SuccessCount
			}
			if record.FailureCount > 0 {
				row["failures"] = record.FailureCount
			}
			if record.LastError != "" {
				row["last_error"] = record.LastError
			}
			if record.AuthRejected {
				row["auth_rejected"] = true
			}
			if record.LastUsedUnix > 0 {
				row["last_used"] = humanClock(record.LastUsedUnix)
			}
		}

		switch {
		case e.Disabled:
			blocked++
		case e.AuthRejected:
			blocked++
		default:
			enabled++
		}
		rows = append(rows, row)
	}

	return map[string]any{
		"accounts": rows,
		"total":    len(rows),
		"enabled":  enabled,
		"disabled": blocked,
	}
}

// toggleAccount enables or disables one credential.
func toggleAccount(body []byte) pluginapi.ManagementResponse {
	var req struct {
		AuthIndex string `json:"auth_index"`
		Path      string `json:"path"`
		Key       string `json:"key"`
		Disabled  *bool  `json:"disabled"`
	}
	if errUnmarshal := json.Unmarshal(body, &req); errUnmarshal != nil {
		return errorJSON(http.StatusBadRequest, "无法解析请求："+errUnmarshal.Error())
	}

	var target *hostAuthEntry
	entries := accounts.get()
	for i := range entries {
		e := entries[i]
		if (req.AuthIndex != "" && e.AuthIndex == req.AuthIndex) ||
			(req.Path != "" && e.Path == req.Path) ||
			(req.Key != "" && (e.SecUserID == req.Key || e.displayLabel() == req.Key)) {
			target = &e
			break
		}
	}
	if target == nil {
		return errorJSON(http.StatusNotFound, "未找到该账号")
	}

	// An omitted flag means "flip it", which is what a single toggle button sends.
	disabled := !target.Disabled
	if req.Disabled != nil {
		disabled = *req.Disabled
	}

	if errSet := setAuthDisabled(*target, disabled); errSet != nil {
		return errorJSON(http.StatusBadGateway, "操作失败："+errSet.Error())
	}
	accounts.invalidate()
	return jsonResponse(map[string]any{
		"accounts": accountsPayload()["accounts"],
		"changed":  target.displayLabel(),
		"disabled": disabled,
	})
}

// switchRealm changes the default upstream and isolates the other realm's accounts.
//
// The two effects are deliberately one action: picking an upstream while leaving the
// other realm's accounts routable produces calls that fail with a country error, which
// reads as a broken credential rather than as a setting that needs the other side
// turned off.
func switchRealm(body []byte) pluginapi.ManagementResponse {
	var req struct {
		Realm string `json:"realm"`
	}
	if errUnmarshal := json.Unmarshal(body, &req); errUnmarshal != nil {
		return errorJSON(http.StatusBadRequest, "无法解析请求："+errUnmarshal.Error())
	}
	target := normalizeRealm(req.Realm)
	if target == "" {
		return errorJSON(http.StatusBadRequest, "未知上游，可选 doubao 或 dola")
	}

	next := state.settings.get()
	next.RealmDefault = target
	state.settings.set(next)

	persisted := true
	var persistErr string
	if errPersist := persistSettings(map[string]json.RawMessage{
		"realm_default": json.RawMessage(`"` + string(target) + `"`),
	}); errPersist != nil {
		persisted = false
		persistErr = errPersist.Error()
	}

	disabledOther, reenabled, errIsolate := applyRealmIsolation(target)
	isolateErr := ""
	if errIsolate != nil {
		isolateErr = errIsolate.Error()
	}

	return jsonResponse(map[string]any{
		"realm":          string(target),
		"realm_name":     profileFor(target).DisplayName,
		"disabled_other": disabledOther,
		"reenabled":      reenabled,
		"isolate_error":  isolateErr,
		"persisted":      persisted,
		"error":          persistErr,
		"accounts":       accountsPayload()["accounts"],
		"settings":       settingsJSON(next),
	})
}

// normalisePanelPath reduces a request path to the tail this handler switches on.
//
// The host hands over the resolved path, which may arrive under either prefix, so
// both are stripped. Everything after them is what identifies the action.
func normalisePanelPath(raw string) string {
	p := strings.TrimSpace(raw)
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	for _, prefix := range []string{
		"/v0/resource/plugins/" + pluginName,
		"/v0/management/" + pluginName,
		"/v0/management",
	} {
		if strings.HasPrefix(p, prefix) {
			p = strings.TrimPrefix(p, prefix)
			break
		}
	}
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimSuffix(p, "/")
}

func htmlResponse(body string) pluginapi.ManagementResponse {
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       []byte(body),
	}
}

func jsonResponse(v any) pluginapi.ManagementResponse {
	raw, errMarshal := json.MarshalIndent(v, "", "  ")
	if errMarshal != nil {
		raw = []byte(`{"error":"marshal failed"}`)
	}
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       raw,
	}
}

// settingsJSON renders the settings in the shape the page's script expects.
func settingsJSON(s pluginSettings) map[string]any {
	return map[string]any{
		"realm_default":           string(s.RealmDefault),
		"expose_models":           s.exposesModels(),
		"request_timeout_seconds": s.RequestTimeoutSeconds,
		"reply_poll_seconds":      s.ReplyPollSeconds,
		"media_poll_seconds":      s.MediaPollSeconds,
		"reply_poll_interval_ms":  s.ReplyPollIntervalMS,
		"debug":                   s.Debug,
		"routing":                 normalizeStrategy(s.Routing.Strategy),
	}
}

// applySettingsPost applies one settings patch.
//
// The body is a partial object: the page sends only the field that changed. The
// result is returned in full so the caller can repaint every control that depends
// on a setting, rather than guessing which ones moved.
func applySettingsPost(body []byte) pluginapi.ManagementResponse {
	var patch map[string]json.RawMessage
	if len(body) > 0 {
		if errUnmarshal := json.Unmarshal(body, &patch); errUnmarshal != nil {
			return errorJSON(http.StatusBadRequest, "无法解析设置内容："+errUnmarshal.Error())
		}
	}

	next := state.settings.get()
	if errApply := applySettingsPatch(&next, patch); errApply != nil {
		return errorJSON(http.StatusBadRequest, errApply.Error())
	}

	state.settings.set(next)

	// Persist through the host so the change survives a restart. A failure here is
	// reported rather than swallowed: the setting is live either way, but the user
	// should know it will not stick.
	persisted := true
	var persistErr string
	if errPersist := persistSettings(patch); errPersist != nil {
		persisted = false
		persistErr = errPersist.Error()
	}

	return jsonResponse(map[string]any{
		"settings":  settingsJSON(next),
		"persisted": persisted,
		"error":     persistErr,
	})
}

// applySettingsPatch folds a partial object into the settings.
func applySettingsPatch(s *pluginSettings, patch map[string]json.RawMessage) error {
	for key, raw := range patch {
		switch key {
		case "realm_default":
			var v string
			if errUnmarshal := json.Unmarshal(raw, &v); errUnmarshal != nil {
				return errField("realm_default")
			}
			r := normalizeRealm(v)
			if r == "" {
				return errUnknownRealm(v)
			}
			s.RealmDefault = r

		case "expose_models":
			var v bool
			if errUnmarshal := json.Unmarshal(raw, &v); errUnmarshal != nil {
				return errField("expose_models")
			}
			s.ExposeModels = boolPtr(v)

		case "debug":
			var v bool
			if errUnmarshal := json.Unmarshal(raw, &v); errUnmarshal != nil {
				return errField("debug")
			}
			s.Debug = v

		case "routing":
			var v routingSettings
			if errUnmarshal := json.Unmarshal(raw, &v); errUnmarshal != nil || strings.TrimSpace(v.Strategy) == "" {
				return errField("routing")
			}
			s.Routing.Strategy = normalizeStrategy(v.Strategy)

		case "request_timeout_seconds":
			n, errNum := positiveInt(raw)
			if errNum != nil {
				return errNumField("request_timeout_seconds", errNum)
			}
			s.RequestTimeoutSeconds = n

		case "reply_poll_seconds":
			n, errNum := positiveInt(raw)
			if errNum != nil {
				return errNumField("reply_poll_seconds", errNum)
			}
			s.ReplyPollSeconds = n

		case "media_poll_seconds":
			n, errNum := positiveInt(raw)
			if errNum != nil {
				return errNumField("media_poll_seconds", errNum)
			}
			s.MediaPollSeconds = n

		case "reply_poll_interval_ms":
			n, errNum := positiveInt(raw)
			if errNum != nil {
				return errNumField("reply_poll_interval_ms", errNum)
			}
			s.ReplyPollIntervalMS = n

		default:
			// An unknown key is ignored rather than rejected: the page and the
			// plugin can be deployed out of step, and refusing the whole patch
			// would make a newer page unable to save anything against an older
			// plugin.
		}
	}
	return nil
}

// positiveInt decodes a positive integer, rejecting zero and negatives.
func positiveInt(raw json.RawMessage) (int, error) {
	var v int
	if errUnmarshal := json.Unmarshal(raw, &v); errUnmarshal != nil {
		return 0, errInvalidInteger
	}
	if v <= 0 {
		return 0, errNotPositive
	}
	return v, nil
}

// errorJSON renders a failure the page can show verbatim.
func errorJSON(status int, message string) pluginapi.ManagementResponse {
	raw, _ := json.Marshal(map[string]any{"error": message})
	return pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       raw,
	}
}

// persistSettings writes the panel's changes to the plugin's own state file.
//
// CPA owns config.yaml and the host offers no callback that rewrites it, so the
// change is stored by the plugin. Only the keys the patch touched are recorded, so
// a value the operator sets by hand in config.yaml keeps applying.
func persistSettings(patch map[string]json.RawMessage) error {
	return mergePanelState(patch)
}

// The validation errors are values rather than formatted strings so the message
// each one produces stays in one place: the panel shows them verbatim, and the
// field name is the only thing that varies.
var (
	errInvalidInteger = errors.New("必须是整数")
	errNotPositive    = errors.New("必须是正整数")
)

func errField(name string) error {
	return fmt.Errorf("%s 格式错误", name)
}

func errUnknownRealm(value string) error {
	return fmt.Errorf("未知上游 %q，可选 doubao 或 dola", value)
}

func errNumField(name string, err error) error {
	return fmt.Errorf("%s %w", name, err)
}

// humanClock renders a Unix timestamp for the tables, with a dash for "never".
func humanClock(sec int64) string {
	if sec <= 0 {
		return "—"
	}
	return time.Unix(sec, 0).Format("01-02 15:04")
}

// removeAccount drops the plugin's local record for an account.
//
// The host owns the credential file and offers no delete callback, so this does
// not touch it: the row disappears from the counters, and if the credential is
// still present in the auth directory it reappears on the next refresh. That is
// deliberate — deleting a file the host is watching would depend on the host's
// reload timing, and the panel would show a state CPA disagrees with.
func removeAccount(body []byte) pluginapi.ManagementResponse {
	var req struct {
		AuthIndex string `json:"auth_index"`
		Key       string `json:"key"`
		SecUserID string `json:"sec_user_id"`
	}
	if errUnmarshal := json.Unmarshal(body, &req); errUnmarshal != nil {
		return errorJSON(http.StatusBadRequest, "无法解析请求："+errUnmarshal.Error())
	}

	key := firstNonEmpty(req.Key, req.SecUserID)
	if key == "" && req.AuthIndex != "" {
		for _, e := range accounts.get() {
			if e.AuthIndex == req.AuthIndex {
				key = firstNonEmpty(e.SecUserID, e.displayLabel())
				break
			}
		}
	}
	if key == "" {
		return errorJSON(http.StatusBadRequest, "缺少 key")
	}
	if !stats.forget(key) {
		return errorJSON(http.StatusNotFound, "没有该账号的本地记录")
	}
	accounts.invalidate()
	return jsonResponse(map[string]any{"ok": true, "key": key})
}
