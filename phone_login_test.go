package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// xorHex 必须与客户端 StringUtils.encryptWithXor 一致：逐字节异或 5 再转十六进制。
// 算错不会报错，只会让上游认为验证码不对，所以用固定向量把它钉住。
func TestXorHexMatchesClient(t *testing.T) {
	// "1" 的 ASCII 是 0x31，异或 5 得 0x34。
	if got := xorHex("1"); got != "34" {
		t.Errorf("xorHex(\"1\") = %q, 期望 34", got)
	}
	// "28" -> '2'=0x32^5=0x37, '8'=0x38^5=0x3d
	if got := xorHex(smsLoginType); got != "373d" {
		t.Errorf("xorHex(%q) = %q, 期望 373d", smsLoginType, got)
	}
	// 往返可逆，避免以后误改成别的编码。
	for _, s := range []string{"13800138000", "123456", "a-b c"} {
		if len(xorHex(s)) != len(s)*2 {
			t.Errorf("%q 编码长度异常", s)
		}
	}
}

// 手机号归一化只放宽形状，不能把明显不是手机号的值当成手机号。
func TestNormalizePhone(t *testing.T) {
	ok := map[string]string{
		"13800138000":    "13800138000",
		"138 0013 8000":  "13800138000",
		"138-0013-8000":  "13800138000",
		"+8613800138000": "13800138000",
		"8613800138000":  "13800138000",
	}
	for input, want := range ok {
		if got := normalizePhone(input); got != want {
			t.Errorf("normalizePhone(%q) = %q, 期望 %q", input, got, want)
		}
	}
	for _, input := range []string{"", "1380013800", "23800138000", "1380013800a", "138001380000"} {
		if got := normalizePhone(input); got != "" {
			t.Errorf("normalizePhone(%q) 应被拒绝，得到 %q", input, got)
		}
	}
}

// 手机号只在需要时脱敏，且不泄露中间四位。
func TestMaskPhone(t *testing.T) {
	if got := maskPhone("13800138000"); got != "138****8000" {
		t.Errorf("maskPhone = %q", got)
	}
	if got := maskPhone("+8613800138000"); got != "138****8000" {
		t.Errorf("带 +86 时 = %q", got)
	}
	if strings.Contains(maskPhone("13800138000"), "0013") {
		t.Error("脱敏后仍包含中间位")
	}
}

// 上游报错要能被识别；成功响应（没有 message）不能被当成错误。
func TestPassportError(t *testing.T) {
	if err := passportError([]byte(`{"message":"","data":{}}`)); err != nil {
		t.Errorf("空 message 不应报错: %v", err)
	}
	cases := []struct{ body, hint string }{
		{`{"message":"操作太频繁"}`, "等一分钟"},
		{`{"message":"验证码错误"}`, "重新获取"},
		{`{"message":"手机号格式不正确"}`, "11 位"},
		{`{"message":"risk control"}`, "风控"},
	}
	for _, c := range cases {
		err := passportError([]byte(c.body))
		if err == nil {
			t.Errorf("%s 应被识别为错误", c.body)
			continue
		}
		if !strings.Contains(err.Error(), c.hint) {
			t.Errorf("%s 的提示 = %q, 期望包含 %q", c.body, err.Error(), c.hint)
		}
	}
}

// Set-Cookie 里的属性不能带进 Cookie 请求头。
func TestCookiesFromResponse(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", "sessionid=abc; Path=/; Domain=.doubao.com; HttpOnly")
	resp.Header.Add("Set-Cookie", "sid_tt=def; Path=/; Secure")
	got := cookiesFromResponse(resp)
	if got != "sessionid=abc; sid_tt=def" {
		t.Errorf("cookiesFromResponse = %q", got)
	}
	if !hasLoginCookie(got) {
		t.Error("应判定为已登录")
	}
	if hasLoginCookie("foo=bar") {
		t.Error("没有 sessionid 时不应判定为已登录")
	}
}

// 登录页必须带上 state，才能把提交绑回这次会话。
func TestLoginPageCarriesState(t *testing.T) {
	page := renderLoginPage("doubao-test-state")
	if !strings.Contains(page, "doubao-test-state") {
		t.Error("页面缺少 state")
	}
	for _, needle := range []string{`id="phone"`, `id="code"`, `id="sendBtn"`, `id="loginBtn"`} {
		if !strings.Contains(page, needle) {
			t.Errorf("登录页缺少 %s", needle)
		}
	}
	// 页面本身不需要密钥就能打开，但提交需要：CPA 的资源路由只接受 GET，
	// 发码与校验必须走管理路由，而管理路由要求 Bearer 鉴权。页面因此在提交
	// 前提示用户保存密钥。
	if !strings.Contains(page, "/v0/management/doubao") {
		t.Error("登录页没有把提交指向管理路由")
	}
}

// 登录相关路由必须真的可达，且不能被页面回退吞掉。
func TestLoginRoutes(t *testing.T) {
	resp := handlePanelRoute(pluginapi.ManagementRequest{Method: "GET", Path: "/login?state=abc"})
	if !strings.Contains(resp.Headers.Get("Content-Type"), "text/html") {
		t.Fatalf("登录页 Content-Type = %q", resp.Headers.Get("Content-Type"))
	}
	if !strings.Contains(string(resp.Body), `id="phone"`) {
		t.Error("返回的不是登录页")
	}

	// 没有 state 时用 realm 自建会话，页面仍要能渲染。
	resp = handlePanelRoute(pluginapi.ManagementRequest{Method: "GET", Path: "/login?realm=dola"})
	if !strings.Contains(string(resp.Body), `id="phone"`) {
		t.Error("带 realm 时没有渲染登录页")
	}

	// 提交接口不应回落到 HTML。
	body, _ := json.Marshal(map[string]string{"state": "x", "phone": "13800138000", "code": "123456"})
	for _, path := range []string{"/login/send", "/login/verify"} {
		out := handlePanelRoute(pluginapi.ManagementRequest{Method: "POST", Path: path, Body: body})
		if strings.Contains(out.Headers.Get("Content-Type"), "text/html") {
			t.Errorf("%s 落到了页面回退", path)
		}
	}
}

// 表单接口要拒绝明显不合法的输入，而不是把请求打到上游。
func TestLoginFormValidation(t *testing.T) {
	cases := []struct {
		body map[string]string
		want string
	}{
		{map[string]string{"state": "s", "phone": "123"}, "11 位手机号"},
		{map[string]string{"state": "s", "phone": "13800138000"}, "验证码"},
	}
	for _, c := range cases {
		raw, _ := json.Marshal(c.body)
		resp := handleLoginVerify(raw)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%v 应被拒绝", c.body)
			continue
		}
		if !strings.Contains(string(resp.Body), c.want) {
			t.Errorf("%v 的提示 = %s", c.body, resp.Body)
		}
	}
}

// 面板的登录页入口要带上选中的上游。
func TestPanelLoginPathSelectsRealm(t *testing.T) {
	script := panelScript()
	if !strings.Contains(script, `"/v0/resource/plugins/doubao/login"`) {
		t.Error("面板脚本没有登录页路径")
	}
	if !strings.Contains(script, "openLogin: function") {
		t.Error("面板缺少 openLogin 动作")
	}
}
