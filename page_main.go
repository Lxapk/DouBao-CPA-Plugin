package main

import (
	"fmt"
	"strings"
)

// The panel page.
//
// Every view is always in the document and only one is visible at a time, which is
// what makes tab switching instant and keeps the page working with the host's
// iframe — there is no router and no history to synchronise.

// renderMainPage assembles the whole page.
func renderMainPage() string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	b.WriteString(`<title>豆包 / Dola 控制台</title><style>` + uiCSS + `</style></head><body>`)

	b.WriteString(`<div class="shell">`)
	b.WriteString(renderHeader())
	b.WriteString(renderNav())
	b.WriteString(`<main class="main">`)

	b.WriteString(renderOverviewView())
	b.WriteString(renderAccountsView())
	b.WriteString(renderModelsView())
	b.WriteString(renderSettingsView())

	b.WriteString(`</main></div>`)
	b.WriteString(`<div id="toasts"></div>`)
	b.WriteString(`<script>` + uiScript + `</script>`)
	b.WriteString(`</body></html>`)
	return b.String()
}

// renderHeader is the title block above the tabs.
func renderHeader() string {
	return `<header class="page-header">` +
		`<h1>豆包 / Dola 控制台</h1>` +
		`<p class="desc">把一个插件同时接入豆包（国内版）与 Dola（国际版），` +
		`反代为 CPA 的 OpenAI 兼容接口：文本对话、深度思考、图片生成与视频生成。</p>` +
		`</header>`
}

// renderNav builds the tab strip.
//
// Tabs rather than a side column: this matches the host's own section navigation,
// and the panel is usually viewed in a narrow frame where a fixed column costs
// more than it gives.
func renderNav() string {
	var b strings.Builder
	b.WriteString(`<nav class="tabbar">`)
	for _, item := range []struct{ view, label string }{
		{"view-overview", "总览"},
		{"view-accounts", "账号"},
		{"view-models", "模型"},
		{"view-settings", "设置"},
	} {
		b.WriteString(`<button type="button" class="tab" data-view="` + item.view + `">` +
			item.label + `</button>`)
	}
	b.WriteString(`</nav>`)
	return b.String()
}

// realmLabel renders the realm name with its region tag.
func realmLabel(r realm) string {
	profile := profileFor(r)
	cls := "cn"
	if r == realmDola {
		cls = "intl"
	}
	return htmlEscape(profile.DisplayName) + `<span class="tag ` + cls + `">` + profile.Region + `</span>`
}

// ---------------------------------------------------------------------------
// Overview
// ---------------------------------------------------------------------------

// renderOverviewView shows what the plugin is and how to start.
//
// First-run guidance belongs here rather than in the settings page: a user who has
// just installed the plugin needs the two upstreams explained and the cookie steps
// spelled out before any setting means anything.
func renderOverviewView() string {
	settings := state.settings.get()
	var b strings.Builder
	b.WriteString(`<section class="view" id="view-overview">`)

	b.WriteString(`<div class="stats">`)
	b.WriteString(statCard("", "上游", len(allRealms)))
	b.WriteString(statCard("", "可用模型", len(buildCatalogue())))
	b.WriteString(statCard("", "默认上游", profileFor(settings.RealmDefault).DisplayName))
	b.WriteString(statCard("", "插件版本", pluginVersion))
	b.WriteString(`</div>`)

	// ---- upstreams ----
	b.WriteString(`<div class="group-head"><h2>两个上游</h2>` +
		`<span class="desc">共用一套协议，只有站点与区域不同</span></div>`)
	b.WriteString(`<div class="box"><div class="pad" style="padding:0">`)
	b.WriteString(`<table><thead><tr><th>标识</th><th>名称</th><th>站点</th><th>aid</th><th>区域</th></tr></thead><tbody>`)
	for _, r := range allRealms {
		p := profileFor(r)
		mark := ""
		if r == settings.RealmDefault {
			mark = `<span class="tag">默认</span>`
		}
		b.WriteString(fmt.Sprintf(
			`<tr><td><code>%s</code>%s</td><td>%s</td><td><code>%s</code></td><td><code>%s</code></td><td>%s / %s</td></tr>`,
			r, mark, realmLabel(r), htmlEscape(p.Host), p.AID, p.Region, p.SysRegion,
		))
	}
	b.WriteString(`</tbody></table></div></div>`)

	// ---- how to authorise ----
	b.WriteString(`<div class="group-head"><h2>如何授权</h2>` +
		`<span class="desc">凭据是浏览器 Cookie，没有可用的 OAuth 回调</span></div>`)
	b.WriteString(`<div class="box"><div class="pad">`)
	b.WriteString(`<ol class="steps">`)
	b.WriteString(`<li>在浏览器中登录 <code>www.doubao.com</code>（国内版）或 <code>www.dola.com</code>（国际版）。</li>`)
	b.WriteString(`<li>按 <b>F12</b> 打开开发者工具，切到 <b>Network</b>，刷新页面。</li>`)
	b.WriteString(`<li>点击任意一条该站点的请求，在 <b>Request Headers</b> 中找到 <code>Cookie</code>。</li>`)
	b.WriteString(`<li>复制 Cookie 的完整值（不要带 <code>Cookie:</code> 前缀），粘贴到 CPA 的授权页面。</li>`)
	b.WriteString(`</ol>`)
	b.WriteString(`<div class="note" style="margin-top:12px">必须是<b>登录后</b>的完整 Cookie，` +
		`其中要包含 <code>flow_cur_user_sec_id</code> 与 <code>sessionid</code>。` +
		`缺少前者时网关会认为会话无效。</div>`)
	b.WriteString(`<div class="note" style="margin-top:8px">两个上游的账号彼此独立：` +
		`豆包账号不能用于 Dola，反之亦然（会返回 <code>710022003 CountryRestricted</code>）。</div>`)
	b.WriteString(`</div></div>`)

	b.WriteString(`</section>`)
	return b.String()
}

// ---------------------------------------------------------------------------
// Accounts
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

// renderModelsView lists the catalogue the plugin publishes.
func renderModelsView() string {
	b := &strings.Builder{}
	b.WriteString(`<section class="view" id="view-models" hidden>`)

	b.WriteString(`<div class="group-head"><h2>模型别名</h2>` +
		`<span class="desc">上游的内部代号不稳定，因此对外暴露的是行为别名</span></div>`)
	b.WriteString(`<div class="box"><div class="pad" style="padding:0">`)
	b.WriteString(`<table><thead><tr><th>别名</th><th>说明</th></tr></thead><tbody>`)
	for _, alias := range capabilityAliases {
		b.WriteString(fmt.Sprintf(`<tr><td><code>%s</code></td><td>%s</td></tr>`,
			alias.ID, htmlEscape(alias.Description)))
	}
	b.WriteString(`</tbody></table></div></div>`)

	b.WriteString(`<div class="group-head"><h2>调用时如何指定上游</h2></div>`)
	b.WriteString(`<div class="box"><div class="pad">`)
	b.WriteString(`<p style="margin:0 0 10px">两个上游暴露同名的模型，所以用前缀区分：</p>`)
	b.WriteString(`<div class="box" style="margin:0"><div class="pad" style="padding:0">`)
	b.WriteString(`<table><tbody>`)
	b.WriteString(`<tr><td><code>doubao/default</code></td><td>国内版 · 默认助手</td></tr>`)
	b.WriteString(`<tr><td><code>dola/image</code></td><td>国际版 · 图像生成</td></tr>`)
	b.WriteString(`<tr><td><code>default</code></td><td>不带前缀时使用设置中的默认上游</td></tr>`)
	b.WriteString(`</tbody></table></div></div>`)
	b.WriteString(`</div></div>`)

	b.WriteString(`<div class="group-head"><h2>完整模型列表</h2>` +
		`<span class="desc">与 <code>/v1/models</code> 返回一致</span></div>`)
	b.WriteString(`<div class="box"><div class="pad" style="padding:0">`)
	b.WriteString(`<table><thead><tr><th>模型 ID</th><th>上游</th><th>说明</th></tr></thead><tbody>`)
	for _, m := range buildCatalogue() {
		owner := htmlEscape(m.OwnedBy)
		if owner == "" {
			owner = "—"
		}
		b.WriteString(fmt.Sprintf(`<tr><td><code>%s</code></td><td>%s</td><td>%s</td></tr>`,
			htmlEscape(m.ID), owner, htmlEscape(m.Description)))
	}
	b.WriteString(`</tbody></table></div></div>`)

	b.WriteString(`</section>`)
	return b.String()
}

// statCard appends one cell of the summary strip. tone is "", "good", "warn" or "bad".
func statCard(tone, label string, value any) string {
	cls := "stat"
	if tone != "" {
		cls += " " + tone
	}
	return fmt.Sprintf(`<div class="%s"><div class="label">%s</div><div class="value">%v</div></div>`,
		cls, htmlEscape(label), value)
}
