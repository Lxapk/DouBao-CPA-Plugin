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
	b.WriteString(`<p class="desc">用手机号接收短信验证码。登录成功后这个页面会提示完成，` +
		`回到 CPA 的授权窗口点确认即可。</p></header>`)
	b.WriteString(`<main class="main">`)

	b.WriteString(`<div class="box"><header><h3>手机号登录</h3>`)
	b.WriteString(`<span class="grow"></span><span class="note" id="loginMsg"></span></header>`)
	b.WriteString(`<div class="pad">`)

	b.WriteString(`<div class="row" style="margin-bottom:12px">`)
	b.WriteString(`<span class="note">站点：` + realmLabel(r) + `</span>`)
	b.WriteString(`<span class="note">` + htmlEscape(profile.Host) + `</span>`)
	b.WriteString(`</div>`)

	b.WriteString(`<div class="note" style="margin-bottom:6px">手机号</div>`)
	b.WriteString(`<div class="row">`)
	b.WriteString(`<input type="tel" id="phone" placeholder="例如 13800138000" autocomplete="tel" style="flex:1 1 220px">`)
	b.WriteString(`<span class="btn-end"><button type="button" id="sendBtn" data-call="sendCode">获取验证码</button></span>`)
	b.WriteString(`</div>`)

	b.WriteString(`<div class="note" style="margin:14px 0 6px">验证码</div>`)
	b.WriteString(`<div class="row">`)
	b.WriteString(`<input type="text" id="code" inputmode="numeric" maxlength="8" placeholder="短信里的 6 位数字" style="flex:1 1 220px">`)
	b.WriteString(`<span class="btn-end"><button type="button" class="primary" id="loginBtn" data-call="submitLogin">登录</button></span>`)
	b.WriteString(`</div>`)

	b.WriteString(`<div id="loginResult" style="margin-top:14px"></div>`)

	b.WriteString(`<div class="group-head" style="margin:20px 0 8px"><h2>说明</h2></div>`)
	for _, line := range []string{
		"验证码由豆包下发，插件只负责提交，不会保存你的短信内容。",
		"登录成功后插件拿到的是与网页端相同的会话，不会多出额外权限。",
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

  // Submissions go to the management routes, not the resource route the page
  // itself was served from: CPA's resource handler only accepts GET (anything
  // else 404s), and these two calls must be POSTs.
  var MGMT = "/v0/management/` + pluginName + `";
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
          throw new Error((data && (data.error || data.message)) || ("HTTP " + resp.status));
        }
        return data;
      });
    });
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
    post("/login/send", { state: STATE, phone: p }).then(function (data) {
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
    post("/login/verify", { state: STATE, phone: p, code: c }).then(function (data) {
      if (data && data.ok) {
        note("登录成功", "ok");
        result("<div><b>登录成功</b></div><div class=\"note\">" +
          esc(data.message || "回到 CPA 的授权窗口点确认即可。") + "</div>", "ok");
        var phoneEl = document.getElementById("phone");
        var codeEl = document.getElementById("code");
        if (phoneEl) phoneEl.disabled = true;
        if (codeEl) codeEl.disabled = true;
      } else {
        note((data && data.message) || "登录失败", "bad");
        result(esc((data && data.message) || "登录失败"), "bad");
      }
    }).catch(function (e) {
      note(e.message, "bad");
      result(esc(e.message), "bad");
    }).then(function () {
      if (btn) { btn.disabled = false; btn.textContent = "登录"; }
    });
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

	r := realmForLoginState(req.State)
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

	r := realmForLoginState(req.State)
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
	if pending := pendingLogins.peek(strings.TrimSpace(stateValue)); pending != nil && pending.Realm != "" {
		return pending.Realm
	}
	if r := state.settings.get().RealmDefault; r != "" {
		return r
	}
	return realmDoubao
}
