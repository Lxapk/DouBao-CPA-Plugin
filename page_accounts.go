package main

import (
	"fmt"
	"strings"
)

// The accounts view: the list, per-account enable/disable, and the authorisation
// guidance.
//
// This is the page the whole realm switch exists for. The two upstreams hold
// separate accounts and reject each other's sessions, so seeing which accounts exist,
// which realm each belongs to, and which are currently routable is the difference
// between a switch that works and a support question.

// renderAccountsView builds the accounts page.
func renderAccountsView() string {
	var b strings.Builder
	b.WriteString(`<section class="view" id="view-accounts" hidden>`)

	b.WriteString(`<div class="group-head"><h2>已授权账号</h2>` +
		`<span class="desc">切换上游时会自动禁用另一侧的账号</span></div>`)
	b.WriteString(renderAccountsBox())

	b.WriteString(`<div class="group-head"><h2>新增授权</h2>` +
		`<span class="desc">豆包没有 OAuth 回调，凭据是浏览器 Cookie</span></div>`)
	b.WriteString(renderAuthorisationBox())

	b.WriteString(`</section>`)
	return b.String()
}

// renderAccountsBox renders the account table.
//
// The table is filled from /accounts on load and replaced wholesale after every
// action, so the page never shows a state the backend disagrees with.
func renderAccountsBox() string {
	var b strings.Builder
	b.WriteString(`<div class="box">`)
	b.WriteString(`<header><h3>账号列表</h3><span class="grow"></span>`)
	b.WriteString(`<span class="note" id="acctMsg"></span>`)
	b.WriteString(`<button type="button" class="ghost" data-call="reloadAccounts">刷新</button>`)
	b.WriteString(`</header>`)
	b.WriteString(`<div class="pad" style="padding:0"><div id="acctWrap">`)
	b.WriteString(`<div class="empty">正在读取…</div>`)
	b.WriteString(`</div></div>`)
	b.WriteString(`</div>`)

	// Two bulk actions that map onto the switch's own semantics, for when the list
	// needs clearing before a realm change rather than by it.
	b.WriteString(`<div class="box"><div class="pad">`)
	b.WriteString(`<div class="row">`)
	b.WriteString(`<span class="note">批量操作只影响当前列出的账号，不会改动其他插件的凭据。</span>`)
	b.WriteString(`<span class="btn-end">`)
	b.WriteString(`<button type="button" class="ghost" data-call="bulkAccounts" data-arg0="enable">全部启用</button>`)
	b.WriteString(`<button type="button" class="ghost" data-call="bulkAccounts" data-arg0="disable">全部禁用</button>`)
	b.WriteString(`</span></div>`)
	b.WriteString(`</div></div>`)
	return b.String()
}

// renderAuthorisationBox is the authorisation form.
//
// This is the plugin's own path, and the only one a user is pointed at. CPA's
// management UI offers an OAuth-shaped box that demands a callback URL and rejects a
// pasted cookie with "state is required" — a message that gives no hint that the
// expected input is a URL, let alone a URL-encoded one. Doing it here removes the
// encoding exercise entirely: the cookie goes straight into a field, the plugin checks
// it against the live upstream, and the result is reported line by line.
func renderAuthorisationBox() string {
	settings := state.settings.get()
	var b strings.Builder

	b.WriteString(`<div class="box">`)
	b.WriteString(`<header><h3>在面板里授权</h3>` +
		`<span class="hint">不需要经过 CPA 的 OAuth 回调</span></header>`)
	b.WriteString(`<div class="pad">`)

	// ---- which upstream this credential belongs to ----
	b.WriteString(`<div class="setting-group" style="padding:0 0 14px">`)
	b.WriteString(`<div class="setting-label">`)
	b.WriteString(`<span class="name">授权归属</span>`)
	b.WriteString(`<span class="desc">决定用哪个站点校验这份 Cookie。选错会提示区域不可用。</span>`)
	b.WriteString(`</div>`)
	b.WriteString(`<div class="seg" id="authRealmSeg">`)
	for _, r := range allRealms {
		cls := ""
		if r == settings.RealmDefault {
			cls = "on"
		}
		b.WriteString(`<button type="button" class="` + cls + `"` +
			` data-value="` + string(r) + `"` +
			` data-call="pickAuthRealm" data-arg0="` + string(r) + `">` +
			realmLabel(r) + `</button>`)
	}
	b.WriteString(`</div></div>`)

	// ---- the paste target ----
	b.WriteString(`<div class="setting-group" style="padding:0 0 12px">`)
	b.WriteString(`<div class="setting-label">`)
	b.WriteString(`<span class="name">Cookie</span>`)
	b.WriteString(`<span class="desc">从浏览器开发者工具里的 <code>Cookie</code> 请求头复制整串值，` +
		`直接粘贴即可 —— 不需要做任何转义。</span>`)
	b.WriteString(`</div>`)
	b.WriteString(`<textarea id="authCookies" rows="5" spellcheck="false"` +
		` placeholder="sessionid=...; flow_cur_user_sec_id=...; ..."` +
		` style="width:100%;background:var(--bg-secondary);border:1px solid var(--border-primary);` +
		`border-radius:var(--radius-md);color:var(--text-primary);font-family:var(--mono);` +
		`font-size:12px;padding:10px;outline:none;resize:vertical"></textarea>`)
	b.WriteString(`<div class="row" style="margin-top:9px">`)
	b.WriteString(`<input type="text" id="authLabel" placeholder="备注名（可选）" style="flex:1 1 200px">`)
	b.WriteString(`<span class="btn-end">`)
	b.WriteString(`<button type="button" class="ghost" data-call="clearAuthForm">清空</button>`)
	b.WriteString(`<button type="button" class="primary" id="authSubmit" data-call="submitAuthorize">校验并授权</button>`)
	b.WriteString(`</span></div>`)
	b.WriteString(`<div id="authResult" style="margin-top:10px"></div>`)
	b.WriteString(`</div>`)

	// ---- how to get the value ----
	b.WriteString(`<div class="setting-group" style="padding:0">`)
	b.WriteString(`<div class="setting-label"><span class="name">在哪里找到它</span></div>`)
	for i, r := range allRealms {
		profile := profileFor(r)
		if i > 0 {
			b.WriteString(`<div style="height:12px"></div>`)
		}
		b.WriteString(`<div class="row" style="margin-bottom:6px">`)
		b.WriteString(`<a class="ghost" style="text-decoration:none;padding:5px 12px;border:1px solid var(--border-primary);` +
			`border-radius:var(--radius-md);color:var(--text-secondary);font-size:13px"` +
			` href="` + htmlEscape(profile.Host+"/chat/") + `" target="_blank" rel="noopener">` +
			realmLabel(r) + ` 登录页 ↗</a>`)
		b.WriteString(`<span class="note">登录后 F12 → Network → 刷新 → 点击任意 ` +
			htmlEscape(strings.TrimPrefix(profile.CookieDomain, ".")) + ` 请求 → 复制 <code>Cookie</code></span>`)
		b.WriteString(`</div>`)
	}
	b.WriteString(`<div class="note" style="margin-top:8px">` +
		`必须包含 <code>sessionid</code> 与 <code>flow_cur_user_sec_id</code>。` +
		`缺少后者会被判定为会话无效 —— 插件会在校验时直接告诉你。</div>`)
	b.WriteString(`</div>`)

	b.WriteString(`</div></div>`)
	return b.String()
}

// accountsTable renders the account list.
//
// It is a separate function because the client re-renders the same fragment after a
// toggle; returning HTML rather than having the script rebuild it keeps the markup in
// one place.
func accountsTable(entries []hostAuthEntry) string {
	if len(entries) == 0 {
		return `<div class="empty">还没有账号。展开下面的「新增授权」按步骤添加。</div>`
	}

	var b strings.Builder
	b.WriteString(`<table><thead><tr>`)
	b.WriteString(`<th>账号</th><th>上游</th><th>状态</th><th>操作</th>`)
	b.WriteString(`</tr></thead><tbody>`)

	for _, e := range entries {
		b.WriteString(`<tr>`)
		b.WriteString(`<td>` + htmlEscape(e.displayLabel()) + `</td>`)
		b.WriteString(`<td>` + realmLabel(e.Realm) + `</td>`)

		b.WriteString(`<td>`)
		if e.Disabled {
			b.WriteString(`<span class="tag">已禁用</span>`)
		} else {
			b.WriteString(`<span class="tag intl">启用中</span>`)
		}
		if e.StatusMessage != "" {
			b.WriteString(`<div class="note" style="margin-top:2px">` + htmlEscape(e.StatusMessage) + `</div>`)
		}
		b.WriteString(`</td>`)

		b.WriteString(`<td>`)
		label := "禁用"
		if e.Disabled {
			label = "启用"
		}
		b.WriteString(`<button type="button" class="ghost"` +
			` data-call="toggleAccount" data-arg0="` + htmlEscape(e.AuthIndex) + `"` +
			` data-arg1="` + htmlEscape(e.Path) + `"` +
			` data-arg2="` + fmt.Sprintf("%t", !e.Disabled) + `">` + label + `</button>`)
		b.WriteString(`</td>`)
		b.WriteString(`</tr>`)

		// The auth index and file path are what the toggle sends back, so showing
		// them makes a mis-toggle diagnosable from a screenshot.
		if e.Path != "" {
			b.WriteString(`<tr><td colspan="4" class="note" style="padding-top:0;border-top:0">` +
				`<code>` + htmlEscape(e.Path) + `</code></td></tr>`)
		}
	}

	b.WriteString(`</tbody></table>`)
	return b.String()
}

// displayLabel is the account name shown in the list.
func (e hostAuthEntry) displayLabel() string {
	for _, candidate := range []string{e.Label, e.SecUserID, e.Name, e.ID} {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return "未命名账号"
}
