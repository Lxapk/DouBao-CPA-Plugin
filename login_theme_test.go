package main

import (
	"strings"
	"testing"
)

// 登录页必须跟随宿主主题。
//
// 它是被 window.open 打开的新标签页，读不到父窗口的 data-theme，只能靠
// URL 上的 theme 参数。缺了这段脚本，无论宿主选哪套配色登录页都停在暗色。
func TestLoginPageFollowsTheme(t *testing.T) {
	page := renderLoginPage("doubao-x")
	if !strings.Contains(page, "function adoptHostTheme") {
		t.Fatal("登录页没有主题同步逻辑")
	}
	if !strings.Contains(page, "new URLSearchParams(location.search).get(\"theme\")") {
		t.Error("没有从 URL 读主题")
	}
	if !strings.Contains(page, "root.setAttribute(\"data-theme\", theme)") {
		t.Error("没有应用主题")
	}
	if !strings.Contains(page, "root.removeAttribute(\"data-theme\")") {
		t.Error("跟随系统时应移除属性")
	}
}

// 控制台打开登录页时要把当前配色带上。
func TestPanelPassesThemeToLogin(t *testing.T) {
	script := panelScript()
	if !strings.Contains(script, `getAttribute("data-theme")`) {
		t.Error("打开登录页时没有读取当前主题")
	}
	if !strings.Contains(script, `"&theme=" + encodeURIComponent(theme)`) {
		t.Error("没有把主题拼进登录页 URL")
	}
}

// 输入框样式要覆盖所有输入型控件。
//
// 逐个列 type 会漏：登录页的手机号是 type="tel"，漏掉之后它在牛皮纸主题下
// 退回浏览器默认的白底，白字看不见。
func TestInputStylesCoverAllTypes(t *testing.T) {
	if !strings.Contains(uiCSS, `input:not([type="checkbox"])`) {
		t.Error("输入框样式仍在逐个列 type，容易漏")
	}
	// 登录页确实用了 tel
	page := renderLoginPage("doubao-x")
	if !strings.Contains(page, `type="tel"`) {
		t.Error("登录页应使用 type=tel")
	}
}
