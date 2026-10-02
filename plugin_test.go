package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// pluginStateDir is overridable so tests can redirect the state file.
var _ = 0

// newTestCredentials builds a credential with generated device fields.
func newTestCredentials(r realm, cookies string) *credentials {
	if cookies == "" {
		cookies = "sessionid=a; flow_cur_user_sec_id=b"
	}
	c := &credentials{Realm: r, Cookies: cookies}
	return c.withDefaults()
}

// Unit tests. Everything here runs offline: no account, no network. The live
// tests in live_probe_test.go cover the upstream contract; these cover the
// parsing and routing the plugin does on its own.

func TestNormalizeRealm(t *testing.T) {
	cases := map[string]realm{
		"doubao":        realmDoubao,
		"豆包":            realmDoubao,
		"CN":            realmDoubao,
		"china":         realmDoubao,
		"dola":          realmDola,
		"Dola":          realmDola,
		"intl":          realmDola,
		"international": realmDola,
		"":              "",
		"nonsense":      "",
	}
	for in, want := range cases {
		if got := normalizeRealm(in); got != want {
			t.Errorf("normalizeRealm(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRealmFromHost(t *testing.T) {
	cases := map[string]realm{
		"https://www.doubao.com/chat/": realmDoubao,
		"www.dola.com":                 realmDola,
		"example.com":                  "",
	}
	for in, want := range cases {
		if got := realmFromHost(in); got != want {
			t.Errorf("realmFromHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRealmProfilesDifferOnlyAsDocumented(t *testing.T) {
	// The two realms share one protocol. If a future change makes them diverge
	// beyond host and identity, one executor can no longer serve both.
	doubao := profileFor(realmDoubao)
	dola := profileFor(realmDola)

	if doubao.Host == dola.Host {
		t.Error("hosts should differ")
	}
	if doubao.AID == dola.AID {
		t.Error("application ids should differ")
	}
	if doubao.Region == dola.Region {
		t.Error("regions should differ")
	}
	if doubao.CookieDomain == dola.CookieDomain {
		t.Error("cookie domains should differ")
	}
}

func TestSplitReasoning(t *testing.T) {
	cases := []struct {
		in     string
		answer string
		reason string
	}{
		{"<think></think>收到", "收到", ""},
		{"<think>想一下</think>答案是2", "答案是2", "想一下"},
		{"没有思考标签", "没有思考标签", ""},
		{"<think>a\nb</think>结果", "结果", "a\nb"},
	}
	for _, c := range cases {
		gotAnswer, gotReason := splitReasoning(c.in)
		if gotAnswer != c.answer {
			t.Errorf("splitReasoning(%q) answer = %q, want %q", c.in, gotAnswer, c.answer)
		}
		if gotReason != c.reason {
			t.Errorf("splitReasoning(%q) reason = %q, want %q", c.in, gotReason, c.reason)
		}
	}
}

func TestSplitReasoningUnclosedTag(t *testing.T) {
	// A stream cut mid-reasoning leaves the tag open. The reasoning is still
	// reasoning, and there is no answer.
	answer, reasoning := splitReasoning("正常<think>没闭合的思考")
	if answer != "正常" {
		t.Errorf("answer = %q, want %q", answer, "正常")
	}
	if reasoning != "没闭合的思考" {
		t.Errorf("reasoning = %q, want %q", reasoning, "没闭合的思考")
	}
}

func TestBuildQueryContainsRequiredParams(t *testing.T) {
	creds := newTestCredentials(realmDoubao, "a=b")
	q := buildQuery(creds, nil)

	for _, want := range []string{"aid=497858", "device_platform=web", "region=CN", "flow_im_arch=v2"} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q: %s", want, q)
		}
	}
	// Signature parameters are deliberately absent: the endpoints this plugin
	// uses do not require them, and replaying a captured one triggers the
	// anti-abuse challenge.
	if strings.Contains(q, "a_bogus") || strings.Contains(q, "msToken") {
		t.Errorf("query should not carry signature params: %s", q)
	}
}

func TestBuildQueryPerRealm(t *testing.T) {
	doubao := buildQuery(newTestCredentials(realmDoubao, ""), nil)
	if !strings.Contains(doubao, "aid=497858") || !strings.Contains(doubao, "region=CN") {
		t.Errorf("doubao query wrong: %s", doubao)
	}
	dola := buildQuery(newTestCredentials(realmDola, ""), nil)
	if !strings.Contains(dola, "aid=495671") || !strings.Contains(dola, "region=JP") {
		t.Errorf("dola query wrong: %s", dola)
	}
}

func TestEndpointURLUsesRealmHost(t *testing.T) {
	doubao := endpointURL(newTestCredentials(realmDoubao, ""), "/chat/completion", nil)
	if !strings.HasPrefix(doubao, "https://www.doubao.com/chat/completion?") {
		t.Errorf("doubao url = %s", doubao)
	}
	dola := endpointURL(newTestCredentials(realmDola, ""), "/chat/completion", nil)
	if !strings.HasPrefix(dola, "https://www.dola.com/chat/completion?") {
		t.Errorf("dola url = %s", dola)
	}
}

func TestSanitizedCookiesCollapsesNewlines(t *testing.T) {
	// Go's HTTP client refuses a header value containing a newline, and pasted
	// cookies routinely contain them.
	creds := &credentials{Cookies: "sessionid=abc;\n  uid_tt=def;\n\n  sid_tt=xyz"}
	got := creds.sanitizedCookies()
	if strings.ContainsAny(got, "\n\r") {
		t.Fatalf("newlines not removed: %q", got)
	}
	if got != "sessionid=abc; uid_tt=def; sid_tt=xyz" {
		t.Errorf("got %q", got)
	}
}

func TestCredentialsValidate(t *testing.T) {
	ok := &credentials{Realm: realmDoubao, Cookies: "sessionid=a; flow_cur_user_sec_id=b"}
	if err := ok.validate(); err != nil {
		t.Errorf("valid credential rejected: %v", err)
	}

	missingFlow := &credentials{Realm: realmDoubao, Cookies: "sessionid=a"}
	if err := missingFlow.validate(); err == nil {
		t.Error("credential without flow_cur_user_sec_id should be rejected")
	}

	missingSession := &credentials{Realm: realmDoubao, Cookies: "flow_cur_user_sec_id=b"}
	if err := missingSession.validate(); err == nil {
		t.Error("credential without sessionid should be rejected")
	}

	noRealm := &credentials{Cookies: "sessionid=a; flow_cur_user_sec_id=b"}
	if err := noRealm.validate(); err == nil {
		t.Error("credential without realm should be rejected")
	}
}

func TestSessionExpiryFromSidGuard(t *testing.T) {
	raw := "sid_guard=abc%7C1790869737%7C2592000%7CSat%2C+31-Oct-2026+15%3A48%3A57+GMT"
	got := sessionExpiryFromCookies(raw)
	if got.IsZero() {
		t.Fatal("expiry not parsed")
	}
	if got.Year() != 2026 || got.Month() != 10 || got.Day() != 31 {
		t.Errorf("expiry = %v, want 2026-10-31", got)
	}
}

func TestResolveUpstreamModel(t *testing.T) {
	state.settings.when = defaultSettings() // realm_default = doubao

	cases := []struct {
		in    string
		realm realm
		upstr string
	}{
		{"default", realmDoubao, ""},
		{"doubao/default", realmDoubao, ""},
		{"dola/default", realmDola, ""},
		{"doubao/pro", realmDoubao, ""},
		{"dola/image", realmDola, ""},
		{"unknown-model", realmDoubao, "unknown-model"},
		{"dola/unknown", realmDola, "unknown"},
	}
	for _, c := range cases {
		gotRealm, gotUp := resolveUpstreamModel(c.in)
		if gotRealm != c.realm || gotUp != c.upstr {
			t.Errorf("resolveUpstreamModel(%q) = (%q,%q), want (%q,%q)",
				c.in, gotRealm, gotUp, c.realm, c.upstr)
		}
	}
}

func TestSkillForModel(t *testing.T) {
	state.settings.when = defaultSettings()
	if s := skillForModel("doubao/image"); s != skillImageGen {
		t.Errorf("image model skill = %d, want %d", s, skillImageGen)
	}
	if s := skillForModel("dola/video"); s != skillVideoGen {
		t.Errorf("video model skill = %d, want %d", s, skillVideoGen)
	}
	if s := skillForModel("doubao/default"); s != 0 {
		t.Errorf("chat model skill = %d, want 0", s)
	}
}

func TestBuildPromptSingleUser(t *testing.T) {
	msgs := json.RawMessage(`[{"role":"user","content":"你好"}]`)
	got, err := buildPrompt(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if got != "你好" {
		t.Errorf("got %q, want %q", got, "你好")
	}
}

func TestBuildPromptWithSystemAndHistory(t *testing.T) {
	msgs := json.RawMessage(`[
		{"role":"system","content":"你是助手"},
		{"role":"user","content":"问题"},
		{"role":"assistant","content":"答案"}
	]`)
	got, err := buildPrompt(msgs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"你是助手", "【对话记录】", "用户：问题", "助手：答案"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}
}

func TestBuildPromptContentParts(t *testing.T) {
	// The array form is what multimodal clients send.
	msgs := json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"描述这张图"}]}]`)
	got, err := buildPrompt(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if got != "描述这张图" {
		t.Errorf("got %q", got)
	}
}

func TestEstimateTokens(t *testing.T) {
	if n := estimateTokens(""); n != 0 {
		t.Errorf("empty = %d, want 0", n)
	}
	// CJK is about one token per character.
	if n := estimateTokens("你好世界"); n != 4 {
		t.Errorf("CJK = %d, want 4", n)
	}
	// Latin is about one per four characters.
	if n := estimateTokens("abcdefgh"); n != 2 {
		t.Errorf("latin = %d, want 2", n)
	}
}

func TestParseCreationBlock(t *testing.T) {
	// Shape taken from a real response: the first frame carries only
	// dimensions, the second carries the key and the thumbnail URL.
	raw := json.RawMessage(`{
		"creation_block": {"creations": [{
			"type": 1,
			"id": "40396788168705026",
			"can_publish": true,
			"image": {
				"status": 1,
				"key": "tos-cn-i-a9rns2rl98/rc_gen_image/2278845749844478bc1036ae75e01e42.jpeg",
				"placeholder": {"width": 2048, "height": 2048, "description": "Seedream 5.0 Flash"},
				"image_thumb": {"url": "https://example.invalid/thumb.png", "width": 2048, "height": 2048}
			}
		}]}
	}`)

	assets := parseCreationBlock(raw)
	if len(assets) != 1 {
		t.Fatalf("got %d assets, want 1", len(assets))
	}
	a := assets[0]
	if a.Key == "" || !strings.Contains(a.Key, "rc_gen_image/") {
		t.Errorf("key = %q", a.Key)
	}
	if a.URL != "https://example.invalid/thumb.png" {
		t.Errorf("url = %q", a.URL)
	}
	if a.Width != 2048 || a.Height != 2048 {
		t.Errorf("size = %dx%d", a.Width, a.Height)
	}
	if a.Model != "Seedream 5.0 Flash" {
		t.Errorf("model = %q", a.Model)
	}
	if a.isVideo() {
		t.Error("should not be a video")
	}
}

func TestParseCreationBlockPlaceholderOnly(t *testing.T) {
	// A frame with neither key nor URL has nothing to hand back yet.
	raw := json.RawMessage(`{"creation_block":{"creations":[{
		"type":1,"id":"1","image":{"status":1,"placeholder":{"width":2048,"height":2048}}
	}]}}`)
	if assets := parseCreationBlock(raw); len(assets) != 0 {
		t.Errorf("placeholder-only frame yielded %d assets, want 0", len(assets))
	}
}

func TestParseCreationBlockVideo(t *testing.T) {
	raw := json.RawMessage(`{"creation_block":{"creations":[{
		"type":2,"id":"v1","video":{"status":1,"key":"tos/v.mp4","url":"https://example.invalid/v.mp4",
		"cover":{"url":"https://example.invalid/poster.png","width":1280,"height":720}}
	}]}}`)
	assets := parseCreationBlock(raw)
	if len(assets) != 1 {
		t.Fatalf("got %d assets, want 1", len(assets))
	}
	if !assets[0].isVideo() {
		t.Error("should be a video")
	}
	if assets[0].URL != "https://example.invalid/v.mp4" {
		t.Errorf("url = %q", assets[0].URL)
	}
}

func TestMergeAssetsPrefersCompleteFrame(t *testing.T) {
	// The same creation is reported repeatedly as it progresses; the later frame
	// adds the key and URL without repeating the model.
	first := []creationAsset{{ID: "x", Type: 1, Model: "Seedream 5.0 Flash", Width: 2048, Height: 2048}}
	second := []creationAsset{{ID: "x", Type: 1, Key: "tos/rc_gen_image/a.jpeg", URL: "https://img/a.png"}}

	merged := mergeAssets(first, second)
	if len(merged) != 1 {
		t.Fatalf("got %d assets, want 1 (same id must merge)", len(merged))
	}
	if merged[0].Key == "" || merged[0].URL == "" {
		t.Error("lost the populated fields")
	}
	if merged[0].Model != "Seedream 5.0 Flash" {
		t.Error("lost the model from the earlier frame")
	}
}

func TestMessageContentBodyTextOf(t *testing.T) {
	body := &messageContentBody{ContentBlock: []responseBlock{
		{BlockType: blockTypeText, Content: json.RawMessage(`{"text_block":{"text":"你"}}`)},
		{BlockType: blockTypeDeepThink, Content: json.RawMessage(`{"text_block":{"text":"思考"}}`)},
		{BlockType: blockTypeText, Content: json.RawMessage(`{"text_block":{"text":"好"}}`)},
	}}
	if got := body.textOf(); got != "你好" {
		t.Errorf("textOf = %q, want %q", got, "你好")
	}
}

func TestMessageContentBodyIgnoresEmptyTextBlock(t *testing.T) {
	// A patch that only keeps the block id alive carries an empty text_block.
	// It must not be treated as content.
	body := &messageContentBody{ContentBlock: []responseBlock{
		{BlockType: blockTypeText, Content: json.RawMessage(`{"text_block":{}}`)},
	}}
	if got := body.textOf(); got != "" {
		t.Errorf("textOf = %q, want empty", got)
	}
}

func TestDescribeAssets(t *testing.T) {
	got := describeAssets([]creationAsset{{Type: 1, Model: "Seedream 5.0 Flash", Width: 2048, Height: 2048}})
	if !strings.Contains(got, "已生成图片") || !strings.Contains(got, "2048×2048") {
		t.Errorf("describeAssets = %q", got)
	}
	if describeAssets(nil) != "" {
		t.Error("nil assets should describe to empty")
	}
}

func TestBuildCatalogueExposesBothRealms(t *testing.T) {
	state.settings.when = defaultSettings()
	ids := map[string]bool{}
	for _, m := range buildCatalogue() {
		ids[m.ID] = true
	}
	for _, want := range []string{"doubao/default", "doubao/image", "dola/default", "dola/image", "default"} {
		if !ids[want] {
			t.Errorf("catalogue missing %q", want)
		}
	}
}

func TestBuildCatalogueSingleRealmWhenNotExposed(t *testing.T) {
	s := defaultSettings()
	s.ExposeModels = boolPtr(false)
	s.RealmDefault = realmDola
	state.settings.when = s
	defer func() { state.settings.when = defaultSettings() }()

	for _, m := range buildCatalogue() {
		if strings.HasPrefix(m.ID, "doubao/") {
			t.Errorf("doubao model leaked while expose_models is off: %s", m.ID)
		}
	}
}

func TestStatusCodeHint(t *testing.T) {
	if statusCodeHint(0, "") != "" {
		t.Error("ok status should have no hint")
	}
	// These two are the ones an operator actually hits.
	if !strings.Contains(statusCodeHint(710012001, ""), "重新") {
		t.Error("LoginInvalid should tell the operator to re-authorise")
	}
	if !strings.Contains(statusCodeHint(712012002, ""), "encoding=utf-8") {
		t.Error("the encoding error should name the required Content-Type")
	}
}

func TestStripCookiePrefix(t *testing.T) {
	cases := map[string]string{
		"sessionid=a":         "sessionid=a",
		"Cookie: sessionid=a": "sessionid=a",
		"cookie:sessionid=a":  "sessionid=a",
		`"sessionid=a"`:       "sessionid=a",
	}
	for in, want := range cases {
		if got := stripCookiePrefix(in); got != want {
			t.Errorf("stripCookiePrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeID(t *testing.T) {
	if got := sanitizeID("doubao-MS4wLjABAAAA/zfr+RF=="); strings.ContainsAny(got, "/+=") {
		t.Errorf("sanitizeID left unsafe characters: %q", got)
	}
	if got := sanitizeID(""); got == "" {
		t.Error("sanitizeID should never return empty")
	}
}

func TestNormalizeResourceKey(t *testing.T) {
	in := "https://p3-flow-imagex-sign.byteimg.com/tos-cn-i-a9rns2rl98/rc_gen_image/abc.jpeg"
	got := normalizeResourceKey(in)
	if got != "rc_gen_image/abc.jpeg" {
		t.Errorf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Auth flow contract
// ---------------------------------------------------------------------------

// TestOAuthStateSatisfiesCPA pins the state format against CPA's validator.
//
// CPA's ValidateOAuthState allows only [A-Za-z0-9._-]. An earlier version of
// this plugin joined the realm with ':', which CPA rejected with
// "invalid oauth state" before the login flow could even begin.
func TestOAuthStateSatisfiesCPA(t *testing.T) {
	raw, err := authLoginStart([]byte(`{"provider":"doubao"}`))
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Result struct {
			URL   string `json:"url"`
			State string `json:"state"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Result.State == "" {
		t.Fatal("state 为空，CPA 会报 invalid oauth state")
	}
	if env.Result.URL == "" {
		t.Fatal("未返回登录 URL")
	}
	for _, r := range env.Result.State {
		allowed := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.'
		if !allowed {
			t.Fatalf("state 含 CPA 不接受的字符 %q: %s", r, env.Result.State)
		}
	}
}

// TestOAuthPollRejectsGarbageCookie pins the fix for a bug that let any
// syntactically plausible cookie authorise successfully.
//
// The upstream answers code 0 to an unauthenticated /alice/user/launch, so a
// check on the code alone accepts garbage. The account handle is the real
// signal.
func TestOAuthPollRejectsGarbageCookie(t *testing.T) {
	creds := &credentials{Realm: realmDoubao, Cookies: "sessionid=not-a-session; flow_cur_user_sec_id=also-fake"}
	result, err := probeCredential(creds.withDefaults())
	if err == nil {
		t.Fatalf("无效 Cookie 通过了校验（返回 %v）——任意乱填都能授权成功", result != nil)
	}
}

// ---------------------------------------------------------------------------
// Panel routing
// ---------------------------------------------------------------------------

// TestPanelRoutes pins the paths the host resolves and the fallback behaviour.
func TestPanelRoutes(t *testing.T) {
	// The host may hand over the full path or the tail; both must land.
	for _, path := range []string{"/", "/panel", "/home", "/v0/resource/plugins/doubao", "/v0/resource/plugins/doubao/panel"} {
		resp := handlePanelRoute(pluginapi.ManagementRequest{Method: "GET", Path: path})
		if resp.StatusCode != 200 {
			t.Errorf("GET %s -> %d", path, resp.StatusCode)
		}
		if ct := resp.Headers.Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Errorf("GET %s Content-Type = %q", path, ct)
		}
	}

	// An unknown path must not 404 the iframe.
	resp := handlePanelRoute(pluginapi.ManagementRequest{Method: "GET", Path: "/does-not-exist"})
	if resp.StatusCode != 200 || !strings.Contains(string(resp.Body), "<!doctype html>") {
		t.Errorf("未知路径没有回退到页面: %d", resp.StatusCode)
	}

	// A query string must not defeat the match.
	resp = handlePanelRoute(pluginapi.ManagementRequest{Method: "GET", Path: "/status?x=1"})
	if ct := resp.Headers.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("/status?x=1 Content-Type = %q", ct)
	}
}

// TestStatusDocumentIsValidJSON guards the machine-readable surface.
func TestStatusDocumentIsValidJSON(t *testing.T) {
	var out statusSnapshot
	if err := json.Unmarshal(statusJSON(), &out); err != nil {
		t.Fatalf("status 不是合法 JSON: %v", err)
	}
	if out.Plugin != pluginName || out.Version != pluginVersion {
		t.Errorf("status 身份字段错误: %+v", out)
	}
	if len(out.Realms) != len(allRealms) {
		t.Errorf("上游数量 = %d, 期望 %d", len(out.Realms), len(allRealms))
	}
	if len(out.Models) == 0 {
		t.Error("模型列表为空")
	}
}

// TestHTMLNeverInterpolatesUnescaped pins the escaping helper the pages depend on.
func TestHTMLNeverInterpolatesUnescaped(t *testing.T) {
	got := htmlEscape(`<script>alert("x")</script>&`)
	if strings.Contains(got, "<script>") || strings.Contains(got, `"`) {
		t.Errorf("未转义: %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("转义结果异常: %s", got)
	}
}

// ---------------------------------------------------------------------------
// Settings persistence
// ---------------------------------------------------------------------------

// TestSettingsPersistAcrossReload pins the storage path.
//
// CPA owns config.yaml and exposes no callback that rewrites it, so a panel change
// is stored by the plugin. Without this the setting would appear to save and then
// revert on the next start.
func TestSettingsPersistAcrossReload(t *testing.T) {
	state.settings.set(defaultSettings())
	defer state.settings.set(defaultSettings())

	// Point the state file at a scratch directory for the duration of the test.
	orig := pluginStateDir
	dir := t.TempDir()
	pluginStateDir = func() string { return dir }
	defer func() { pluginStateDir = orig }()

	// Save a change through the same path the panel uses.
	body, _ := json.Marshal(map[string]any{"realm_default": "dola", "media_poll_seconds": 900})
	resp := handlePanelRoute(pluginapi.ManagementRequest{Method: "POST", Path: "/settings", Body: body})
	if resp.StatusCode != 200 {
		t.Fatalf("保存失败: %d %s", resp.StatusCode, resp.Body)
	}
	var out struct {
		Persisted bool `json:"persisted"`
	}
	json.Unmarshal(resp.Body, &out)
	if !out.Persisted {
		t.Fatalf("未持久化: %s", resp.Body)
	}

	// A fresh load from a config that does not mention these keys must pick the
	// saved values back up.
	next := defaultSettings()
	overlayPanelState(&next)
	if next.RealmDefault != realmDola {
		t.Errorf("realm 未恢复: %q", next.RealmDefault)
	}
	if next.MediaPollSeconds != 900 {
		t.Errorf("media_poll_seconds 未恢复: %d", next.MediaPollSeconds)
	}

	// An explicit config value must not be silently overwritten by a stale file
	// for keys the overlay does not carry.
	explicit := defaultSettings()
	explicit.RequestTimeoutSeconds = 45
	overlayPanelState(&explicit)
	if explicit.RequestTimeoutSeconds != 45 {
		t.Errorf("overlay 覆盖了未保存的字段: %d", explicit.RequestTimeoutSeconds)
	}
}

// TestPanelStatePathIsOutsideConfigDir guards the "do not touch CPA's files" rule.
func TestPanelStatePathIsOutsideConfigDir(t *testing.T) {
	p := panelStatePath()
	if !strings.HasSuffix(p, "settings.json") {
		t.Errorf("状态文件路径异常: %s", p)
	}
	if strings.Contains(p, "config.yaml") {
		t.Errorf("状态文件不得写入 CPA 的 config.yaml: %s", p)
	}
}

// ---------------------------------------------------------------------------
// Account isolation
// ---------------------------------------------------------------------------

// TestAuthEntryFilter pins which host entries this plugin claims.
//
// Getting this wrong is silent in both directions: too narrow and the panel shows no
// accounts, too wide and another provider's credential is rewritten by the realm
// switch.
func TestAuthEntryFilter(t *testing.T) {
	cases := []struct {
		name string
		e    pluginapi.HostAuthFileEntry
		want bool
	}{
		{"provider doubao", pluginapi.HostAuthFileEntry{Provider: "doubao"}, true},
		{"provider dola", pluginapi.HostAuthFileEntry{Provider: "dola"}, true},
		{"type doubao", pluginapi.HostAuthFileEntry{Type: "doubao"}, true},
		{"name prefix", pluginapi.HostAuthFileEntry{Name: "doubao-test.json"}, true},
		{"dola name prefix", pluginapi.HostAuthFileEntry{Name: "dola-x.json"}, true},
		{"label cn", pluginapi.HostAuthFileEntry{Label: "豆包 · 测试"}, true},
		{"label intl", pluginapi.HostAuthFileEntry{Label: "Dola · test"}, true},
		{"other provider", pluginapi.HostAuthFileEntry{Provider: "codebuddy", Type: "codebuddy", Name: "codebuddy-x.json"}, false},
		{"empty", pluginapi.HostAuthFileEntry{}, false},
	}
	for _, c := range cases {
		if got := isOurAuthEntry(c.e); got != c.want {
			t.Errorf("%s: isOurAuthEntry = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestRealmIsolationTracksOnlyItsOwnChanges pins the tracking rule.
//
// The switch must re-enable only the accounts it disabled itself; an account the
// operator turned off by hand has to stay off across a switch back.
func TestRealmIsolationTracksOnlyItsOwnChanges(t *testing.T) {
	r := &realmIsolation{}
	bySwitch := hostAuthEntry{AuthIndex: "idx-a", Path: "/auth/doubao-a.json"}
	byHand := hostAuthEntry{AuthIndex: "idx-b", Path: "/auth/doubao-b.json"}

	r.track(bySwitch)
	if !r.isTracked(bySwitch) {
		t.Fatal("switch-disabled account not tracked")
	}
	if r.isTracked(byHand) {
		t.Fatal("hand-disabled account should not be tracked")
	}

	r.untrack(bySwitch)
	if r.isTracked(bySwitch) {
		t.Fatal("untrack did not clear the entry")
	}
}

// TestAccountIsolationSurvivesMissingPath pins that tracking falls back to the auth
// index when a path is not reported, which is the common case from host.auth.list.
func TestAccountIsolationSurvivesMissingPath(t *testing.T) {
	r := &realmIsolation{}
	e := hostAuthEntry{AuthIndex: "only-index"}
	r.track(e)
	if !r.isTracked(e) {
		t.Fatal("entry without a path was not tracked")
	}
}

// ---------------------------------------------------------------------------
// Callback-box flow
// ---------------------------------------------------------------------------

// TestOAuthCallbackPath pins the file name CPA writes the submitted value to.
//
// The whole cookie flow depends on this path: CPA's management UI labels the box as
// an OAuth code field, the user pastes a Doubao cookie into it, and the plugin finds
// it here. A wrong name means the poll never sees the value and the UI spins forever.
func TestOAuthCallbackPath(t *testing.T) {
	dir := t.TempDir()
	state := "doubao-2f3a1b4c-8d9e-4f2a-b1c3-4d5e6f7a8b9c"

	got := filepath.Join(dir, ".oauth-doubao-"+state+".oauth")
	if want := oauthCallbackPath(state, dir); want != got {
		t.Fatalf("路径不符\n got: %s\nwant: %s", want, got)
	}

	// The provider that CPA interpolates is the plugin name.
	if !strings.Contains(oauthCallbackPath(state, dir), ".oauth-"+pluginName+"-") {
		t.Errorf("路径未使用插件名作为 provider: %s", oauthCallbackPath(state, dir))
	}

	// A state with a path separator must be refused rather than escaping the dir.
	// ValidateOAuthState rejects it upstream, but this function builds a file path
	// from the value, so it re-checks.
	if p := oauthCallbackPath("../../etc/passwd", dir); p != "" {
		t.Fatalf("带路径分隔符的 state 未被拒绝: %s", p)
	}
	if p := oauthCallbackPath(state, ""); p != "" {
		t.Fatalf("空 authDir 应返回空路径: %s", p)
	}
}

// TestReadOAuthCallbackCode covers the handoff the flow now depends on.
func TestReadOAuthCallbackCode(t *testing.T) {
	dir := t.TempDir()
	state := "doubao-abc-123"
	path := oauthCallbackPath(state, dir)
	if path == "" {
		t.Fatal("callback 路径为空")
	}

	// Nothing submitted yet: the poll must report pending, not an error.
	if got := readOAuthCallbackCode(state, dir); got != "" {
		t.Fatalf("未提交时应为空，得到 %q", got)
	}

	// A submitted value is what the user pasted into the box.
	const cookie = "sessionid=abc; flow_cur_user_sec_id=xyz"
	payload, _ := json.Marshal(map[string]string{"code": cookie, "state": state})
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readOAuthCallbackCode(state, dir); got != cookie {
		t.Fatalf("读回不符\n got: %q\nwant: %q", got, cookie)
	}

	// Consuming it stops a retry from re-importing the same credential.
	consumeOAuthCallback(state, dir)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("consume 未删除回调文件")
	}

	// An error payload must not be mistaken for a credential.
	errPayload, _ := json.Marshal(map[string]string{"state": state, "error": "access_denied"})
	os.WriteFile(path, errPayload, 0o600)
	if got := readOAuthCallbackCode(state, dir); got != "" {
		t.Fatalf("错误载荷被当作凭据: %q", got)
	}
}

// TestVersionMatchesRegistry pins pluginVersion to registry.json.
//
// The two drifted for nine releases: the published registry reached 0.2.2 while the
// compiled constant still said 0.1.0, so the host logged builds under a version that
// was never released and the panel displayed it. Nothing failed — the mismatch was
// only visible by reading the host's log against the release list. This test makes it
// a build error instead.
func TestVersionMatchesRegistry(t *testing.T) {
	raw, errRead := os.ReadFile("registry.json")
	if errRead != nil {
		t.Skipf("registry.json 不可读: %v", errRead)
	}
	var reg struct {
		Plugins []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"plugins"`
	}
	if errUnmarshal := json.Unmarshal(raw, &reg); errUnmarshal != nil {
		t.Fatalf("registry.json 解析失败: %v", errUnmarshal)
	}
	for _, p := range reg.Plugins {
		if p.ID != pluginName {
			continue
		}
		if p.Version != pluginVersion {
			t.Fatalf("版本号不一致：registry.json 是 %q，rpc.go 是 %q\n"+
				"两者必须相同，否则宿主记录的版本与实际发布的不符。",
				p.Version, pluginVersion)
		}
		return
	}
	t.Fatalf("registry.json 中没有 %q 插件", pluginName)
}
