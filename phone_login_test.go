package main

import (
	"encoding/json"
	"net/http"
	"os"
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
	// "24" -> '2'=0x32^5=0x37, '4'=0x34^5=0x31
	if got := xorHex(smsLoginType); got != "3731" {
		t.Errorf("xorHex(%q) = %q, 期望 3731", smsLoginType, got)
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

// 加密前的 mobile 必须带 "+86 " 前缀，否则上游回 1003「手机号错误」。
// 这里的十六进制是浏览器真实请求里抓到的值，可以直接对照。
func TestPassportMobileMatchesBrowser(t *testing.T) {
	if got := passportMobile("13800138000"); got != "+86 13800138000" {
		t.Errorf("passportMobile = %q", got)
	}
	if got := xorHex(passportMobile("13800138000")); got != "2e3d332534363d353534363d353535" {
		t.Errorf("加密结果 = %q, 期望与浏览器一致", got)
	}
	if strings.Contains(passportMobile("13800138000"), "%") {
		t.Error("mobile 不应做 URL 转义")
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
	// 成功：error_code 为 0，message 是 "success"。这条曾经被判成失败。
	if err := passportError([]byte(`{"data":{"error_code":0},"message":"success"}`)); err != nil {
		t.Errorf("message=success 应视为成功: %v", err)
	}
	if err := passportError([]byte(`{"data":{"error_code":0},"message":""}`)); err != nil {
		t.Errorf("空响应应视为成功: %v", err)
	}

	// 失败：网关一律带非零 error_code。
	cases := []struct{ body, hint string }{
		{`{"data":{"error_code":1206,"description":"验证码发送太频繁，请稍后再试"},"message":"error"}`, "等一分钟"},
		{`{"data":{"error_code":1203,"description":"错误次数过多或验证码过期，请稍后重试"},"message":"error"}`, "重新获取"},
		{`{"data":{"error_code":1202,"description":"验证码错误，请重新输入"},"message":"error"}`, "核对"},
		{`{"data":{"error_code":1003,"description":"手机号错误，请重新填写"},"message":"error"}`, "11 位"},
		{`{"data":{"error_code":16,"description":"该应用无权限"},"message":"error"}`, ""},
	}
	for _, c := range cases {
		err := passportError([]byte(c.body))
		if err == nil {
			t.Errorf("%s 应被识别为错误", c.body)
			continue
		}
		if c.hint != "" && !strings.Contains(err.Error(), c.hint) {
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

// readSourceForTest 读源码文本，用于断言「某段代码里没有出现某个调用」。
func readSourceForTest(t *testing.T, name string) string {
	t.Helper()
	raw, errRead := os.ReadFile(name)
	if errRead != nil {
		t.Skipf("读不到 %s: %v", name, errRead)
	}
	return string(raw)
}

// 请求形状必须与网页端一致：换成 App 的接口或少了 jssdk 参数，上游都会回
// 「该应用无权限」(error_code 16)。这里把三处关键值钉住，避免以后误改。
func TestPassportRequestShape(t *testing.T) {
	if passportHost != "https://accounts.doubao.com" {
		t.Errorf("passportHost = %q", passportHost)
	}
	if passportSendCodePath != "/passport/web/send_code/" {
		t.Errorf("发码路径 = %q", passportSendCodePath)
	}
	// validate_code 是 App 侧接口，网页端登录用 sms_login。
	if passportLoginPath != "/passport/web/sms_login/" {
		t.Errorf("登录路径 = %q", passportLoginPath)
	}

	query := passportQuery("497858")
	for _, want := range []string{
		"passport_jssdk_version=", "passport_jssdk_type=normal",
		"is_from_ttaccountsdk=1", "aid=497858", "language=zh",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("query 缺少 %s: %s", want, query)
		}
	}
}

// 登录请求不带 type，改用 login_only / ignore_lark_account。
func TestLoginFormHasNoType(t *testing.T) {
	src := readSourceForTest(t, "phone_login.go")
	loginPart := src[strings.Index(src, "func loginWithCode"):]
	loginPart = loginPart[:strings.Index(loginPart, "\n}")]
	if strings.Contains(loginPart, `Set("type"`) {
		t.Error("登录请求不应带 type")
	}
	for _, want := range []string{`"login_only"`, `"ignore_lark_account"`, `"is6Digits"`} {
		if !strings.Contains(loginPart, want) && want != `"is6Digits"` {
			t.Errorf("登录请求缺少 %s", want)
		}
	}
}

// 短信登录拿到的 Cookie 没有 flow_cur_user_sec_id（那是网页前端写的），
// 发请求必须由插件补上，否则 IM 接口一律判定会话无效。
func TestSecUserIDIsInjectedIntoCookie(t *testing.T) {
	creds := &credentials{
		Realm:     realmDoubao,
		Cookies:   "sessionid=abc; sid_tt=def",
		SecUserID: "1234567890",
	}
	got := creds.sanitizedCookies()
	if !strings.Contains(got, "flow_cur_user_sec_id=1234567890") {
		t.Errorf("未补 flow_cur_user_sec_id: %q", got)
	}
	// 不能破坏原有 Cookie。
	if !strings.Contains(got, "sessionid=abc") || !strings.Contains(got, "sid_tt=def") {
		t.Errorf("原有 Cookie 被破坏: %q", got)
	}

	// 已经带了就不要重复追加。
	withField := &credentials{
		Realm:     realmDoubao,
		Cookies:   "sessionid=abc; flow_cur_user_sec_id=999",
		SecUserID: "1234567890",
	}
	out := withField.sanitizedCookies()
	if strings.Count(out, "flow_cur_user_sec_id=") != 1 {
		t.Errorf("重复追加: %q", out)
	}
}

// 有账号 handle 但没有 flow_cur_user_sec_id 的凭据应被接受：
// 短信登录就是这个形态，字段由插件在发请求时补。
func TestValidateAcceptsSecUserIDWithoutFlowCookie(t *testing.T) {
	creds := &credentials{
		Realm:     realmDoubao,
		Cookies:   "sessionid=abc",
		SecUserID: "1234567890",
	}
	if err := creds.validate(); err != nil {
		t.Errorf("应被接受: %v", err)
	}

	// 两个都没有才拒绝。
	bare := &credentials{Realm: realmDoubao, Cookies: "sessionid=abc"}
	if err := bare.validate(); err == nil {
		t.Error("缺少账号标识时应拒绝")
	}
}

// 每个具体模型必须能解析出正确的端点归属、技能 ID 和 model_item_key。
// 弄错任何一个都会得到「要它画图却回一段文字」或「用了默认模型」。
func TestModelCatalogue(t *testing.T) {
	type want struct {
		kind  string
		skill int
		key   string
	}
	cases := map[string]want{
		"default":            {kindChat, 0, ""},
		"pro":                {kindChat, 0, ""},
		"image":              {kindImage, skillImageGen, ""},
		"seedream-5.0-pro":   {kindImage, skillImageGen, "seedream_5.0_pro"},
		"seedream-5.0-flash": {kindImage, skillImageGen, "seedream_5.0"},
		"seedream-4.5":       {kindImage, skillImageGen, "seedream_4.5"},
		"seedream-4.0":       {kindImage, skillImageGen, "seedream_4.0"},
		"video":              {kindVideo, skillVideoGen, ""},
		"seedance-2.5":       {kindVideo, skillVideoGen, "seedance_2.5"},
		"seedance-2.0":       {kindVideo, skillVideoGen, "seedance_2.0"},
		"seedance-2.0-fast":  {kindVideo, skillVideoGen, "seedance_2.0_fast"},
		"seedance-2.0-mini":  {kindVideo, skillVideoGen, "seedance_2.0_mini"},
	}
	for name, w := range cases {
		// 带与不带上游前缀都应按同一别名解析。
		for _, model := range []string{name, "doubao/" + name, "dola/" + name} {
			if got := kindForModel(model); got != w.kind {
				t.Errorf("kindForModel(%q) = %q, 期望 %q", model, got, w.kind)
			}
			if got := skillForModel(model); got != w.skill {
				t.Errorf("skillForModel(%q) = %d, 期望 %d", model, got, w.skill)
			}
			if got := modelKeyFor(model); got != w.key {
				t.Errorf("modelKeyFor(%q) = %q, 期望 %q", model, got, w.key)
			}
		}
	}
}

// 图片端点的 /v1/models 条目要带上 openai-image 类型，且每个具体模型都要出现。
func TestCatalogueListsEveryModel(t *testing.T) {
	models := buildCatalogue()
	seen := map[string]string{}
	for _, m := range models {
		seen[m.ID] = m.Type
	}
	for _, id := range []string{
		"seedream-5.0-pro", "seedream-5.0-flash", "seedream-4.5", "seedream-4.0",
	} {
		typ, ok := seen[id]
		if !ok {
			t.Errorf("目录缺少 %s", id)
			continue
		}
		if typ != "openai-image" {
			t.Errorf("%s 类型 = %q, 期望 openai-image", id, typ)
		}
	}
	for _, id := range []string{"seedance-2.5", "seedance-2.0", "seedance-2.0-fast", "seedance-2.0-mini"} {
		if _, ok := seen[id]; !ok {
			t.Errorf("目录缺少 %s", id)
		}
	}
}

// 请求里的 model_item_key 必须真的带上所选模型。
func TestCompletionRequestCarriesModelKey(t *testing.T) {
	creds := &credentials{Realm: realmDoubao, BotID: "1", Cookies: "sessionid=x"}
	req := buildCompletionRequest(creds, "一只猫", generationMode{
		Skill: skillImageGen, Kind: kindImage, ModelKey: "seedream_5.0_pro",
	}, "conv", "", 0)
	if req.Option.ModelConfig.ModelItemKey != "seedream_5.0_pro" {
		t.Errorf("ModelConfig.ModelItemKey = %q", req.Option.ModelConfig.ModelItemKey)
	}
	if req.Option.AggregateParams.ModelItemKey != "seedream_5.0_pro" {
		t.Errorf("AggregateParams.ModelItemKey = %q", req.Option.AggregateParams.ModelItemKey)
	}

	// 未指定模型时回落为 "0"（交给上游挑默认）。
	req = buildCompletionRequest(creds, "hi", generationMode{Kind: kindChat}, "conv", "", 0)
	if req.Option.ModelConfig.ModelItemKey != "0" {
		t.Errorf("默认 ModelItemKey = %q, 期望 0", req.Option.ModelConfig.ModelItemKey)
	}
}
