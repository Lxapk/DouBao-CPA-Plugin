package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// 登录页要有国际版 / 国内版的切换按钮，否则用户只能登豆包。
func TestLoginPageHasRealmSwitch(t *testing.T) {
	page := renderLoginPage("doubao-x")
	for _, r := range allRealms {
		if !strings.Contains(page, `data-arg0="`+string(r)+`"`) {
			t.Errorf("登录页缺少 %s 的切换按钮", r)
		}
	}
	if !strings.Contains(page, `id="loginRealmSeg"`) {
		t.Error("缺少切换控件容器")
	}
	if !strings.Contains(page, `id="loginSite"`) {
		t.Error("缺少站点提示")
	}
	// 提交时必须带上选中的上游，否则切换按钮不起作用
	script := loginPageScript("doubao-x")
	if !strings.Contains(script, "realm: currentRealm()") {
		t.Error("提交时未带上所选上游")
	}
	if !strings.Contains(script, `data-call="pickLoginRealm"`) &&
		!strings.Contains(script, `action === "pickLoginRealm"`) {
		t.Error("切换按钮没有处理逻辑")
	}
}

// 页面按 state 记住的上游显示，不能一律显示豆包。
func TestLoginPageDefaultRealmFromState(t *testing.T) {
	page := renderLoginPage("dola-abc123")
	if !strings.Contains(page, `class="on" data-value="dola"`) {
		t.Error("dola 的页面应默认选中 Dola")
	}
	if !strings.Contains(page, "https://www.dola.com") {
		t.Error("站点提示应为 Dola")
	}

	// 豆包侧同理
	page = renderLoginPage("doubao-abc123")
	if !strings.Contains(page, `class="on" data-value="doubao"`) {
		t.Error("豆包页面应默认选中豆包")
	}
}

// 请求里选了哪一侧，就登哪一侧。
func TestRealmForLoginForm(t *testing.T) {
	cases := []struct {
		req  loginFormRequest
		want realm
	}{
		{loginFormRequest{Realm: "dola"}, realmDola},
		{loginFormRequest{Realm: "doubao"}, realmDoubao},
		// 没带 realm 时看 state 前缀
		{loginFormRequest{State: "dola-xyz"}, realmDola},
	}
	for _, c := range cases {
		if got := realmForLoginForm(c.req); got != c.want {
			t.Errorf("realmForLoginForm(%+v) = %q, 期望 %q", c.req, got, c.want)
		}
	}
}

// 站点映射要覆盖两侧，供切换时更新提示。
func TestSiteMapCoversRealms(t *testing.T) {
	var m map[string]string
	if err := json.Unmarshal([]byte(siteMapJSON()), &m); err != nil {
		t.Fatalf("站点映射不是 JSON: %v", err)
	}
	for _, r := range allRealms {
		if m[string(r)] != profileFor(r).Host {
			t.Errorf("%s 的站点缺失或不对: %q", r, m[string(r)])
		}
	}
}
