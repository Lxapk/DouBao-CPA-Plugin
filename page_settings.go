package main

import (
	"html"
	"strings"
)

// The settings page.
//
// Grouped by what the settings affect, not by which field they write: a flat run of
// inputs makes it hard to see which ones are related. The groups are which upstream
// a call goes to, how it is served, how accounts are picked, and diagnostics.

// renderSettingsView builds the settings page.
func renderSettingsView() string {
	settings := state.settings.get()

	var b strings.Builder
	b.WriteString(`<section class="view" id="view-settings" hidden>`)

	// ---- which upstream ----
	b.WriteString(`<div class="group-head"><h2>上游</h2>` +
		`<span class="desc">不带前缀的模型名会用哪一个上游</span></div>`)
	b.WriteString(renderRealmBox(settings))
	b.WriteString(renderExposeBox(settings))

	// ---- account selection ----
	b.WriteString(`<div class="group-head"><h2>路由</h2>` +
		`<span class="desc">请求如何在这些账号之间分配</span></div>`)
	b.WriteString(renderRoutingBox(routingStatusJSON()))

	// ---- panel access ----
	b.WriteString(`<div class="group-head"><h2>面板访问</h2>` +
		`<span class="desc">保存设置需要向 CPA 证明身份</span></div>`)
	b.WriteString(renderKeyBox())

	// ---- runtime ----
	b.WriteString(`<div class="group-head"><h2>运行信息</h2><span class="desc">只读</span></div>`)
	b.WriteString(`<div class="box"><div class="pad">`)
	b.WriteString(`<div class="row" style="gap:26px;flex-wrap:wrap;font-size:13px">`)
	b.WriteString(`<span class="note">版本 <b class="mono">` + pluginVersion + `</b></span>`)
	b.WriteString(`<span class="note">默认上游 <b class="mono">` +
		html.EscapeString(string(settings.RealmDefault)) + `</b></span>`)
	b.WriteString(`<span class="note">数据目录 <b class="mono">` + html.EscapeString(pluginStateDir()) + `</b></span>`)
	b.WriteString(`</div>`)
	b.WriteString(`<div class="row" style="gap:26px;flex-wrap:wrap;font-size:13px;margin-top:10px">`)
	for _, r := range allRealms {
		profile := profileFor(r)
		b.WriteString(`<span class="note">` + realmLabel(r) + ` <b class="mono">` +
			htmlEscape(profile.Host) + `</b></span>`)
	}
	b.WriteString(`</div></div></div>`)

	b.WriteString(`</section>`)
	return b.String()
}

// renderKeyBox collects the CPA management key.
//
// Reading the page needs no key — it is served as a public resource — but writing a
// setting goes through CPA's management API, which does. The key is kept in
// localStorage and attached by the write helper, so it is never handed to the plugin
// and never leaves the browser except as an Authorization header to CPA itself.
func renderKeyBox() string {
	var b strings.Builder
	b.WriteString(`<div class="box">`)
	b.WriteString(`<header><h3>CPA 管理密钥 <span class="hint">仅保存在本机浏览器</span></h3>` +
		`<span class="grow"></span><span class="note" id="keyMsg"></span></header>`)
	b.WriteString(`<div class="pad">`)
	b.WriteString(`<div class="note" style="margin-bottom:10px">` +
		`与 CPA 面板使用同一个密钥；浏览器会把它作为 <code>Authorization</code> 头发给 CPA，` +
		`不经过插件。</div>`)
	b.WriteString(`<div class="row">`)
	b.WriteString(`<input type="password" id="mgmtKey" placeholder="CPA management key" style="flex:1 1 260px">`)
	b.WriteString(`<span class="btn-end">`)
	b.WriteString(`<button type="button" class="ghost" data-call="clearKey">清除</button>`)
	b.WriteString(`<button type="button" class="primary" data-call="saveKey">保存到浏览器</button>`)
	b.WriteString(`</span></div>`)
	b.WriteString(`<div class="note" id="keyState" style="margin-top:9px"></div>`)
	b.WriteString(`</div></div>`)
	return b.String()
}

// renderRealmBox is the domestic / international switch.
//
// This is the setting a user reaches for most, so it leads the page. The effect is
// spelled out underneath in prose rather than left to be inferred from the label:
// the two upstreams hold different accounts, and picking the wrong one looks like a
// broken credential rather than a wrong setting.
func renderRealmBox(settings pluginSettings) string {
	var b strings.Builder
	b.WriteString(`<div class="box">`)
	b.WriteString(`<header><h3>默认上游 <span class="hint">调用与新授权各自归属哪一侧</span></h3>` +
		`<span class="grow"></span><span class="note" id="realmMsg"></span></header>`)
	b.WriteString(`<div class="pad">`)

	b.WriteString(`<div class="seg" id="realmSeg">`)
	for _, opt := range []struct {
		value, label, title string
	}{
		{"doubao", "国内版 · 豆包", "使用 www.doubao.com"},
		{"dola", "国际版 · Dola", "使用 www.dola.com"},
	} {
		cls := ""
		if string(settings.RealmDefault) == opt.value {
			cls = "on"
		}
		b.WriteString(`<button type="button" class="` + cls + `"` +
			` data-value="` + opt.value + `"` +
			` data-call="setRealm" data-arg0="` + opt.value + `"` +
			` title="` + opt.title + `">` + opt.label + `</button>`)
	}
	b.WriteString(`</div>`)

	// The consequence of the current choice, rewritten in place when it changes.
	b.WriteString(`<div class="setting-effect" id="realmEffect"` +
		` data-doubao="当前：不带前缀的模型调用豆包（国内版）。需要豆包账号；用 Dola 账号调用会返回区域不可用。"` +
		` data-dola="当前：不带前缀的模型调用 Dola（国际版）。需要 Dola 账号；用豆包账号调用会返回区域不可用。">` +
		realmEffectText(settings.RealmDefault) + `</div>`)
	b.WriteString(`<div class="note" style="margin-top:8px">切换会同时停用另一侧的账号：` +
		`两侧账号不能互相调用，留着只会让请求落到必然失败的那一个上。</div>`)
	b.WriteString(`</div></div>`)
	return b.String()
}

func realmEffectText(r realm) string {
	if r == realmDola {
		return "当前：不带前缀的模型调用 Dola（国际版）。需要 Dola 账号；用豆包账号调用会返回区域不可用。"
	}
	return "当前：不带前缀的模型调用豆包（国内版）。需要豆包账号；用 Dola 账号调用会返回区域不可用。"
}

// renderExposeBox controls whether both upstreams' models are advertised.
func renderExposeBox(settings pluginSettings) string {
	var b strings.Builder
	b.WriteString(`<div class="box">`)
	b.WriteString(`<header><h3>模型可见范围</h3><span class="grow"></span>` +
		`<span class="note" id="exposeMsg"></span></header>`)
	b.WriteString(`<div class="pad">`)
	b.WriteString(`<div class="seg" id="exposeSeg">`)
	on := settings.exposesModels()
	for _, opt := range []struct {
		value, label string
		state        bool
	}{
		{"on", "两者都列出", true},
		{"off", "仅默认上游", false},
	} {
		cls := ""
		if opt.state == on {
			cls = "on"
		}
		b.WriteString(`<button type="button" class="` + cls + `"` +
			` data-value="` + opt.value + `"` +
			` data-call="setExpose" data-arg0="` + opt.value + `">` + opt.label + `</button>`)
	}
	b.WriteString(`</div>`)
	b.WriteString(`<div class="setting-effect">关闭后 <code>/v1/models</code> 只列出默认上游的模型，` +
		`另一个上游的账号即使已授权也不会出现在选择列表里。</div>`)
	b.WriteString(`</div></div>`)
	return b.String()
}

// renderRoutingBox draws the request-distribution strategy as a segmented control
// with one line of explanation that swaps on selection.
func renderRoutingBox(routing map[string]any) string {
	var b strings.Builder
	b.WriteString(`<div class="box">`)
	b.WriteString(`<header><h3>路由策略 <span class="hint">请求如何在这些账号之间分配</span></h3>`)
	b.WriteString(`<span class="grow"></span><span class="note" id="strategyMsg"></span>`)
	b.WriteString(`</header><div class="pad">`)

	options, _ := routing["options"].([]map[string]any)
	current, _ := routing["strategy"].(string)

	b.WriteString(`<div class="seg" id="strategySeg">`)
	effect := ""
	var attrs strings.Builder
	for _, opt := range options {
		value, _ := opt["value"].(string)
		label, _ := opt["label"].(string)
		desc, _ := opt["description"].(string)
		tradeoff, _ := opt["tradeoff"].(string)
		line := strings.TrimSpace(desc + " " + tradeoff)
		if value == current {
			effect = line
		}
		attrs.WriteString(` data-` + html.EscapeString(value) + `="` + html.EscapeString(line) + `"`)

		cls := ""
		if value == current {
			cls = "on"
		}
		b.WriteString(`<button type="button" class="` + cls + `"` +
			` data-value="` + html.EscapeString(value) + `"` +
			` data-call="pickStrategy" data-arg0="` + html.EscapeString(value) + `"` +
			` title="` + html.EscapeString(desc) + `">` + html.EscapeString(label) + `</button>`)
	}
	b.WriteString(`</div>`)
	b.WriteString(`<div class="setting-effect" id="strategySegEffect"` + attrs.String() + `>` +
		html.EscapeString(effect) + `</div>`)

	b.WriteString(`<div class="row" style="margin-top:14px">`)
	b.WriteString(`<span class="note">下一个请求：<b id="strategyNext">` +
		html.EscapeString(routingNextHint()) + `</b></span>`)
	b.WriteString(`<span class="grow"></span>`)
	b.WriteString(`<button type="button" class="xs" data-call="resetRotation">重置轮巡位置</button>`)
	b.WriteString(`</div>`)

	b.WriteString(`</div></div>`)
	return b.String()
}
