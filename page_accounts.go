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

// renderAuthorisationBox is the authorisation guidance.
//
// The upstream has no device-code flow and no loopback redirect — its passport
// endpoints answer "该应用无权限" to a web client — so the credential is the browser's
// own cookie jar. Pretending otherwise would produce a login button that cannot work.
// What the page can do is make the manual step unambiguous: a real link to the sign-in
// page for each upstream, and the exact path through DevTools to the value to copy.
func renderAuthorisationBox() string {
	var b strings.Builder
	b.WriteString(`<div class="box">`)

	for i, r := range allRealms {
		profile := profileFor(r)
		if i > 0 {
			b.WriteString(`<div style="border-top:1px solid var(--border-color)"></div>`)
		}

		b.WriteString(`<header><h3>` + realmLabel(r) + ` 授权</h3></header>`)
		b.WriteString(`<div class="pad">`)

		// A real link, not a bare URL in prose: the user is meant to click it.
		loginURL := profile.Host + "/chat/"
		b.WriteString(`<div class="row" style="margin-bottom:12px">`)
		b.WriteString(`<a class="ghost" style="text-decoration:none;padding:6px 14px;border:1px solid var(--border-primary);border-radius:var(--radius-md);color:var(--text-secondary)"` +
			` href="` + htmlEscape(loginURL) + `" target="_blank" rel="noopener">` +
			`打开 ` + htmlEscape(profile.Host) + ` 登录 ↗</a>`)
		b.WriteString(`<span class="note">先在浏览器里完成登录，再复制 Cookie</span>`)
		b.WriteString(`</div>`)

		b.WriteString(`<ol class="steps">`)
		b.WriteString(`<li>点击上面的链接，在<b>同一个浏览器</b>里登录 ` + htmlEscape(profile.DisplayName) + `。</li>`)
		b.WriteString(`<li>登录成功后按 <b>F12</b> 打开开发者工具，切到 <b>Network</b> 面板。</li>`)
		b.WriteString(`<li>刷新页面，点击任意一条 ` + htmlEscape(strings.TrimPrefix(profile.CookieDomain, ".")) +
			` 域名的请求。</li>`)
		b.WriteString(`<li>在 <b>Request Headers</b> 里找到 <code>Cookie</code>，右键 → <b>Copy value</b>。</li>`)
		b.WriteString(`<li>回到 CPA 的账号页，新增 <b>豆包 / Dola</b> 账号，把整串粘贴进去。</li>`)
		b.WriteString(`</ol>`)

		b.WriteString(`<div class="setting-effect" style="margin-top:12px">`)
		b.WriteString(`必须是<b>登录后</b>的完整 Cookie，其中要包含 <code>flow_cur_user_sec_id</code> 与 ` +
			`<code>sessionid</code>。只复制其中几个字段会失败——网关绑定的是整套会话。`)
		b.WriteString(`</div>`)

		b.WriteString(`<div class="note" style="margin-top:8px">`)
		b.WriteString(`该账号会归到 <b>` + htmlEscape(profile.DisplayName) + `</b>（` + profile.Region + `）。`)
		if r == realmDoubao {
			b.WriteString(`两个上游的账号不能互换：豆包账号调用 Dola 会返回 <code>710022003 CountryRestricted</code>。`)
		} else {
			b.WriteString(`两个上游的账号不能互换：Dola 账号调用豆包会返回 <code>710022003 CountryRestricted</code>。`)
		}
		b.WriteString(`</div>`)
		b.WriteString(`</div>`)
	}

	b.WriteString(`</div>`)

	// Why there is no one-click login, stated plainly so it does not read as an
	// omission.
	b.WriteString(`<div class="box"><div class="pad">`)
	b.WriteString(`<div class="setting-label"><span class="name">为什么没有一键登录</span>`)
	b.WriteString(`<span class="desc">豆包未开放网页端 OAuth：其 passport 接口对浏览器来源返回 ` +
		`「该应用无权限」，只能走 App 端。因此凭据取浏览器自己的 Cookie。</span></div>`)
	b.WriteString(`<div class="note">好在 Cookie 的获取方式是固定的，照上面的步骤操作即可。` +
		`失效后重新复制一次就能恢复，不需要重启 CPA。</div>`)
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
