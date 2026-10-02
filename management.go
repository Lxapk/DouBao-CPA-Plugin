package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// The management panel.
//
// CPA mounts plugin resources under /v0/resource/plugins/<id>/ and plugin
// management routes under /v0/management/<id>. Resource routes are
// browser-navigable and not management-authenticated, which is what makes them
// suitable for the HTML page; the status JSON lives there too so the page can
// read it without a second auth context.

// managementRegistration answers management.register.
func managementRegistration() managementRegistrationResponse {
	return managementRegistrationResponse{
		Resources: []pluginapi.ResourceRoute{
			{
				Path:        "/panel",
				Menu:        "豆包 / Dola",
				Description: "查看上游账号状态与可用模型，并查看授权步骤。",
			},
			{
				Path:        "/status",
				Description: "插件状态 JSON：上游、模型与授权情况。",
			},
		},
	}
}

// handleManagement answers management.handle.
func handleManagement(request []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if len(request) > 0 {
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}

	path := strings.TrimSuffix(req.Path, "/")
	switch {
	case strings.HasSuffix(path, "/status"):
		return okEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			Body:       statusJSON(),
		})
	default:
		return okEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:       []byte(panelHTML()),
		})
	}
}

// statusSnapshot is the plugin status document.
type statusSnapshot struct {
	Plugin       string           `json:"plugin"`
	Version      string           `json:"version"`
	DefaultRealm string           `json:"default_realm"`
	Realms       []realmStatus    `json:"realms"`
	Models       []map[string]any `json:"models"`
	Settings     map[string]any   `json:"settings"`
}

type realmStatus struct {
	Realm       string `json:"realm"`
	DisplayName string `json:"display_name"`
	Host        string `json:"host"`
	AID         string `json:"aid"`
	Region      string `json:"region"`
	BotID       string `json:"bot_id"`
	ModelCount  int    `json:"model_count"`
}

func statusJSON() []byte {
	settings := state.settings.get()
	out := statusSnapshot{
		Plugin:       pluginName,
		Version:      pluginVersion,
		DefaultRealm: string(settings.RealmDefault),
		Settings: map[string]any{
			"expose_models":           settings.exposesModels(),
			"request_timeout_seconds": settings.RequestTimeoutSeconds,
			"reply_poll_seconds":      settings.ReplyPollSeconds,
			"reply_poll_interval_ms":  settings.ReplyPollIntervalMS,
			"debug":                   settings.Debug,
		},
		Models: modelDisplayList(),
	}
	for _, r := range allRealms {
		p := profileFor(r)
		out.Realms = append(out.Realms, realmStatus{
			Realm:       string(r),
			DisplayName: p.DisplayName,
			Host:        p.Host,
			AID:         p.AID,
			Region:      p.Region,
			BotID:       defaultBotIDFor(r),
			ModelCount:  len(catalogueFor(r)),
		})
	}
	raw, _ := json.MarshalIndent(out, "", "  ")
	return raw
}

// panelHTML renders the plugin's page.
//
// It is deliberately self-contained: no external requests, no framework. A
// plugin page that depends on a CDN fails in exactly the environments this proxy
// is used in.
func panelHTML() string {
	settings := state.settings.get()
	var realmRows strings.Builder
	for _, r := range allRealms {
		p := profileFor(r)
		mark := ""
		if r == settings.RealmDefault {
			mark = ` <span class="tag">默认</span>`
		}
		realmRows.WriteString(fmt.Sprintf(
			`<tr><td><code>%s</code></td><td>%s%s</td><td><code>%s</code></td><td>%s</td><td>%s</td></tr>`,
			r, p.DisplayName, mark, p.Host, p.AID, p.Region,
		))
	}

	var modelRows strings.Builder
	for _, m := range modelDisplayList() {
		modelRows.WriteString(fmt.Sprintf(
			`<tr><td><code>%s</code></td><td>%s</td><td>%s</td></tr>`,
			htmlEscape(fmt.Sprint(m["id"])),
			htmlEscape(fmt.Sprint(m["owned_by"])),
			htmlEscape(fmt.Sprint(m["description"])),
		))
	}

	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>豆包 / Dola 插件</title>
<style>
:root { color-scheme: light dark; }
body { font-family: system-ui, -apple-system, "Segoe UI", sans-serif; margin: 0; padding: 24px; line-height: 1.6; }
h1 { font-size: 20px; margin: 0 0 4px; }
h2 { font-size: 15px; margin: 28px 0 8px; }
.sub { opacity: .65; font-size: 13px; margin-bottom: 8px; }
table { border-collapse: collapse; width: 100%; font-size: 13px; }
th, td { text-align: left; padding: 7px 10px; border-bottom: 1px solid rgba(128,128,128,.25); }
th { font-weight: 600; opacity: .7; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }
.tag { font-size: 11px; padding: 1px 6px; border-radius: 999px; background: rgba(64,128,255,.15); color: #4a7dff; }
ol { padding-left: 20px; }
ol li { margin: 4px 0; }
.note { background: rgba(128,128,128,.1); border-radius: 8px; padding: 12px 16px; font-size: 13px; }
</style>
</head>
<body>
<h1>豆包 / Dola</h1>
<div class="sub">版本 ` + pluginVersion + ` · 把豆包（国内版）与 Dola（国际版）反代为 OpenAI 兼容接口</div>

<h2>上游</h2>
<table>
<tr><th>标识</th><th>名称</th><th>站点</th><th>aid</th><th>region</th></tr>
` + realmRows.String() + `
</table>

<h2>可用模型</h2>
<table>
<tr><th>模型 ID</th><th>上游</th><th>说明</th></tr>
` + modelRows.String() + `
</table>

<h2>如何授权</h2>
<ol>
<li>在浏览器中登录 <code>www.doubao.com</code>（国内版）或 <code>www.dola.com</code>（国际版）。</li>
<li>按 F12 打开开发者工具，切到 <b>Network</b>，刷新页面。</li>
<li>点击任意一条该站点的请求，在 <b>Request Headers</b> 中找到 <code>Cookie</code>。</li>
<li>复制 Cookie 的完整值，粘贴到 CPA 的授权页面。</li>
</ol>
<div class="note">
必须是<b>登录后</b>的完整 Cookie，其中要包含 <code>flow_cur_user_sec_id</code> 与 <code>sessionid</code>。
缺少前者时网关会认为会话无效，聊天请求会失败。
</div>

<h2>调用方式</h2>
<div class="note">
模型名可加前缀指定上游：<code>doubao/default</code> 或 <code>dola/default</code>；
不加前缀时使用默认上游（当前 <code>` + string(settings.RealmDefault) + `</code>）。
</div>

</body>
</html>`
}

// htmlEscape escapes the few characters that matter inside element content.
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
