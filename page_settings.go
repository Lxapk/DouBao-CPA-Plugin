package main

import (
	"fmt"
	"strings"
)

// The settings view.
//
// Grouped by what the settings affect, not by which field they write: a flat run of
// inputs makes it hard to see which ones are related. The three groups are how a
// call is served, which upstream it goes to, and how the plugin reports what it did.

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

	// ---- how a call is served ----
	b.WriteString(`<div class="group-head"><h2>调用</h2>` +
		`<span class="desc">超时与等待预算，决定一次请求愿意等多久</span></div>`)
	b.WriteString(renderCallBox(settings))

	// ---- diagnostics ----
	b.WriteString(`<div class="group-head"><h2>诊断</h2>` +
		`<span class="desc">排查问题时打开</span></div>`)
	b.WriteString(renderDebugBox(settings))

	// ---- panel access ----
	b.WriteString(`<div class="group-head"><h2>面板访问</h2>` +
		`<span class="desc">保存设置需要向 CPA 证明身份</span></div>`)
	b.WriteString(renderKeyBox())

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
	b.WriteString(`<div class="setting-group">`)
	b.WriteString(`<div class="setting-label">` +
		`<span class="name">用于保存设置</span>` +
		`<span class="desc">读取页面与模型列表不需要密钥；保存设置时浏览器会把它作为 ` +
		`<code>Authorization</code> 头发给 CPA。与 CPA 面板使用的是同一个密钥。</span>` +
		`</div>`)
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

	b.WriteString(`<div class="setting-group">`)
	b.WriteString(`<div class="setting-label">`)
	b.WriteString(`<span class="name">不带前缀的模型名走哪个上游</span>`)
	b.WriteString(`<span class="desc">带 <code>doubao/</code> 或 <code>dola/</code> 前缀的模型不受影响。</span>`)
	b.WriteString(`</div>`)

	b.WriteString(`<div class="setting-control"><div class="seg" id="realmSeg">`)
	for _, opt := range []struct {
		value, label, title string
	}{
		{"doubao", "国内版 · 豆包", "使用 www.doubao.com"},
		{"dola", "国际版 · Dola", "使用 www.dola.com"},
	} {
		on := ""
		if string(settings.RealmDefault) == opt.value {
			on = " on"
		}
		b.WriteString(`<button type="button" class="` + strings.TrimSpace(on) + `"` +
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
	b.WriteString(`<div class="setting-group">`)
	b.WriteString(`<div class="setting-label">`)
	b.WriteString(`<span class="name">是否同时暴露两个上游的模型</span>`)
	b.WriteString(`<span class="desc">关闭后 <code>/v1/models</code> 只列出默认上游的模型，` +
		`另一个上游的账号即使已授权也不会出现在选择列表里。</span>`)
	b.WriteString(`</div>`)
	b.WriteString(`<div class="setting-control"><div class="seg" id="exposeSeg">`)
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
	b.WriteString(`</div></div></div></div>`)
	return b.String()
}

// renderCallBox holds the timing settings.
func renderCallBox(settings pluginSettings) string {
	var b strings.Builder
	b.WriteString(`<div class="box">`)
	b.WriteString(`<header><h3>超时与轮询</h3><span class="grow"></span>` +
		`<span class="note" id="callMsg"></span></header>`)

	for _, row := range []struct {
		id, name, label, desc string
		value                 int
	}{
		{
			"setTimeout", "request_timeout_seconds",
			"单次上游请求超时（秒）",
			"单个 HTTP 请求的最长耗时。流式请求不受此限制，由下面的预算决定。",
			settings.RequestTimeoutSeconds,
		},
		{
			"setReply", "reply_poll_seconds",
			"等待文本回复（秒）",
			"文本对话愿意等待的最长时间。太短会在模型还在生成时放弃。",
			settings.ReplyPollSeconds,
		},
		{
			"setMedia", "media_poll_seconds",
			"等待图片 / 视频（秒）",
			"生成图片或视频愿意等待的最长时间。生成远比文本慢，默认给到 10 分钟。",
			settings.MediaPollSeconds,
		},
		{
			"setIntervalMs", "reply_poll_interval_ms",
			"轮询间隔（毫秒）",
			"检查流式应答的频率。调小响应更及时，代价是更频繁的请求。",
			settings.ReplyPollIntervalMS,
		},
	} {
		b.WriteString(`<div class="setting-group">`)
		b.WriteString(`<div class="setting-label">`)
		b.WriteString(`<span class="name">` + row.label + `</span>`)
		b.WriteString(`<span class="desc">` + row.desc + `</span>`)
		b.WriteString(`</div>`)
		b.WriteString(`<div class="row">`)
		b.WriteString(fmt.Sprintf(`<input type="number" id="%s" value="%d" min="1">`, row.id, row.value))
		b.WriteString(`<button type="button" class="primary"` +
			` data-call="saveNum" data-arg0="` + row.id + `" data-arg1="` + row.name + `"` +
			`>保存</button>`)
		b.WriteString(`</div></div>`)
	}

	b.WriteString(`</div>`)
	return b.String()
}

// renderDebugBox holds the diagnostics toggle.
func renderDebugBox(settings pluginSettings) string {
	var b strings.Builder
	b.WriteString(`<div class="box">`)
	b.WriteString(`<header><h3>详细日志</h3><span class="grow"></span>` +
		`<span class="note" id="debugMsg"></span></header>`)
	b.WriteString(`<div class="setting-group">`)
	b.WriteString(`<div class="setting-label">`)
	b.WriteString(`<span class="name">记录上游请求与响应摘要</span>`)
	b.WriteString(`<span class="desc">打开后插件会把每次调用的方法与响应摘要写进 CPA 日志。` +
		`摘要包含 Cookie 之外的所有请求头，排查协议问题时很有用，平时建议关闭。</span>`)
	b.WriteString(`</div>`)
	b.WriteString(`<div class="setting-control"><div class="seg" id="debugSeg">`)
	for _, opt := range []struct {
		value, label string
		state        bool
	}{
		{"on", "打开", true},
		{"off", "关闭", false},
	} {
		cls := ""
		if opt.state == settings.Debug {
			cls = "on"
		}
		b.WriteString(`<button type="button" class="` + cls + `"` +
			` data-value="` + opt.value + `"` +
			` data-call="setDebug" data-arg0="` + opt.value + `">` + opt.label + `</button>`)
	}
	b.WriteString(`</div></div></div></div>`)
	return b.String()
}
