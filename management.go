package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// The panel's HTTP surface.
//
// One handler covers the page and the JSON it talks to, because they share a prefix
// and the same (unauthenticated) resource context; splitting them would add
// indirection without adding safety. The page is served from
// /v0/resource/plugins/doubao, which CPA exposes without the management key, so the
// panel works in the host's iframe without the user pasting a key anywhere.

// managementRegistration declares the panel's routes.
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
// false for any method but GET, and the request then 404s. That is why the
// settings endpoint is registered under Routes.
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
				Menu:        "豆包 / Dola",
				Description: "上游状态、模型列表与插件设置，全部集中在这一页。",
			},
		},
		Routes: []pluginapi.ManagementRoute{
			{
				Method:      http.MethodGet,
				Path:        "/doubao/status",
				Description: "插件状态 JSON：上游、模型与当前设置。",
			},
			{
				Method:      http.MethodGet,
				Path:        "/doubao/settings",
				Description: "当前设置 JSON。",
			},
			{
				Method:      http.MethodPost,
				Path:        "/doubao/settings",
				Description: "更新设置（默认上游、模型可见范围、超时与轮询、日志）。",
			},
			{
				// The page is also mounted on the management path so a browser can
				// reach it directly, and so the in-page writes share one prefix
				// with the page's own origin.
				Method:      http.MethodGet,
				Path:        "/doubao/panel",
				Description: "豆包 / Dola 控制台页面。",
			},
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
//
// The paths are the tails CPA resolves after stripping the plugin prefix, so the
// resource page and the management API both arrive here and are told apart by
// their tail alone.
func handlePanelRoute(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	path := normalisePanelPath(req.Path)
	method := strings.ToUpper(strings.TrimSpace(req.Method))

	switch path {
	case "", "/", "/panel", "/home":
		return htmlResponse(renderMainPage())

	case "/status":
		return jsonResponse(statusJSON())

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
				return fmt.Errorf("realm_default 格式错误")
			}
			r := normalizeRealm(v)
			if r == "" {
				return fmt.Errorf("未知上游 %q，可选 doubao 或 dola", v)
			}
			s.RealmDefault = r

		case "expose_models":
			var v bool
			if errUnmarshal := json.Unmarshal(raw, &v); errUnmarshal != nil {
				return fmt.Errorf("expose_models 格式错误")
			}
			s.ExposeModels = boolPtr(v)

		case "debug":
			var v bool
			if errUnmarshal := json.Unmarshal(raw, &v); errUnmarshal != nil {
				return fmt.Errorf("debug 格式错误")
			}
			s.Debug = v

		case "request_timeout_seconds":
			n, errNum := positiveInt(raw)
			if errNum != nil {
				return fmt.Errorf("request_timeout_seconds %w", errNum)
			}
			s.RequestTimeoutSeconds = n

		case "reply_poll_seconds":
			n, errNum := positiveInt(raw)
			if errNum != nil {
				return fmt.Errorf("reply_poll_seconds %w", errNum)
			}
			s.ReplyPollSeconds = n

		case "media_poll_seconds":
			n, errNum := positiveInt(raw)
			if errNum != nil {
				return fmt.Errorf("media_poll_seconds %w", errNum)
			}
			s.MediaPollSeconds = n

		case "reply_poll_interval_ms":
			n, errNum := positiveInt(raw)
			if errNum != nil {
				return fmt.Errorf("reply_poll_interval_ms %w", errNum)
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
		return 0, fmt.Errorf("必须是整数")
	}
	if v <= 0 {
		return 0, fmt.Errorf("必须是正整数")
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
