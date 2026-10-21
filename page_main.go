package main

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// The panel's page frame.
//
// Structure mirrors the Tinker plugin: a title block, a tab strip, then one page
// per functional area. Every card carries its own title and its own actions in
// the header, so a button sits next to what it acts on rather than in a toolbar
// at the top of the page.
//
// Each page is its own function so the shape of the panel is readable from the
// list of names.

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
	// Every page is in the document; only one is visible at a time.
	b.WriteString(renderAccountsView())
	b.WriteString(renderUsageView())
	b.WriteString(renderGuideView())
	b.WriteString(renderSettingsView())
	b.WriteString(`</main></div>`)

	b.WriteString(`<div id="toasts"></div>`)
	b.WriteString(panelScript())
	b.WriteString(`</body></html>`)
	return b.String()
}

func renderHeader() string {
	var b strings.Builder
	b.WriteString(`<header class="page-header">`)
	b.WriteString(`<h1>豆包 / Dola 控制台</h1>`)
	b.WriteString(`<p class="desc">把一个插件同时接入豆包（国内版）与 Dola（国际版），` +
		`反代为 CPA 的 OpenAI 兼容接口：文本对话、深度思考、图片生成与视频生成，` +
		`支持多账号轮换与调用统计。</p>`)
	b.WriteString(`</header>`)
	return b.String()
}

// renderNav builds the tab strip.
//
// Tabs rather than a side column: this matches the host's own section navigation,
// and the panel is usually viewed in a narrow frame where a fixed column costs
// more than it gives.
func renderNav() string {
	var b strings.Builder
	b.WriteString(`<nav class="tabbar">`)
	for i, item := range []struct{ view, label string }{
		{"view-accounts", "账号"},
		{"view-usage", "统计"},
		{"view-guide", "说明"},
		{"view-settings", "设置"},
	} {
		cls := "tab"
		if i == 0 {
			cls += " on"
		}
		b.WriteString(`<button type="button" class="` + cls + `" data-view="` + item.view + `">` +
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
	return `<span class="realm">` + htmlEscape(profile.DisplayName) +
		`<span class="tag ` + cls + `">` + profile.Region + `</span></span>`
}

// statCard appends one cell of the summary strip. tone is "", "good", "warn" or "bad".
func statCard(b *strings.Builder, tone, label string, value any) {
	cls := "stat"
	if tone != "" {
		cls += " " + tone
	}
	b.WriteString(`<div class="` + cls + `"><div class="v">` + html.EscapeString(fmt.Sprint(value)) +
		`</div><div class="k">` + html.EscapeString(label) + `</div></div>`)
}

// humanTime renders a Unix timestamp for the tables, with a dash for "never".
func humanTime(sec int64) string {
	if sec <= 0 {
		return "—"
	}
	return time.Unix(sec, 0).Format("01-02 15:04")
}
