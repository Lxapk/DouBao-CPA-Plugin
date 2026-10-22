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
	// 四个格子：手机上统计条是两列，格数必须是偶数，否则最后一行空半格。
	// 值是纯数字 —— .stat .v 用等宽字体且 nowrap，塞长文本会把字号挤变形。
	// 四个格子，值都是纯数字。
	//
	// 格数必须是偶数：手机上一行两列，五格会留下半格空白。上游分布也不能塞进
	// .stat .v —— 那是等宽字体加 nowrap，长文本会被压得比其他格子小一号。
	statCard(&b, "", "账号总数", total)
	statCard(&b, "good", "启用中", enabled)
	statCard(&b, "", "豆包账号", countRealm(entries, realmDoubao))
	statCard(&b, "bad", "已停用", blocked)
	b.WriteString(`</div>`)

	b.WriteString(`<div class="box">`)
	b.WriteString(`<header><h3>账号 <span class="hint">授权后自动出现</span></h3>`)
	b.WriteString(`<span class="grow"></span><span class="note" id="accountMsg"></span>`)
	b.WriteString(`<button type="button" class="xs" data-call="refreshAccounts">刷新</button>`)
	b.WriteString(`<button type="button" class="xs primary" data-call="openLogin">添加账号</button>`)
	b.WriteString(`</header>`)

	// The table is filled from /accounts on load and replaced after every action,
	// so a row never shows a state the backend disagrees with.
	b.WriteString(`<div id="acctWrap">`)
	if total == 0 {
		b.WriteString(`<div class="empty">还没有账号。点右上角「添加账号」，用手机号收验证码登录。</div>`)
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

	b.WriteString(`<div class="foot"><span class="grow"></span>` +
		`<button type="button" class="xs" data-call="bulkAccounts" data-arg0="enable">全部启用</button>` +
		`<button type="button" class="xs" data-call="bulkAccounts" data-arg0="disable">全部停用</button>` +
		`</div>`)
	b.WriteString(`</div>`)

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
