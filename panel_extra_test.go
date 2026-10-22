package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// The three pages the panel grew must be in the document together: the script
// switches by toggling [hidden], so a page rendered separately would never show.
func TestPanelRendersEveryView(t *testing.T) {
	page := renderMainPage()
	for _, view := range []string{
		`id="view-accounts"`,
		`id="view-usage"`,
		`id="view-guide"`,
		`id="view-settings"`,
	} {
		if !strings.Contains(page, view) {
			t.Errorf("页面缺少 %s", view)
		}
	}
	// Each tab has to name a view that exists, otherwise clicking it shows nothing.
	for _, view := range []string{"view-accounts", "view-usage", "view-guide", "view-settings"} {
		if !strings.Contains(page, `data-view="`+view+`"`) {
			t.Errorf("导航缺少 %s", view)
		}
	}
}

// The panel's script must not reference an endpoint the router does not serve:
// a typo there produces a silent 404 that reads as a backend fault.
func TestPanelScriptEndpointsAreRouted(t *testing.T) {
	script := panelScript()
	for _, path := range []string{
		"/accounts", "/account/toggle", "/account/remove",
		"/realm/switch", "/settings", "/calls", "/calls/clear",
		"/routing/config", "/routing/reset",
	} {
		if !strings.Contains(script, `"`+path+`"`) {
			t.Errorf("脚本未使用 %s", path)
			continue
		}
		// A mutation answers 400 without a body, which still proves the route is
		// wired; what must never happen is the page fallback, whose body is HTML.
		resp := handlePanelRoute(pluginapi.ManagementRequest{Method: "GET", Path: path})
		if strings.Contains(resp.Headers.Get("Content-Type"), "text/html") {
			t.Errorf("%s 落到了页面回退", path)
		}
	}
	for _, action := range []string{
		"refreshAccounts", "toggleAccount", "bulkAccounts", "removeAccount",
		"openLogin", "setRealm",
		"setExpose", "pickStrategy", "resetRotation",
		"refreshUsage", "clearCalls", "saveKey", "clearKey",
	} {
		if !strings.Contains(script, action+": function") {
			t.Errorf("脚本缺少动作 %s", action)
		}
	}
}

// The stats endpoints must answer with the shapes the script reads.
func TestStatsEndpointsAnswer(t *testing.T) {
	saved := stats.items
	stats.items = map[string]*accountStats{
		"u1": {Key: "u1", Label: "豆包 · u1", Realm: "doubao", CallCount: 3, SuccessCount: 2, FailureCount: 1, LastUsedUnix: 1_700_000_000},
	}
	defer func() { stats.items = saved }()

	state.logs.add(callEntry{Account: "豆包 · u1", Model: "doubao/default", Realm: "doubao", Status: "ok", Duration: 12})

	statusResp := handlePanelRoute(pluginapi.ManagementRequest{Method: "GET", Path: "/status"})
	var status statusSnapshot
	if errUnmarshal := json.Unmarshal(statusResp.Body, &status); errUnmarshal != nil {
		t.Fatalf("status 不是合法 JSON: %v", errUnmarshal)
	}
	if status.Routing["strategy"] == "" {
		t.Error("status 缺少路由策略")
	}

	callsResp := handlePanelRoute(pluginapi.ManagementRequest{Method: "GET", Path: "/calls"})
	var calls struct {
		Calls []callEntry `json:"calls"`
	}
	if errUnmarshal := json.Unmarshal(callsResp.Body, &calls); errUnmarshal != nil {
		t.Fatalf("/calls 不是合法 JSON: %v", errUnmarshal)
	}
	if len(calls.Calls) != 1 || calls.Calls[0].Model != "doubao/default" {
		t.Errorf("/calls 内容异常: %+v", calls.Calls)
	}

	clearResp := handlePanelRoute(pluginapi.ManagementRequest{Method: "POST", Path: "/calls/clear"})
	if clearResp.StatusCode != 200 {
		t.Errorf("/calls/clear -> %d", clearResp.StatusCode)
	}
	if len(state.logs.recent(10)) != 0 {
		t.Error("清空后仍有记录")
	}
}

// countRealm backs the accounts page's summary strip.
func TestCountRealm(t *testing.T) {
	entries := []hostAuthEntry{{Realm: realmDoubao}, {Realm: realmDoubao}, {Realm: realmDola}}
	if got := countRealm(entries, realmDoubao); got != 2 {
		t.Errorf("豆包账号数 = %d, 期望 2", got)
	}
	if got := countRealm(entries, realmDola); got != 1 {
		t.Errorf("Dola 账号数 = %d, 期望 1", got)
	}
}

// The skill ids come from the client enum, not from its ordinals. An image
// request that names the wrong skill silently answers with text instead of a
// picture, which is the failure mode that wasted the most time on this plugin.
func TestSkillIDsMatchClientEnum(t *testing.T) {
	if skillImageGen != 2001 || skillVideoGen != 2002 {
		t.Errorf("技能 ID 与客户端枚举不一致: image=%d video=%d", skillImageGen, skillVideoGen)
	}
}

func TestModeForModelDeepThink(t *testing.T) {
	if mode := modeForModel("doubao/pro"); !mode.DeepThink || mode.Skill != 0 {
		t.Errorf("pro 应为深度思考: %+v", mode)
	}
	if mode := modeForModel("dola/image"); mode.DeepThink || mode.Skill != skillImageGen || mode.Kind != "image" {
		t.Errorf("image 解析错误: %+v", mode)
	}
	if mode := modeForModel("default"); mode.Skill != 0 || mode.DeepThink {
		t.Errorf("default 应为普通对话: %+v", mode)
	}
}
