package main

import (
	"strings"
	"testing"
)

// 账号页的主操作要在表头，和 tinker 的控制台一致。
func TestAccountsHeaderActions(t *testing.T) {
	page := renderAccountsView()
	for _, want := range []string{
		`data-call="refreshAccounts"`,
		`data-call="openLogin"`,
		"添加账号",
		"刷新",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("账号页缺少 %q", want)
		}
	}
}

// 统计条是两列布局，格数必须是偶数，否则会空半格。
func TestAccountsStatCardsEven(t *testing.T) {
	page := renderAccountsView()
	statsStart := strings.Index(page, `class="stats"`)
	statsEnd := strings.Index(page[statsStart:], `</div>`)
	block := page[statsStart : statsStart+statsEnd]
	if n := strings.Count(block, `class="stat`); n%2 != 0 {
		t.Errorf("统计卡 %d 个，两列布局会空半格", n)
	}
}

// 账号页只有表头那一个添加入口，页面里不再有独立的添加账号区块。
func TestAccountsHasSingleAddEntry(t *testing.T) {
	page := renderAccountsView()
	if strings.Contains(page, `id="authSection"`) {
		t.Error("不应再有独立的添加账号区块")
	}
	if strings.Contains(page, "renderAuthorisationBox") || strings.Contains(page, "粘贴 Cookie") {
		t.Error("Cookie 授权表单应已移除")
	}
	// 表头那一个按钮还在，且只出现一次
	if n := strings.Count(page, `data-call="openLogin"`); n != 1 {
		t.Errorf("添加账号按钮应只有 1 个，实际 %d", n)
	}
}

// 账号表底部不再有启停说明文字。
func TestAccountsFooterHasNoNote(t *testing.T) {
	page := renderAccountsView()
	if strings.Contains(page, "启停会写入 CPA 的 auth 文件") {
		t.Error("底部说明文字应已移除")
	}
	// 两个批量按钮还在
	for _, want := range []string{"全部启用", "全部停用"} {
		if !strings.Contains(page, want) {
			t.Errorf("缺少 %q", want)
		}
	}
}

// 设置页要能读到当前生效的上游地址，便于核对。
func TestSettingsShowsUpstreams(t *testing.T) {
	page := renderSettingsView()
	for _, r := range allRealms {
		profile := profileFor(r)
		if !strings.Contains(page, profile.Host) {
			t.Errorf("设置页未显示 %s 的地址 %s", r, profile.Host)
		}
	}
	if !strings.Contains(page, pluginVersion) {
		t.Error("设置页未显示插件版本")
	}
}

// 说明文字不能用最弱的文字色，那是给占位符用的。
func TestSettingsDescReadable(t *testing.T) {
	page := renderSettingsView()
	if strings.Contains(page, "color:var(--text-quaternary)") {
		t.Error("说明文字用了占位符色，对比度不足")
	}
}
