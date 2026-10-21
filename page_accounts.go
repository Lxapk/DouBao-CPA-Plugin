package main

import (
	"html"
	"strings"
)

// The accounts page: the pool, per-account enable/disable, and the authorisation
// form.
//
// This is the page the whole realm switch exists for. The two upstreams hold
// separate accounts and reject each other's sessions, so seeing which accounts
// exist, which realm each belongs to, and which are currently routable is the
// difference between a switch that works and a support question.

// renderAccountsView builds the accounts page.
func renderAccountsView() string {
	entries := accounts.get()
	total := len(entries)
	enabled, blocked := 0, 0
	for _, e := range entries {
		if e.Disabled || e.AuthRejected {
			blocked++
			continue
		}
		enabled++
	}

	var b strings.Builder
	b.WriteString(`<section class="view" id="view-accounts">`)

	b.WriteString(`<div class="stats">`)
	statCard(&b, "", "账号总数", total)
	statCard(&b, "good", "启用中", enabled)
	statCard(&b, "warn", "国内版", countRealm(entries, realmDoubao))
	statCard(&b, "bad", "已停用", blocked)
	b.WriteString(`</div>`)

	b.WriteString(`<div class="box">`)
	b.WriteString(`<header><h3>账号 <span class="hint">授权后自动出现</span></h3>`)
	b.WriteString(`<span class="grow"></span><span class="note" id="accountMsg"></span>`)
	b.WriteString(`<button type="button" class="xs" data-call="refreshAccounts">刷新</button>`)
	b.WriteString(`</header>`)

	// The table is filled from /accounts on load and replaced after every action,
	// so a row never shows a state the backend disagrees with.
	b.WriteString(`<div id="acctWrap">`)
	if total == 0 {
		b.WriteString(`<div class="empty">还没有账号。展开下面的「添加账号」，用手机号登录。</div>`)
	} else {
		b.WriteString(`<div class="tbl-wrap"><table><thead><tr>`)
		b.WriteString(`<th>账号</th><th class="state">上游</th><th class="state">状态</th>`)
		b.WriteString(`<th class="num">调用</th><th class="num">最近使用</th><th class="act"></th>`)
		b.WriteString(`</tr></thead><tbody>`)
		for _, e := range entries {
			b.WriteString(renderAccountRow(e))
		}
		b.WriteString(`</tbody></table></div>`)
	}
	b.WriteString(`</div>`)

	b.WriteString(`<div class="foot"><span class="note">` +
		`启停会写入 CPA 的 auth 文件，与该账号在 CPA 面板里的状态保持一致。</span>` +
		`<span class="grow"></span>` +
		`<button type="button" class="xs" data-call="bulkAccounts" data-arg0="enable">全部启用</button>` +
		`<button type="button" class="xs" data-call="bulkAccounts" data-arg0="disable">全部停用</button>` +
		`</div>`)
	b.WriteString(`</div>`)

	b.WriteString(`<div class="group-head"><h2>添加账号</h2>` +
		`<span class="desc">手机号 + 短信验证码，不需要开浏览器翻 Cookie</span></div>`)
	b.WriteString(renderAuthorisationBox())

	b.WriteString(`</section>`)
	return b.String()
}

// countRealm counts the accounts of one upstream.
func countRealm(entries []hostAuthEntry, r realm) int {
	n := 0
	for _, e := range entries {
		if e.Realm == r {
			n++
		}
	}
	return n
}

// renderAccountRow draws one account.
//
// The counter columns are read from the plugin's own store: the host does not
// track how often a credential served a request, and a re-authorisation keeps the
// same handle so the numbers survive it.
func renderAccountRow(e hostAuthEntry) string {
	name := e.displayLabel()

	status := `<span class="pill on">启用</span>`
	switch {
	case e.Disabled:
		status = `<span class="pill off">已停用</span>`
	case e.AuthRejected:
		status = `<span class="pill off">需重新授权</span>`
	}

	record := stats.lookup(e.SecUserID)
	if record == nil {
		record = stats.lookup(name)
	}
	calls := int64(0)
	lastUsed := "—"
	if record != nil {
		calls = record.CallCount
		lastUsed = humanTime(record.LastUsedUnix)
	}

	action := `<button type="button" class="xs danger" data-call="toggleAccount"` +
		` data-arg0="` + html.EscapeString(e.AuthIndex) + `"` +
		` data-arg1="` + html.EscapeString(e.Path) + `"` +
		` data-arg2="true">停用</button>`
	if e.Disabled {
		action = `<button type="button" class="xs" data-call="toggleAccount"` +
			` data-arg0="` + html.EscapeString(e.AuthIndex) + `"` +
			` data-arg1="` + html.EscapeString(e.Path) + `"` +
			` data-arg2="false">启用</button>`
	}

	detail := ""
	if record != nil && record.LastError != "" && !e.Disabled {
		detail = `<div class="note bad" style="margin-top:3px">` + html.EscapeString(record.LastError) + `</div>`
	}

	// The counter row is the only thing this plugin owns for an account, so the
	// removal action is offered only when there is one.
	remove := ""
	if record != nil {
		remove = `<button type="button" class="xs ghost" data-call="removeAccount"` +
			` data-arg0="` + html.EscapeString(firstNonEmpty(e.SecUserID, name)) + `"` +
			` title="只清除本地统计，不会删除 CPA 的凭据文件">删除记录</button>`
	}

	return `<tr>` +
		`<td><div class="acct-name"><strong title="` + html.EscapeString(name) + `">` +
		html.EscapeString(name) + `</strong>` + detail + `</div></td>` +
		`<td class="state">` + realmLabel(e.Realm) + `</td>` +
		`<td class="state">` + status + `</td>` +
		`<td class="num">` + fmtInt(calls) + `</td>` +
		`<td class="note num">` + lastUsed + `</td>` +
		`<td class="act">` + action + remove + `</td>` +
		`</tr>`
}

// renderAuthorisationBox is the add-account form.
//
// 主路径是手机号 + 短信验证码：点「添加账号」会打开插件的登录页，那里输入手机号
// 和验证码即可，不需要开开发者工具、也不需要复制 Cookie。这与 tinker 插件的
// 体验一致。
//
// 粘贴 Cookie 的入口保留在下面，作为备用：用别处导出的会话、或者登录接口被上游
// 改动时，仍然可以手工落库。
func renderAuthorisationBox() string {
	settings := state.settings.get()
	var b strings.Builder

	b.WriteString(`<div class="box">`)
	b.WriteString(`<header><h3>添加账号 <span class="hint">手机号 + 短信验证码</span></h3>`)
	b.WriteString(`<span class="grow"></span><span class="note" id="authMsg"></span>`)
	b.WriteString(`</header>`)
	b.WriteString(`<div class="pad">`)

	b.WriteString(`<div class="note" style="margin-bottom:10px">` +
		`选好上游后点「打开登录页」，在弹出的页面里填手机号并接收验证码。` +
		`登录成功后回到这里刷新，账号会出现在上面的列表里。</div>`)

	// ---- which upstream this credential belongs to ----
	b.WriteString(`<div class="row" style="align-items:flex-start;gap:18px">`)
	b.WriteString(`<div style="flex:1 1 240px;min-width:0">`)
	b.WriteString(`<div class="note" style="margin-bottom:7px">登录归属</div>`)
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
	b.WriteString(`</div>`)
	b.WriteString(`<div class="note" style="margin-top:7px">决定用哪个站点登录。选错会提示区域不可用。</div>`)
	b.WriteString(`</div>`)
	b.WriteString(`<div style="flex:1 1 240px;min-width:0">`)
	b.WriteString(`<div class="note" style="margin-bottom:7px">&nbsp;</div>`)
	b.WriteString(`<button type="button" class="primary" data-call="openLogin" style="width:100%">打开登录页</button>`)
	b.WriteString(`</div>`)
	b.WriteString(`</div>`)

	// ---- fallback: paste a cookie ----
	b.WriteString(`<div class="group-head" style="margin:20px 0 8px"><h2>或者粘贴 Cookie</h2>` +
		`<span class="desc">备用方式：从浏览器复制登录后的 Cookie</span></div>`)
	b.WriteString(`<textarea id="authCookies" rows="4" spellcheck="false"` +
		` placeholder="sessionid=...; flow_cur_user_sec_id=...; ..."` +
		` style="width:100%;background:var(--bg-quinary);border:1px solid var(--border-primary);` +
		`border-radius:var(--radius-md);color:var(--text-primary);font-family:var(--mono);` +
		`font-size:12px;padding:10px;outline:none;resize:vertical"></textarea>`)
	b.WriteString(`<div class="row" style="margin-top:10px">`)
	b.WriteString(`<input type="text" id="authLabel" placeholder="备注名（可选）" style="flex:1 1 200px">`)
	b.WriteString(`<span class="grow"></span>`)
	b.WriteString(`<button type="button" class="ghost" data-call="clearAuthForm">清空</button>`)
	b.WriteString(`<button type="button" class="primary" id="authSubmit" data-call="submitAuthorize">校验并授权</button>`)
	b.WriteString(`</div>`)
	b.WriteString(`<div id="authResult" style="margin-top:10px"></div>`)

	// ---- how to get the value ----
	for i, r := range allRealms {
		profile := profileFor(r)
		if i > 0 {
			b.WriteString(`<div style="height:10px"></div>`)
		}
		b.WriteString(`<div class="row" style="margin-top:10px">`)
		b.WriteString(`<a class="ghost" style="text-decoration:none;padding:5px 12px;border:1px solid var(--border-primary);` +
			`border-radius:var(--radius-md);color:var(--text-secondary);font-size:12.5px"` +
			` href="` + htmlEscape(profile.Host+"/chat/") + `" target="_blank" rel="noopener">` +
			realmLabel(r) + ` 网页版 ↗</a>`)
		b.WriteString(`<span class="note">登录后 F12 → Network → 刷新 → 点击任意 ` +
			htmlEscape(strings.TrimPrefix(profile.CookieDomain, ".")) + ` 请求 → 复制 <code>Cookie</code></span>`)
		b.WriteString(`</div>`)
	}
	b.WriteString(`<div class="note" style="margin-top:8px">` +
		`Cookie 需要包含 <code>sessionid</code> 与 <code>flow_cur_user_sec_id</code>。` +
		`缺少后者会被判定为会话无效 —— 插件会在校验时直接告诉你。</div>`)

	b.WriteString(`</div></div>`)
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

// fmtInt renders a counter without importing strconv into the page code.
func fmtInt(v int64) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
