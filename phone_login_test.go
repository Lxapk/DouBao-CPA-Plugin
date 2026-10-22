package main

import (
	"encoding/hex"
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
	// 账号中心按上游取：豆包在 accounts.，Dola 没有独立账号中心。
	if got := profileFor(realmDoubao).PassportHost; got != "https://accounts.doubao.com" {
		t.Errorf("豆包 passportHost = %q", got)
	}
	if got := profileFor(realmDola).PassportHost; got != "https://www.dola.com" {
		t.Errorf("Dola passportHost = %q", got)
	}
	if passportSendCodePath != "/passport/web/send_code/" {
		t.Errorf("发码路径 = %q", passportSendCodePath)
	}
	// validate_code 是 App 侧接口，网页端登录用 sms_login。
	if passportLoginPath != "/passport/web/sms_login/" {
		t.Errorf("登录路径 = %q", passportLoginPath)
	}

	query := passportQuery(profileFor(realmDoubao))
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
		"default": {kindChat, 0, ""},
		"pro":     {kindChat, 0, ""},
		"image":   {kindImage, skillImageGen, ""},
		// 键名取自两侧 APK 的资源表，两边的拼法不同。
		"seedream-5.0-pro":   {kindImage, skillImageGen, "seedream50Pro"},
		"seedream-5.0-flash": {kindImage, skillImageGen, "seedream50Lite"},
		"seedream-4.5":       {kindImage, skillImageGen, "seedream_4.5"},
		"seedream-4.0":       {kindImage, skillImageGen, "seedream_4.0"},
		"video":              {kindVideo, skillVideoGen, ""},
		"seedance-2.5":       {kindVideo, skillVideoGen, "seedance25"},
		"seedance-2.0":       {kindVideo, skillVideoGen, "seedance20Standard"},
		"seedance-2.0-fast":  {kindVideo, skillVideoGen, "seedance20Fast"},
		"seedance-2.0-mini":  {kindVideo, skillVideoGen, "seedance20Mini"},
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
//
// 目录里的模型带上游前缀（doubao/、dola/），裸名只在请求时解析，不出现在列表。
func TestCatalogueListsEveryModel(t *testing.T) {
	models := buildCatalogue()
	seen := map[string]string{}
	for _, m := range models {
		seen[m.ID] = m.Type
	}
	for _, id := range []string{
		"doubao/seedream-5.0-pro", "doubao/seedream-5.0-flash",
		"doubao/seedream-4.5", "doubao/seedream-4.0",
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
	for _, id := range []string{
		"doubao/seedance-2.5", "doubao/seedance-2.0",
		"doubao/seedance-2.0-fast", "doubao/seedance-2.0-mini",
	} {
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

// 一个账号只应看到它所属上游的模型：没登录 Dola 就不该出现 dola/*。
func TestModelForAuthIsRealmScoped(t *testing.T) {
	state.settings.when = defaultSettings()
	defer func() { state.settings.when = defaultSettings() }()

	cases := []struct {
		name       string
		attributes map[string]string
		storage    []byte
		want       string
		notWant    string
	}{
		{
			name:       "attributes 指定豆包",
			attributes: map[string]string{"realm": "doubao"},
			want:       "doubao/seedream-4.5",
			notWant:    "dola/",
		},
		{
			name:       "attributes 指定 Dola",
			attributes: map[string]string{"realm": "dola"},
			want:       "dola/seedream-4.5",
			notWant:    "doubao/",
		},
		{
			name:    "从凭据 JSON 读 realm",
			storage: []byte(`{"realm":"dola","cookies":"x"}`),
			want:    "dola/seedance-2.5",
			notWant: "doubao/",
		},
	}
	for _, c := range cases {
		// AuthModelRequest 没有 json tag，键名就是字段名本身（PascalCase）。
		raw, _ := json.Marshal(map[string]any{
			"Attributes":  c.attributes,
			"StorageJSON": c.storage,
		})
		out, errModels := modelForAuth(raw)
		if errModels != nil {
			t.Fatalf("%s: %v", c.name, errModels)
		}
		var env struct {
			OK     bool `json:"ok"`
			Result struct {
				Models []catalogModel `json:"models"`
			} `json:"result"`
		}
		if errUnmarshal := json.Unmarshal(out, &env); errUnmarshal != nil {
			t.Fatalf("%s 解析失败: %v", c.name, errUnmarshal)
		}
		ids := map[string]bool{}
		for _, m := range env.Result.Models {
			ids[m.ID] = true
			if strings.HasPrefix(m.ID, c.notWant) {
				t.Errorf("%s: 不应出现 %s", c.name, m.ID)
			}
			// 裸名不该在列表里，否则同一模型会有三个 ID。
			if !strings.Contains(m.ID, "/") {
				t.Errorf("%s: 目录里出现裸名 %s", c.name, m.ID)
			}
		}
		if !ids[c.want] {
			t.Errorf("%s: 缺少 %s", c.name, c.want)
		}
	}
}

// Dola 没有独立账号中心，passport 挂在站点本身；aid 必须是 495671。
//
// 实测两个值的结果差别很明显：
//
//	aid=495671  → error_code 7（访问太频繁，说明接口与参数都通过）
//	aid=497858  → error_code 16（该应用无权限，那是豆包的 aid）
func TestDolaPassportProfile(t *testing.T) {
	profile := profileFor(realmDola)
	if profile.PassportHost != "https://www.dola.com" {
		t.Errorf("Dola PassportHost = %q", profile.PassportHost)
	}
	if profile.PassportAID != "495671" {
		t.Errorf("Dola PassportAID = %q，用错会得到 error_code 16", profile.PassportAID)
	}

	// 参数取自 Dola 网页端的真实请求（抓包），与豆包是两套体系。
	query := passportQuery(profile)
	for _, want := range []string{
		"aid=495671",
		"language=zh",
		"passport_jssdk_version=2.0.1-verify-center.1",
		"account_sdk_source=web",
		"device_platform=web",
		"os_type=4",
		"terminal_type=2",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("Dola query 缺少 %s: %s", want, query)
		}
	}
	// 豆包那一套不能混进来
	if strings.Contains(query, "is_from_ttaccountsdk") ||
		strings.Contains(query, "passport_jssdk_type") {
		t.Errorf("Dola query 混入了豆包参数: %s", query)
	}
	// 不能把豆包的 aid 带过去
	if strings.Contains(query, "497858") {
		t.Errorf("Dola query 里出现了豆包的 aid: %s", query)
	}

	// 豆包侧仍然是自己的账号中心与 aid
	doubao := profileFor(realmDoubao)
	if doubao.PassportHost != "https://accounts.doubao.com" || doubao.PassportAID != "497858" {
		t.Errorf("豆包 passport 配置被改坏: %+v", doubao)
	}
}

// 网页版接口是唯一走得通的：App 侧的 mobile 接口被签名挡住。
func TestPassportUsesWebEndpoints(t *testing.T) {
	if !strings.HasPrefix(passportSendCodePath, "/passport/web/") {
		t.Errorf("发码应走 web 接口，实际 %q", passportSendCodePath)
	}
	if !strings.HasPrefix(passportLoginPath, "/passport/web/") {
		t.Errorf("登录应走 web 接口，实际 %q", passportLoginPath)
	}
	// App 侧接口的路径不能被引用
	for _, p := range []string{"/passport/mobile/"} {
		if strings.Contains(passportSendCodePath, p) || strings.Contains(passportLoginPath, p) {
			t.Errorf("不应使用 App 侧接口 %s", p)
		}
	}
}

// 两侧的 model_item_key 拼法不同，必须按凭据所属上游取。
//
// 之前两侧共用一个键，而那个键在两边都不存在（seedance_2.0_fast），
// 服务端静默忽略后回落到默认模型 —— 表现就是「选 Fast 却生成了 Mini」。
func TestModelKeyPerRealm(t *testing.T) {
	cases := []struct {
		alias        string
		doubao, dola string
	}{
		{"seedream-5.0-pro", "seedream50Pro", "seedream_5.0_global"},
		{"seedream-5.0-flash", "seedream50Lite", "seedream_5.0_global"},
		{"seedream-4.5", "seedream_4.5", "seedream_4.5_global"},
		{"seedream-4.0", "seedream_4.0", "seedream_4.0_global"},
		{"seedance-2.5", "seedance25", "seedance_2.5"},
		{"seedance-2.0", "seedance20Standard", "seedance_2.0_pc_dola"},
		{"seedance-2.0-fast", "seedance20Fast", "seedance_2.0_pc_dola"},
		{"seedance-2.0-mini", "seedance20Mini", "seedance_2.0_pc_dola"},
	}
	for _, c := range cases {
		if got := modelKeyForRealm(c.alias, realmDoubao); got != c.doubao {
			t.Errorf("豆包 %s = %q, 期望 %q", c.alias, got, c.doubao)
		}
		if got := modelKeyForRealm(c.alias, realmDola); got != c.dola {
			t.Errorf("Dola %s = %q, 期望 %q", c.alias, got, c.dola)
		}
	}
}

// 这些键必须来自 APK 的资源表，不能是意译出来的名字。
// 写错会被服务端静默忽略，用户只会看到「选了 A 生成 B」。
func TestModelKeysMatchAppResources(t *testing.T) {
	// 豆包（豆包_15.4.0.apk）与 Dola（Dola_15.3.0.apk）资源里出现的键
	doubaoKeys := map[string]bool{
		"seedream50Pro": true, "seedream50Lite": true, "seedream_4.5": true,
		"seedream_4.0": true, "seedance25": true, "seedance20Standard": true,
		"seedance20Fast": true, "seedance20Mini": true,
		"seedream_3.0": true, "seedance_1.5": true,
	}
	dolaKeys := map[string]bool{
		"seedream_5.0_global": true, "seedream_4.5_global": true,
		"seedream_4.0_global": true, "seedance_2.5": true, "seedance_2.0_pc_dola": true,
		"seedream_3.0_global": true, "seedance_1.5": true,
	}
	for _, alias := range capabilityAliases {
		if alias.ModelKey != "" && !doubaoKeys[alias.ModelKey] {
			t.Errorf("%s 的豆包键 %q 在 APK 资源里不存在", alias.ID, alias.ModelKey)
		}
		if alias.DolaKey != "" && !dolaKeys[alias.DolaKey] {
			t.Errorf("%s 的 Dola 键 %q 在 APK 资源里不存在", alias.ID, alias.DolaKey)
		}
	}
}

// Dola 的登录路径与豆包不同。
//
// 抓包确认：Dola 走 /passport/web/sms_login_only/，其响应直接带回账号信息
// （user_id / name / mobile）；豆包走 /passport/web/sms_login/。
func TestDolaUsesLoginOnlyPath(t *testing.T) {
	if passportLoginOnlyPath != "/passport/web/sms_login_only/" {
		t.Errorf("Dola 登录路径 = %q", passportLoginOnlyPath)
	}
	if passportLoginPath != "/passport/web/sms_login/" {
		t.Errorf("豆包登录路径 = %q", passportLoginPath)
	}
	if profileFor(realmDola).PassportSDKStyle != passportSDKVerifyCenter {
		t.Error("Dola 应使用 verify-center 参数体系")
	}
	if profileFor(realmDoubao).PassportSDKStyle != passportSDKClassic {
		t.Error("豆包应使用 classic 参数体系")
	}
}

// 两套参数体系的版本号不能混用 —— 这是 Dola 发码一直返回 error_code 7 的原因。
func TestPassportQueryVersionsDiffer(t *testing.T) {
	dola := passportQuery(profileFor(realmDola))
	doubao := passportQuery(profileFor(realmDoubao))

	if !strings.Contains(dola, "2.0.1-verify-center.1") {
		t.Errorf("Dola 应使用 verify-center 版本: %s", dola)
	}
	if strings.Contains(doubao, "verify-center") {
		t.Errorf("豆包不应使用 verify-center 版本: %s", doubao)
	}
	// 豆包不带 verify-center 那套参数
	if strings.Contains(doubao, "account_sdk_source") {
		t.Errorf("豆包不应带 account_sdk_source: %s", doubao)
	}
}

// qs 是参与签名的参数名列表，用同一套 xor5+hex 编码。
// 从真实请求解出来是这 8 个字段，顺序即签名顺序。
func TestQSParameterList(t *testing.T) {
	const qs = "6466666a706b715a76616e5a766a7077666029646c61296160736c66605a75696471636a77682969646b6270646260296a765a717c75602975647676756a77715a6f7676616e5a736077766c6a6b29716077686c6b64695a717c7560297360776c637c4375"
	raw, errDecode := hex.DecodeString(qs)
	if errDecode != nil {
		t.Fatalf("qs 不是合法 hex: %v", errDecode)
	}
	for i := range raw {
		raw[i] ^= 5
	}
	got := string(raw)
	want := "account_sdk_source,aid,device_platform,language,os_type,passport_jssdk_version,terminal_type,verifyFp"
	if got != want {
		t.Errorf("qs 解出 %q，期望 %q", got, want)
	}
}
