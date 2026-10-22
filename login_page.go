package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// 登录页：手机号 + 短信验证码。
//
// 这一页由插件自己提供，而不是复用 CPA 的 OAuth 弹窗。原因是那个弹窗只能收
// 一个值（回调 URL 或授权码），而豆包登录需要手机号与验证码两步，并且发码和
// 校验是两次请求。tinker 插件也是同样的取舍。
//
// 页面挂在 CPA 的资源路由下（无需管理密钥），提交时直接调用本插件的接口，
// 登录成功后把结果挂回 pending logins，CPA 下一次轮询就能拿到凭据。

// renderLoginPage 生成登录页。
//
// state 原样放进页面，用来把这次提交绑到发起登录的那个会话上。
func renderLoginPage(state string) string {
	r := realmForLoginState(state)
	profile := profileFor(r)

	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	b.WriteString(`<title>` + pluginDisplayName + ` 登录</title><style>` + uiCSS + `</style></head><body>`)
	b.WriteString(`<div class="shell"><header class="page-header">`)
	b.WriteString(`<h1>` + pluginDisplayName + ` 登录</h1>`)
	b.WriteString(`<p class="desc">用手机号接收短信验证码。登录成功后账号即刻写入，` +
		`可以回到控制台继续，也可以在这里接着登录下一个账号。</p></header>`)
	b.WriteString(`<main class="main">`)

	b.WriteString(`<div class="box"><header><h3>手机号登录</h3>`)
	b.WriteString(`<span class="grow"></span><span class="note" id="loginMsg"></span></header>`)
	b.WriteString(`<div class="pad">`)

	// 登录前先选国际版还是国内版。
	//
	// 这两个上游的账号互不通用，选错会得到「区域不可用」；而用户打开登录页时
	// 常常还没想好要加哪一侧，所以开关放在页面里，而不是只能靠面板里预设。
	b.WriteString(`<div class="note" style="margin-bottom:7px">登录到哪个上游</div>`)
	b.WriteString(`<div class="seg" id="loginRealmSeg">`)
	for _, each := range allRealms {
		cls := ""
		if each == r {
			cls = "on"
		}
		item := profileFor(each)
		b.WriteString(`<button type="button" class="` + cls + `"` +
			` data-value="` + string(each) + `"` +
			` data-call="pickLoginRealm" data-arg0="` + string(each) + `"` +
			` title="` + htmlEscape(item.Host) + `">` + realmLabel(each) + `</button>`)
	}
	b.WriteString(`</div>`)
	b.WriteString(`<div class="note" id="loginSite" style="margin:8px 0 14px">` +
		htmlEscape(profile.Host) + `</div>`)
	b.WriteString(`<div class="note" style="margin:-8px 0 14px">` +
		`两侧账号互不通用：豆包账号不能用于 Dola，反之亦然。</div>`)

	b.WriteString(`<div class="note" style="margin-bottom:6px">手机号</div>`)
	b.WriteString(`<div class="row">`)
	b.WriteString(`<input type="tel" id="phone" placeholder="例如 13800138000" autocomplete="tel" style="flex:1 1 220px">`)
	b.WriteString(`<span class="btn-end"><button type="button" class="secondary" id="sendBtn" data-call="sendCode">获取验证码</button></span>`)
	b.WriteString(`</div>`)

	b.WriteString(`<div class="note" style="margin:14px 0 6px">验证码</div>`)
	b.WriteString(`<div class="row">`)
	b.WriteString(`<input type="text" id="code" inputmode="numeric" maxlength="8" placeholder="短信里的 6 位数字" style="flex:1 1 220px">`)
	b.WriteString(`<span class="btn-end"><button type="button" class="primary" id="loginBtn" data-call="submitLogin">登录</button></span>`)
	b.WriteString(`</div>`)

	b.WriteString(`<div id="loginResult" style="margin-top:14px"></div>`)

	// 登录完成后才显示：一个原地重开一次登录，一个回到控制台。
	// 没有这两个出口，用户登录完只能自己关标签页，也不知道账号到底进去没有。
	b.WriteString(`<div class="row" id="afterLogin" style="margin-top:14px;gap:8px" hidden>`)
	b.WriteString(`<button type="button" class="primary" data-call="backToPanel">回到控制台</button>`)
	b.WriteString(`<button type="button" data-call="loginAgain">继续登录另一个账号</button>`)
	b.WriteString(`</div>`)

	b.WriteString(`<div class="group-head" style="margin:20px 0 8px"><h2>说明</h2></div>`)
	for _, line := range []string{
		"验证码由所选上游下发，插件只负责提交，不会保存你的短信内容。",
		"登录成功后插件拿到的是与网页端相同的会话，不会多出额外权限。",
		"国内版与国际版的账号互不通用：给 Dola 加账号就选 Dola，别用豆包的手机号试。",
		"如果提示「请求太频繁 / Maximum number of attempts」，那是上游按 IP 计数的防刷限制，等 10–30 分钟再点获取验证码，期间别反复试。",
		"会话到期后需要重新登录；同一账号重复登录会覆盖旧记录，不会产生重复账号。",
	} {
		b.WriteString(`<div class="note" style="margin:5px 0;line-height:1.7">· ` + line + `</div>`)
	}

	b.WriteString(`</div></div></main></div>`)
	b.WriteString(`<div id="toasts"></div>`)
	b.WriteString(loginPageScript(state))
	b.WriteString(`</body></html>`)
	return b.String()
}

// loginPageScript drives the two-step form.
//
// 提交走本插件的资源路径而不是管理 API：登录页本身是在没有管理密钥的情况下
// 打开的，要求用户先填密钥就本末倒置了。
func loginPageScript(state string) string {
	stateJSON, _ := json.Marshal(state)
	return `<script>
(function () {
  "use strict";
  var STATE = ` + string(stateJSON) + `;
  var SITE_MAP = ` + siteMapJSON() + `;

  /* ---------- theme ---------- */

  // 跟随宿主配色。
  //
  // 登录页是被 window.open 打开的新标签页，没有父窗口可读，只能靠 URL 上的
  // theme 参数；独立打开时两者都没有，就移除属性让 media query 决定，
  // 也就是「跟随系统」。不复制这段的话，无论宿主选哪套配色，登录页都停在
  // 默认的暗色上。
  function adoptHostTheme() {
    var theme = "";
    try {
      if (window.parent && window.parent !== window && window.parent.document) {
        theme = window.parent.document.documentElement.getAttribute("data-theme") || "";
      }
    } catch (e) {
      theme = "";
    }
    if (!theme) {
      try { theme = new URLSearchParams(location.search).get("theme") || ""; } catch (e) { theme = ""; }
    }
    var root = document.documentElement;
    if (theme) root.setAttribute("data-theme", theme);
    else root.removeAttribute("data-theme");
  }

  adoptHostTheme();

  // Submissions go to the management routes, not the resource route the page
  // itself was served from: CPA's resource handler only accepts GET (anything
  // else 404s), and these two calls must be POSTs.
  var MGMT = "/v0/management/` + pluginName + `";
  var PANEL_PATH = "/v0/resource/plugins/` + pluginName + `/panel";
  var PANEL_KEY = "cpa-management-key";

  function managementKey() {
    var names = [PANEL_KEY, "doubao.mgmtKey"];
    for (var i = 0; i < names.length; i++) {
      try {
        var v = (localStorage.getItem(names[i]) || "").trim();
        if (v) return v;
      } catch (e) {}
    }
    return "";
  }

  function note(text, kind) {
    var el = document.getElementById("loginMsg");
    if (!el) return;
    el.textContent = text || "";
    el.className = "note" + (kind ? " " + kind : "");
  }

  function result(html, kind) {
    var el = document.getElementById("loginResult");
    if (!el) return;
    el.innerHTML = html ? '<div class="toast ' + (kind || "") + '" style="max-width:none">' + html + "</div>" : "";
  }

  function post(path, body) {
    var k = managementKey();
    if (!k) {
      return Promise.reject(new Error("请先在插件「设置」里保存 CPA 管理密钥，登录页需要它才能提交"));
    }
    return fetch(MGMT + path, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Authorization": "Bearer " + k,
        "X-Management-Key": k
      },
      body: JSON.stringify(body)
    }).then(function (resp) {
      return resp.text().then(function (text) {
        var data = null;
        try { data = text ? JSON.parse(text) : null; } catch (e) { data = null; }
        if (!resp.ok) {
          // 把能拿到的信息都带上：只显示一句 "error" 时，用户和排查者都
          // 无从判断是上游拒绝、参数不对，还是网关拦了。
          var msg = (data && (data.error || data.message)) || "";
          msg = String(msg).trim();
          if (!msg || msg === "error") {
            msg = "请求失败（HTTP " + resp.status + "）";
            var snippet = String(text || "").trim();
            if (snippet) msg += "：" + snippet.slice(0, 200);
          }
          var err = new Error(msg);
          err.status = resp.status;
          err.body = text;
          throw err;
        }
        return data;
      });
    });
  }

  // 当前选中的上游。提交时随请求带上，服务端据此选账号中心与 aid ——
  // 这两个值两侧不同，只靠 URL 上的 realm 会让切换按钮形同虚设。
  var REALM = STATE.split("-")[0] || "doubao";

  function currentRealm() {
    return REALM;
  }

  var SITES = SITE_MAP;

  function pickLoginRealm(value) {
    if (!value) return;
    REALM = value;
    var seg = document.getElementById("loginRealmSeg");
    if (seg) {
      var btns = seg.querySelectorAll("button");
      for (var i = 0; i < btns.length; i++) {
        var on = btns[i].getAttribute("data-value") === value;
        btns[i].className = on ? "on" : "";
      }
    }
    var site = document.getElementById("loginSite");
    if (site && SITES[value]) site.textContent = SITES[value];
    // 换了上游，之前发的验证码就不作数了，清掉避免误用。
    var code = document.getElementById("code");
    if (code) code.value = "";
    note("");
    result("");
    showAfterLogin(false);
  }

  function phoneValue() {
    var el = document.getElementById("phone");
    return ((el && el.value) || "").replace(/[\s-]/g, "");
  }

  function codeValue() {
    var el = document.getElementById("code");
    return ((el && el.value) || "").replace(/\s/g, "");
  }

  function sendCode() {
    var p = phoneValue();
    if (!/^1\d{10}$/.test(p)) { note("请填写 11 位手机号", "bad"); return; }
    var btn = document.getElementById("sendBtn");
    if (btn) { btn.disabled = true; btn.textContent = "发送中…"; }
    note("正在发送…");
    post("/login/send", { state: STATE, phone: p, realm: currentRealm() }).then(function (data) {
      if (data && data.ok) {
        note("验证码已发送", "ok");
        var input = document.getElementById("code");
        if (input) input.focus();
      } else {
        note((data && data.message) || "发送失败", "bad");
      }
    }).catch(function (e) {
      note(e.message, "bad");
    }).then(function () {
      if (btn) { btn.disabled = false; btn.textContent = "获取验证码"; }
    });
  }

  function submitLogin() {
    var p = phoneValue(), c = codeValue();
    if (!/^1\d{10}$/.test(p)) { note("请填写 11 位手机号", "bad"); return; }
    if (!/^\d{4,8}$/.test(c)) { note("请填写短信验证码", "bad"); return; }
    var btn = document.getElementById("loginBtn");
    if (btn) { btn.disabled = true; btn.textContent = "登录中…"; }
    note("正在登录…");
    post("/login/verify", { state: STATE, phone: p, code: c, realm: currentRealm() }).then(function (data) {
      if (data && data.ok) {
        note("登录成功", "ok");
        var lines = ["<div><b>登录成功</b></div>"];
        if (data.label) lines.push("<div class=\"note\">账号：" + esc(data.label) + "</div>");
        if (data.realm) lines.push("<div class=\"note\">上游：" + esc(data.realm) + "</div>");
        lines.push("<div class=\"note\">" + esc(data.message || "账号已写入，可以直接使用。") + "</div>");
        lines.push("<div class=\"note\">下面可以回到控制台，或继续登录另一个账号。</div>");
        result(lines.join(""), "ok");

        var phoneEl = document.getElementById("phone");
        var codeEl = document.getElementById("code");
        if (phoneEl) phoneEl.disabled = true;
        if (codeEl) codeEl.disabled = true;
        if (btn) { btn.disabled = true; btn.textContent = "已登录"; }
        showAfterLogin(true);
        // 通知发起这次登录的控制台刷新账号列表。控制台是另一个窗口，
        // 不通知的话它要等用户手动点刷新才看得到新账号。
        notifyParent();
      } else {
        note((data && data.message) || "登录失败", "bad");
        result(esc((data && data.message) || "登录失败"), "bad");
      }
    }).catch(function (e) {
      note(e.message, "bad");
      result(esc(e.message), "bad");
    }).then(function () {
      // 成功时按钮已经改成「已登录」，这里只恢复失败的情况。
      if (btn && !btn.disabled) { btn.textContent = "登录"; }
    });
  }

  /* ---------- after login ---------- */

  function showAfterLogin(show) {
    var el = document.getElementById("afterLogin");
    if (el) el.hidden = !show;
  }

  // 控制台如果是从本页打开的，就通知它刷新；跨源或没有 opener 时静默跳过。
  function notifyParent() {
    try {
      if (window.opener && !window.opener.closed && window.opener.__doubaoLoginDone) {
        window.opener.__doubaoLoginDone();
      }
    } catch (e) { /* 跨源时读不到，忽略 */ }
  }

  function backToPanel() {
    // 从控制台打开的就关掉自己，让控制台回到前台；直接打开时跳到控制台。
    try {
      if (window.opener && !window.opener.closed) { window.close(); return; }
    } catch (e) { /* 跨源读不到 opener，走跳转 */ }
    location.href = PANEL_PATH;
  }

  function loginAgain() {
    // 原地重开一次登录：清空输入、解禁控件、清掉结果，重新发码。
    // 换 state 是不必要的 —— 上一笔已经 consum 掉了，这里继续用同一个会话
    // 只为让 realm 判断保持同一个上游。
    var phoneEl = document.getElementById("phone");
    var codeEl = document.getElementById("code");
    var btn = document.getElementById("loginBtn");
    if (phoneEl) { phoneEl.disabled = false; phoneEl.value = ""; phoneEl.focus(); }
    if (codeEl) { codeEl.disabled = false; codeEl.value = ""; }
    if (btn) { btn.disabled = false; btn.textContent = "登录"; }
    result("");
    note("");
    showAfterLogin(false);
  }

  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#39;" }[c];
    });
  }

  // 管理密钥是提交的前提：CPA 的管理路由要求 Bearer 鉴权，而登录页是被
  // 新标签页打开的，拿不到父窗口的会话，只能读 localStorage 里已保存的密钥。
  function checkKey() {
    if (managementKey()) return;
    note("请先在插件「设置」里保存 CPA 管理密钥，否则无法提交", "bad");
  }

  checkKey();

  document.addEventListener("click", function (ev) {
    var el = ev.target;
    while (el && el !== document) {
      var action = el.dataset ? el.dataset.call : null;
      if (action === "sendCode") { ev.preventDefault(); sendCode(); return; }
      if (action === "submitLogin") { ev.preventDefault(); submitLogin(); return; }
      if (action === "pickLoginRealm") { ev.preventDefault(); pickLoginRealm(el.dataset.arg0); return; }
      if (action === "backToPanel") { ev.preventDefault(); backToPanel(); return; }
      if (action === "loginAgain") { ev.preventDefault(); loginAgain(); return; }
      el = el.parentNode;
    }
  });

  document.addEventListener("keydown", function (ev) {
    if (ev.key === "Enter" && ev.target && ev.target.id === "code") submitLogin();
  });
})();
</script>`
}

// ───────────────────────── 表单接口 ─────────────────────────

// loginFormRequest 是登录页两个接口共用的请求体。
type loginFormRequest struct {
	State string `json:"state"`
	Phone string `json:"phone"`
	Code  string `json:"code"`
	// Realm 是页面上选中的上游。它决定用哪个账号中心与 aid ——
	// 这两个值两侧不同，只靠 state 会让切换按钮不起作用。
	Realm string `json:"realm"`
}

// realmForLoginForm 按请求里选中的上游决定登录去哪一侧。
//
// 优先用页面选的值，其次看登录会话绑定的那一侧，最后退回默认上游。
func realmForLoginForm(req loginFormRequest) realm {
	if r := normalizeRealm(req.Realm); r != "" {
		return r
	}
	return realmForLoginState(req.State)
}

// handleLoginSendCode 发送验证码。
//
// 与 auth.login.poll 里「只给手机号」的分支等价，但这是页面直接调用的路径：
// 用户点「获取验证码」时不必等 CPA 的轮询周期。
func handleLoginSendCode(body []byte) pluginapi.ManagementResponse {
	var req loginFormRequest
	if errUnmarshal := json.Unmarshal(body, &req); errUnmarshal != nil {
		return errorJSON(http.StatusBadRequest, "无法解析请求："+errUnmarshal.Error())
	}
	phone := normalizePhone(req.Phone)
	if phone == "" {
		return errorJSON(http.StatusBadRequest, "请填写 11 位手机号")
	}

	r := realmForLoginForm(req)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if errSend := sendLoginCode(ctx, r, phone); errSend != nil {
		return errorJSON(http.StatusBadGateway, errSend.Error())
	}
	logInfo("doubao: 已向 " + maskPhone(phone) + " 发送登录验证码")
	return jsonResponse(map[string]any{"ok": true, "message": "验证码已发送"})
}

// handleLoginVerify 校验验证码并落库。
//
// 成功后凭据直接写进 auth 目录，账号立刻可用；同时把结果挂到这次登录会话上，
// 让 CPA 的轮询也能收敛成功。
func handleLoginVerify(body []byte) pluginapi.ManagementResponse {
	var req loginFormRequest
	if errUnmarshal := json.Unmarshal(body, &req); errUnmarshal != nil {
		return errorJSON(http.StatusBadRequest, "无法解析请求："+errUnmarshal.Error())
	}
	phone := normalizePhone(req.Phone)
	code := strings.TrimSpace(req.Code)
	if phone == "" {
		return errorJSON(http.StatusBadRequest, "请填写 11 位手机号")
	}
	if code == "" {
		return errorJSON(http.StatusBadRequest, "请填写短信验证码")
	}

	r := realmForLoginForm(req)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cookies, errLogin := loginWithCode(ctx, r, phone, code)
	if errLogin != nil {
		return errorJSON(http.StatusBadRequest, "登录失败："+errLogin.Error())
	}

	creds := (&credentials{Realm: r, Cookies: cookies}).withDefaults()

	// 必须探测成功才算登录完成。短信登录只拿到 sessionid，账号 handle
	// （flow_cur_user_sec_id）要靠 /alice/user/launch 换回来；探测失败意味着
	// 这串 Cookie 调不动 IM 接口，落库只会得到一个永远失败的账号。
	probe, errProbe := probeCredential(creds)
	if errProbe != nil {
		logError("doubao: 短信登录后探测账号失败：" + errProbe.Error())
		return errorJSON(http.StatusBadGateway,
			"登录已通过，但账号信息获取失败："+errProbe.Error()+
				"。请重试一次；若持续失败，说明该账号被风控，需要先在官方客户端登录一次。")
	}
	creds.applyLaunch(probe)
	if creds.Nickname == "" {
		creds.Nickname = maskPhone(phone)
	}

	path, errSave := writeAuthRecord(creds, &launchResult{SecUserID: creds.SecUserID, Nickname: creds.Nickname}, "")
	if errSave != nil {
		logError("doubao: 写入账号文件失败：" + errSave.Error())
	}
	accounts.invalidate()
	logInfo("doubao: 账号 " + creds.label() + " 短信登录成功")

	pendingLogins.complete(req.State, creds)

	out := map[string]any{
		"ok":      true,
		"message": "登录成功，回到 CPA 的授权窗口点确认即可。",
		"label":   creds.label(),
		"realm":   string(r),
	}
	if path != "" {
		out["path"] = path
	}
	return jsonResponse(out)
}

// normalizePhone 归一化手机号：去掉空白、连字符与 +86 前缀。
//
// 只做形状检查，不做归属地判断——号码是否可用由上游回答。
func normalizePhone(raw string) string {
	phone := strings.TrimSpace(raw)
	phone = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(phone)
	phone = strings.TrimPrefix(phone, "+86")
	phone = strings.TrimPrefix(phone, "86")
	if len(phone) != 11 || phone[0] != '1' {
		return ""
	}
	for _, r := range phone {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return phone
}

// realmForLoginState 把登录会话绑定的上游取出来，缺省用默认上游。
func realmForLoginState(stateValue string) realm {
	stateValue = strings.TrimSpace(stateValue)
	if pending := pendingLogins.peek(stateValue); pending != nil && pending.Realm != "" {
		return pending.Realm
	}
	// state 的形状是 "<realm>-<uuid>"（startPanelLogin 生成的）。会话已经过期
	// 或不在内存里时（重启过），前缀仍能说明这一页是为哪一侧打开的，
	// 否则登录页会把 Dola 的页面显示成豆包，用户看到站点不对还以为点错了。
	if idx := strings.IndexByte(stateValue, '-'); idx > 0 {
		if r := normalizeRealm(stateValue[:idx]); r != "" {
			return r
		}
	}
	if r := state.settings.get().RealmDefault; r != "" {
		return r
	}
	return realmDoubao
}

// siteMapJSON 把每个上游的站点地址交给页面，切换时用来更新提示。
func siteMapJSON() string {
	out := map[string]string{}
	for _, r := range allRealms {
		out[string(r)] = profileFor(r).Host
	}
	raw, errMarshal := json.Marshal(out)
	if errMarshal != nil {
		return "{}"
	}
	return string(raw)
}
