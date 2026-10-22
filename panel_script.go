package main

// panelScript returns the panel's behaviour, wrapper included.
//
// Two ideas keep this small:
//
//  1. Every control declares what it does in data attributes — data-call names
//     the action, data-argN carries its arguments — and one delegated click
//     handler dispatches them. Adding a button therefore means adding an entry to
//     actions{}, not another addEventListener.
//  2. Nothing is fetched until the key exists. The management API rejects
//     anonymous requests, so an eager load would only produce a wall of 401s that
//     read like a plugin fault.
func panelScript() string {
	return `<script>
(function () {
  "use strict";

  /* ---------- theme ---------- */

  // Follow the host's theme.
  //
  // CPA writes data-theme="dark" or data-theme="white" on its own <html> and
  // removes the attribute for "follow system". This page is loaded inside that
  // document (or in an iframe of it), so the attribute can be copied across —
  // except when the page is opened in its own tab, where there is no host. Then
  // the attribute is left absent and the media query decides, which is exactly
  // the "follow system" case.
  //
  // Reading a parent document from another origin throws, and the host does not
  // reload the iframe when the operator switches theme, so the check has to be
  // guarded and repeated.
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
    // 原样透传，交给 CSS 决定用哪套配色。
    //
    // 这里不再白名单过滤：host 新增一个主题（black、kraft…）时，写死列表会让
    // 面板悄悄退回暗色。取值为空才是"跟随系统"，此时移除属性让 media query 生效。
    if (theme) root.setAttribute("data-theme", theme);
    else root.removeAttribute("data-theme");
  }

  adoptHostTheme();
  setInterval(adoptHostTheme, 1500);

  /* ---------- storage ---------- */

  // CPA stores its own key under "cpa-management-key"; reuse it so the operator
  // never retypes what the panel already knows.
  var PANEL_KEY = "cpa-management-key";
  var OUR_KEY = "doubao.mgmtKey";
  var VIEW_KEY = "doubao-panel-view";
  var AUTH_REALM_KEY = "doubao-auth-realm";
  // 默认上游，来自 /settings；登录页初值用它。
  var stateDefaultRealm = "doubao";
  var API = "/v0/management";
  // The login form is a resource route: it must be reachable without the
  // management key, because the operator has not necessarily saved one yet.
  var LOGIN_PATH = "/v0/resource/plugins/doubao/login";

  function storageGet(name) {
    try { return localStorage.getItem(name) || ""; } catch (e) { return ""; }
  }

  function storageSet(name, value) {
    try { localStorage.setItem(name, value); return true; } catch (e) { return false; }
  }

  function storageRemove(name) {
    try { localStorage.removeItem(name); } catch (e) {}
  }

  function key() {
    var names = [PANEL_KEY, OUR_KEY];
    for (var i = 0; i < names.length; i++) {
      var v = storageGet(names[i]).trim();
      if (v) return v;
    }
    return "";
  }

  /* ---------- helpers ---------- */

  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#39;" }[c];
    });
  }

  function toast(message, kind) {
    var host = document.getElementById("toasts");
    if (!host) return;
    var el = document.createElement("div");
    el.className = "toast" + (kind ? " " + kind : "");
    el.textContent = message;
    host.appendChild(el);
    setTimeout(function () { el.remove(); }, 3200);
  }

  function note(id, msg, kind) {
    var el = document.getElementById(id);
    if (!el) return;
    el.textContent = msg || "";
    el.className = "note" + (kind ? " " + kind : "");
    if (!msg) return;
    setTimeout(function () {
      if (el.textContent === msg) { el.textContent = ""; el.className = "note"; }
    }, 3600);
  }

  function request(path, options) {
    var k = key();
    if (!k) return Promise.reject(new Error("请先在「设置」里保存管理密钥"));
    var opts = options || {};
    opts.headers = Object.assign({
      "Content-Type": "application/json",
      "Authorization": "Bearer " + k,
      "X-Management-Key": k
    }, opts.headers || {});
    return fetch(API + "/doubao" + path, opts).then(function (resp) {
      return resp.text().then(function (body) {
        var data = null;
        try { data = body ? JSON.parse(body) : null; } catch (e) { data = null; }
        if (!resp.ok) {
          var msg = (data && (data.message || data.error)) || ("HTTP " + resp.status);
          if (resp.status === 401 || resp.status === 403) {
            msg = "未授权：请在「设置」里填入 CPA 管理密钥";
          }
          var err = new Error(String(msg));
          // Carry the parsed body: a rejected credential answers with a
          // structured 400 whose error_hint is the actionable half.
          err.data = data;
          err.status = resp.status;
          throw err;
        }
        return data;
      });
    });
  }

  function write(path, body) {
    return request(path, {
      method: "POST",
      body: JSON.stringify(body || {})
    });
  }

  function reload() { location.reload(); }

  // 登录页在自己被打开时会回调这里，让新账号立刻出现在列表里。
  window.__doubaoLoginDone = function () {
    try {
      showView("view-accounts");
      loadAccounts();
      toast("账号已添加", "ok");
    } catch (e) { /* 页面结构变了就算了，不影响登录本身 */ }
  };

  /* ---------- tabs ---------- */

  function showView(name) {
    name = String(name || "");
    if (name && name.indexOf("view-") !== 0) name = "view-" + name;

    var views = document.querySelectorAll(".view");
    var shown = false;
    for (var i = 0; i < views.length; i++) {
      var match = views[i].id === name;
      views[i].hidden = !match;
      if (match) shown = true;
    }
    // Never leave the page blank: an unknown id falls back to the first page.
    if (!shown && views.length) {
      views[0].hidden = false;
      name = views[0].id;
    }
    var tabs = document.querySelectorAll(".tabbar .tab[data-view]");
    for (var j = 0; j < tabs.length; j++) {
      tabs[j].classList.toggle("on", tabs[j].getAttribute("data-view") === name);
    }
    storageSet(VIEW_KEY, name);
  }

  function restoreView() {
    var saved = storageGet(VIEW_KEY);
    var page = saved ? document.getElementById(saved) : null;
    if (page) { showView(saved); return; }
    var first = document.querySelector(".view");
    if (first) showView(first.id);
  }

  /* ---------- segmented controls ---------- */

  function segSelect(id, value) {
    var seg = document.getElementById(id);
    if (!seg) return;
    var buttons = seg.querySelectorAll("button");
    for (var i = 0; i < buttons.length; i++) {
      buttons[i].classList.toggle("on", buttons[i].getAttribute("data-value") === value);
    }
  }

  function segValue(id) {
    var seg = document.getElementById(id);
    if (!seg) return "";
    var on = seg.querySelector("button.on");
    return on ? on.getAttribute("data-value") : "";
  }

  /* ---------- settings ---------- */

  // applySettings repaints every control that reflects a setting, so a save
  // updates the whole page rather than just the control that was touched.
  function applySettings(s) {
    if (!s) return;
    if (s.realm_default) stateDefaultRealm = s.realm_default;
    segSelect("realmSeg", s.realm_default || "doubao");
    segSelect("debugSeg", s.debug ? "on" : "off");
    segSelect("strategySeg", s.routing || "round_robin");
    var map = {
      request_timeout_seconds: "setTimeout",
      reply_poll_seconds: "setReply",
      media_poll_seconds: "setMedia",
      reply_poll_interval_ms: "setIntervalMs"
    };
    for (var name in map) {
      var el = document.getElementById(map[name]);
      if (el && s[name] !== undefined) el.value = s[name];
    }
    var effect = document.getElementById("realmEffect");
    if (effect && effect.dataset[s.realm_default]) effect.textContent = effect.dataset[s.realm_default];
    var segEffect = document.getElementById("strategySegEffect");
    if (segEffect && s.routing && segEffect.dataset[s.routing]) {
      segEffect.textContent = segEffect.dataset[s.routing];
    }
  }

  function setSetting(name, value, messageId) {
    var body = {};
    body[name] = value;
    write("/settings", body).then(function (data) {
      if (data && data.settings) applySettings(data.settings);
      if (data && data.persisted === false) {
        note(messageId, "已生效，但未能写入配置：" + (data.error || "未知原因"), "bad");
        return;
      }
      note(messageId, "已保存", "ok");
    }).catch(function (e) {
      note(messageId, "保存失败：" + e.message, "bad");
    });
  }

  /* ---------- accounts ---------- */

  function renderAccounts(list) {
    var wrap = document.getElementById("acctWrap");
    if (!wrap) return;
    if (!list || !list.length) {
      wrap.innerHTML = '<div class="empty">还没有账号。展开下面的「新增授权」粘贴 Cookie。</div>';
      return;
    }
    var html = '<div class="tbl-wrap"><table><thead><tr>'
      + '<th>账号</th><th class="state">上游</th><th class="state">状态</th>'
      + '<th class="num">调用</th><th class="num">最近使用</th><th class="act"></th>'
      + '</tr></thead><tbody>';
    list.forEach(function (a) {
      var realmTag = a.realm === "dola"
        ? '<span class="tag intl">' + esc(a.realm_name) + '</span>'
        : '<span class="tag cn">' + esc(a.realm_name) + '</span>';
      var status = '<span class="pill on">启用</span>';
      if (a.disabled) status = '<span class="pill off">已停用</span>';
      else if (a.auth_rejected) status = '<span class="pill off">需重新授权</span>';
      var action = a.disabled
        ? '<button type="button" class="xs" data-call="toggleAccount" data-arg0="' + esc(a.auth_index || "")
          + '" data-arg1="' + esc(a.path || "") + '" data-arg2="false">启用</button>'
        : '<button type="button" class="xs danger" data-call="toggleAccount" data-arg0="' + esc(a.auth_index || "")
          + '" data-arg1="' + esc(a.path || "") + '" data-arg2="true">停用</button>';
      var detail = a.last_error
        ? '<div class="note bad" style="margin-top:3px">' + esc(a.last_error) + '</div>'
        : (a.status_message ? '<div class="note" style="margin-top:3px">' + esc(a.status_message) + '</div>' : '');
      html += '<tr>'
        + '<td><div class="acct-name"><strong title="' + esc(a.label || a.name || a.id) + '">'
        + esc(a.label || a.name || a.id) + '</strong>' + detail + '</div></td>'
        + '<td class="state">' + realmTag + '</td>'
        + '<td class="state">' + status + '</td>'
        + '<td class="num">' + esc(a.calls || 0) + '</td>'
        + '<td class="note num">' + esc(a.last_used || "—") + '</td>'
        + '<td class="act">' + action + '</td>'
        + '</tr>';
    });
    wrap.innerHTML = html + '</tbody></table></div>';
  }

  function loadAccounts() {
    return request("/accounts").then(function (data) {
      if (data && data.accounts) renderAccounts(data.accounts);
      return data;
    }).catch(function (e) {
      note("accountMsg", e.message, "bad");
    });
  }

  /* ---------- authorisation ---------- */

  /* ---------- actions ---------- */

  var actions = {
    refreshAccounts: function () {
      if (!key()) { note("accountMsg", "请先在「设置」里保存管理密钥", "bad"); return; }
      note("accountMsg", "刷新中…");
      loadAccounts().then(function () { note("accountMsg", "已刷新", "ok"); });
    },

    toggleAccount: function (authIndex, path, disabled) {
      if (!key()) { toast("请先保存管理密钥", "bad"); return; }
      write("/account/toggle", {
        auth_index: authIndex,
        path: path,
        disabled: disabled === "true"
      }).then(function (data) {
        if (data && data.accounts) renderAccounts(data.accounts);
        toast((data && data.disabled ? "已停用 " : "已启用 ") + ((data && data.changed) || ""), "ok");
      }).catch(function (e) { toast(e.message, "bad"); });
    },

    bulkAccounts: function (mode) {
      var disable = mode === "disable";
      if (!key()) { toast("请先保存管理密钥", "bad"); return; }
      request("/accounts").then(function (data) {
        var list = (data && data.accounts) || [];
        var targets = list.filter(function (a) { return !!a.disabled !== disable; });
        if (!targets.length) { note("accountMsg", "无需改动"); return; }
        note("accountMsg", "处理中…");
        // Sequential rather than parallel: each call rewrites a credential file,
        // and fanning out would race on the shared auth store.
        var i = 0;
        function next() {
          if (i >= targets.length) {
            loadAccounts();
            note("accountMsg", (disable ? "已停用 " : "已启用 ") + targets.length + " 个账号", "ok");
            return;
          }
          var a = targets[i++];
          write("/account/toggle", { auth_index: a.auth_index, path: a.path, disabled: disable })
            .then(next)
            .catch(function (e) { note("accountMsg", "批量操作中断：" + e.message, "bad"); });
        }
        next();
      }).catch(function (e) { note("accountMsg", e.message, "bad"); });
    },

    removeAccount: function (key) {
      if (!key) { toast("缺少账号标识", "bad"); return; }
      if (!key()) { toast("请先保存管理密钥", "bad"); return; }
      write("/account/remove", { key: key }).then(function () {
        toast("已清除本地记录", "ok");
        setTimeout(reload, 400);
      }).catch(function (e) { toast(e.message, "bad"); });
    },

    openLogin: function () {
      // 账号页只有这一个添加入口。登录页里有国际版 / 国内版的切换，
      // 这里只负责给一个合理的初值：上次选过就用上次的，否则用默认上游。
      var realm = storageGet(AUTH_REALM_KEY) || stateDefaultRealm || "doubao";
      window.open(LOGIN_PATH + "?realm=" + encodeURIComponent(realm), "_blank");
    },

    setRealm: function (value) {
      segSelect("realmSeg", value);
      note("realmMsg", "切换中…");
      var effect = document.getElementById("realmEffect");
      if (effect && effect.dataset[value]) effect.textContent = effect.dataset[value];
      write("/realm/switch", { realm: value }).then(function (data) {
        if (data && data.settings) applySettings(data.settings);
        if (data && data.accounts) renderAccounts(data.accounts);
        var parts = [];
        if (data && data.disabled_other) parts.push("已停用 " + data.disabled_other + " 个另一侧账号");
        if (data && data.reenabled) parts.push("已恢复 " + data.reenabled + " 个本侧账号");
        if (data && data.isolate_error) parts.push("账号同步失败：" + data.isolate_error);
        if (data && data.persisted === false) parts.push("未能写入配置：" + (data.error || ""));
        var bad = parts.some(function (p) { return p.indexOf("失败") >= 0 || p.indexOf("未能") >= 0; });
        note("realmMsg", parts.length ? parts.join("；") : "已切换", bad ? "bad" : "ok");
      }).catch(function (e) { note("realmMsg", "切换失败：" + e.message, "bad"); });
    },

    pickStrategy: function (value) {
      segSelect("strategySeg", value);
      var effect = document.getElementById("strategySegEffect");
      if (effect && effect.dataset[value]) effect.textContent = effect.dataset[value];
      note("strategyMsg", "保存中…");
      write("/routing/config", { strategy: value }).then(function (data) {
        note("strategyMsg", "已保存", "ok");
        var next = document.getElementById("strategyNext");
        if (next && data && data.hint) next.textContent = data.hint;
        toast("路由策略已更新", "ok");
      }).catch(function (e) { note("strategyMsg", e.message, "bad"); });
    },

    resetRotation: function () {
      write("/routing/reset", {}).then(function () {
        toast("轮巡位置已重置", "ok");
      }).catch(function (e) { toast(e.message, "bad"); });
    },

    refreshUsage: function () {
      if (!key()) { toast("请先在「设置」里保存管理密钥", "bad"); return; }
      request("/calls").then(function () { reload(); })
        .catch(function (e) { toast(e.message, "bad"); });
    },

    clearCalls: function () {
      if (!key()) { toast("请先保存管理密钥", "bad"); return; }
      write("/calls/clear", {}).then(function () {
        toast("调用记录已清空", "ok");
        setTimeout(reload, 400);
      }).catch(function (e) { toast(e.message, "bad"); });
    },

    saveKey: function () {
      var el = document.getElementById("mgmtKey");
      if (!el) return;
      var v = (el.value || "").trim();
      if (!v) { note("keyMsg", "请输入密钥", "bad"); return; }
      if (!storageSet(OUR_KEY, v)) {
        note("keyMsg", "浏览器拒绝写入 localStorage", "bad");
        return;
      }
      el.value = "";
      refreshKeyState();
      note("keyMsg", "已保存", "ok");
      loadAccounts();
    },

    clearKey: function () {
      storageRemove(OUR_KEY);
      refreshKeyState();
      note("keyMsg", "已清除", "ok");
    }
  };

  function refreshKeyState() {
    var el = document.getElementById("keyState");
    if (!el) return;
    var k = key();
    if (k) {
      el.textContent = "已保存密钥（" + k.length + " 字符），可以直接保存设置。";
      el.className = "note ok";
    } else {
      el.textContent = "尚未保存密钥：涉及数据的操作会提示缺少管理密钥。";
      el.className = "note";
    }
  }

  /* ---------- wiring ---------- */

  // One listener for the whole document: every control declares data-call plus
  // positional data-argN attributes, so adding a control is a markup change only.
  document.addEventListener("click", function (ev) {
    var target = ev.target;
    while (target && target !== document) {
      if (target.dataset && target.dataset.view) {
        showView(target.dataset.view);
        return;
      }
      var name = target.dataset ? target.dataset.call : null;
      if (name && actions[name]) {
        ev.preventDefault();
        var args = [];
        for (var i = 0; target.dataset["arg" + i] !== undefined; i++) {
          args.push(target.dataset["arg" + i]);
        }
        actions[name].apply(null, args);
        return;
      }
      target = target.parentNode;
    }
  });

  function boot() {
    restoreView();
    refreshKeyState();
    if (!key()) {
      note("accountMsg", "请先在「设置」里保存管理密钥", "bad");
      return;
    }
    loadAccounts();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
</script>`
}
