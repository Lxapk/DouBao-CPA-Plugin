package main

// The panel's stylesheet and the script that drives it.
//
// The token values mirror CPAMC's themes.scss so the panel sits inside the host's
// management UI rather than beside it. The theme follows the host: the host writes
// data-theme="dark" or "white" on its own root and removes the attribute when the
// user picked "follow system", in which case the media query decides. Reading it
// back is what keeps an explicit choice in sync.

const uiCSS = `
:root,
:root[data-theme="dark"] {
  color-scheme: dark;
  --bg-secondary: #151412;
  --bg-primary: #1d1b18;
  --bg-tertiary: #262320;
  --bg-hover: #2e2a26;
  --bg-quinary: #191714;

  --text-primary: #f6f4f1;
  --text-secondary: #c9c3bb;
  --text-tertiary: #9c958d;
  --text-quaternary: #6f6962;

  --border-color: #3a3530;
  --border-primary: #4a453f;
  --border-hover: #5a544d;

  --primary-color: #8b8680;
  --primary-hover: #9a948e;
  --primary-active: #a6a099;
  --primary-contrast: #ffffff;

  --success-color: #10b981;
  --warning-color: #c65746;
  --error-color: #c65746;
  --info-color: #5b8def;

  --shadow: 0 1px 3px 0 rgb(0 0 0 / 0.3);
  --shadow-lg: 0 14px 30px rgba(0, 0, 0, 0.4);
}

:root[data-theme="white"],
:root[data-theme="light"] {
  color-scheme: light;
  --bg-secondary: #ffffff;
  --bg-primary: #ffffff;
  --bg-tertiary: #f6f6f6;
  --bg-hover: #f0f0f0;
  --bg-quinary: #ffffff;

  --text-primary: #2d2a26;
  --text-secondary: #6d6760;
  --text-tertiary: #a29c95;
  --text-quaternary: #c0bab3;

  --border-color: #e5e5e5;
  --border-primary: #d9d9d9;
  --border-hover: #cccccc;

  --primary-color: #8b8680;
  --primary-hover: #7f7a74;
  --primary-active: #726d67;
  --primary-contrast: #ffffff;

  --success-color: #10b981;
  --warning-color: #c65746;
  --error-color: #c65746;
  --info-color: #3b82f6;

  --shadow: 0 1px 2px 0 rgb(0 0 0 / 0.08);
  --shadow-lg: 0 10px 18px -3px rgb(0 0 0 / 0.1);
}

/* "follow system": no attribute on the root, so the media query decides. */
@media (prefers-color-scheme: light) {
  :root:not([data-theme]) {
    color-scheme: light;
    --bg-secondary: #ffffff;
    --bg-primary: #ffffff;
    --bg-tertiary: #f6f6f6;
    --bg-hover: #f0f0f0;
    --bg-quinary: #ffffff;
    --text-primary: #2d2a26;
    --text-secondary: #6d6760;
    --text-tertiary: #a29c95;
    --text-quaternary: #c0bab3;
    --border-color: #e5e5e5;
    --border-primary: #d9d9d9;
    --border-hover: #cccccc;
    --shadow: 0 1px 2px 0 rgb(0 0 0 / 0.08);
    --shadow-lg: 0 10px 18px -3px rgb(0 0 0 / 0.1);
  }
}

:root {
  --radius-sm: 4px;
  --radius-md: 8px;
  --radius-lg: 12px;
  --radius-card: 14px;
  --radius-full: 9999px;

  --space-xs: 4px;
  --space-sm: 8px;
  --space-md: 16px;
  --space-lg: 24px;
  --space-xl: 32px;

  --dur-fast: 150ms;
  --ease: ease;

  --mono: ui-monospace, "SF Mono", "Cascadia Mono", Menlo, Consolas, monospace;
  --sans: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "PingFang SC",
          "Microsoft YaHei", "Helvetica Neue", sans-serif;
}

* { box-sizing: border-box; }

body {
  margin: 0;
  padding: var(--space-lg);
  background: var(--bg-secondary);
  color: var(--text-primary);
  font-family: var(--sans);
  font-size: 14px;
  line-height: 1.6;
  -webkit-font-smoothing: antialiased;
}

.shell { max-width: 1080px; margin: 0 auto; }

/* ---- header ---- */
.page-header { margin-bottom: var(--space-lg); }
.page-header h1 { margin: 0 0 6px; font-size: 22px; font-weight: 600; letter-spacing: -.01em; }
.page-header .desc { margin: 0; color: var(--text-tertiary); font-size: 13px; }

/* ---- tabs ---- */
.tabbar {
  display: flex;
  gap: var(--space-xs);
  margin-bottom: var(--space-lg);
  border-bottom: 1px solid var(--border-color);
  overflow-x: auto;
  scrollbar-width: none;
}
.tabbar::-webkit-scrollbar { display: none; }
.tab {
  appearance: none;
  border: 0;
  background: transparent;
  color: var(--text-tertiary);
  font: inherit;
  font-size: 13px;
  padding: 9px 14px;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  cursor: pointer;
  white-space: nowrap;
  transition: color var(--dur-fast) var(--ease), border-color var(--dur-fast) var(--ease);
}
.tab:hover { color: var(--text-secondary); }
.tab.on { color: var(--text-primary); border-bottom-color: var(--text-primary); font-weight: 500; }

/* ---- cards ---- */
.box {
  background: var(--bg-primary);
  border: 1px solid var(--border-color);
  border-radius: var(--radius-card);
  margin-bottom: var(--space-md);
  overflow: hidden;
}
.box > header {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  padding: 13px var(--space-md);
  border-bottom: 1px solid var(--border-color);
}
.box > header h3 { margin: 0; font-size: 14px; font-weight: 600; }
.box > header .hint { color: var(--text-quaternary); font-size: 12px; font-weight: 400; }
.box .pad { padding: var(--space-md); }
.box .grow { flex: 1; }

.group-head { margin: var(--space-lg) 0 var(--space-sm); }
.group-head:first-child { margin-top: 0; }
.group-head h2 { margin: 0; font-size: 14px; font-weight: 600; }
.group-head .desc { color: var(--text-tertiary); font-size: 12px; }

/* ---- stat strip ---- */
.stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: var(--space-sm); }
.stat {
  background: var(--bg-primary);
  border: 1px solid var(--border-color);
  border-radius: var(--radius-lg);
  padding: 13px var(--space-md);
}
.stat .label { color: var(--text-tertiary); font-size: 12px; margin-bottom: 3px; }
.stat .value { font-size: 20px; font-weight: 600; font-variant-numeric: tabular-nums; }
.stat.good .value { color: var(--success-color); }
.stat.warn .value { color: var(--warning-color); }
.stat.bad .value { color: var(--error-color); }

/* ---- tables ---- */
table { width: 100%; border-collapse: collapse; font-size: 13px; }
th, td { text-align: left; padding: 9px var(--space-md); border-bottom: 1px solid var(--border-color); }
th { color: var(--text-tertiary); font-weight: 500; font-size: 12px; }
tbody tr:last-child td { border-bottom: 0; }
tbody tr:hover { background: var(--bg-tertiary); }

/* ---- settings rows ---- */
.setting-group { padding: var(--space-md); border-bottom: 1px solid var(--border-color); }
.setting-group:last-child { border-bottom: 0; }
.setting-label { margin-bottom: 9px; }
.setting-label .name { display: block; font-size: 13px; font-weight: 500; }
.setting-label .desc { display: block; color: var(--text-tertiary); font-size: 12px; margin-top: 2px; }
.setting-effect {
  margin-top: 9px;
  padding: 9px 11px;
  background: var(--bg-tertiary);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  font-size: 12px;
}

/* ---- segmented control ---- */
.seg { display: inline-flex; background: var(--bg-tertiary); border-radius: var(--radius-md); padding: 2px; gap: 2px; }
.seg button {
  appearance: none; border: 0; background: transparent; color: var(--text-tertiary);
  font: inherit; font-size: 13px; padding: 6px 15px; border-radius: 6px;
  cursor: pointer; transition: background var(--dur-fast) var(--ease), color var(--dur-fast) var(--ease);
}
.seg button:hover { color: var(--text-secondary); }
.seg button.on { background: var(--bg-primary); color: var(--text-primary); font-weight: 500; box-shadow: var(--shadow); }

/* ---- inputs and buttons ---- */
input, select {
  background: var(--bg-secondary);
  border: 1px solid var(--border-primary);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font: inherit;
  font-size: 13px;
  padding: 6px 10px;
  outline: none;
  transition: border-color var(--dur-fast) var(--ease);
}
input:focus, select:focus { border-color: var(--border-hover); }
input[type="number"] { width: 100px; font-variant-numeric: tabular-nums; }

button.primary, button.ghost {
  appearance: none; font: inherit; font-size: 13px; padding: 6px 14px;
  border-radius: var(--radius-md); cursor: pointer; border: 1px solid transparent;
  transition: background var(--dur-fast) var(--ease), border-color var(--dur-fast) var(--ease);
}
button.primary { background: var(--primary-color); color: var(--primary-contrast); }
button.primary:hover { background: var(--primary-hover); }
button.ghost { background: transparent; border-color: var(--border-primary); color: var(--text-secondary); }
button.ghost:hover { background: var(--bg-tertiary); border-color: var(--border-hover); }
button:disabled { opacity: .5; cursor: not-allowed; }

.row { display: flex; gap: var(--space-sm); align-items: center; flex-wrap: wrap; }
.btn-end { display: flex; gap: var(--space-sm); margin-left: auto; }

/* ---- misc ---- */
code, .mono { font-family: var(--mono); font-size: 12px; }
code { background: var(--bg-tertiary); padding: 1px 5px; border-radius: var(--radius-sm); }
.note { color: var(--text-tertiary); font-size: 12px; }
.note.ok { color: var(--success-color); }
.note.bad { color: var(--error-color); }
.tag {
  display: inline-block; font-size: 11px; padding: 1px 7px; border-radius: var(--radius-full);
  background: var(--bg-tertiary); color: var(--text-tertiary); margin-left: 6px;
}
.tag.cn { background: rgba(198, 87, 70, .14); color: var(--warning-color); }
.tag.intl { background: rgba(91, 141, 239, .14); color: var(--info-color); }
.empty { padding: var(--space-lg); text-align: center; color: var(--text-quaternary); font-size: 13px; }

/* ---- toasts ---- */
#toasts { position: fixed; right: var(--space-md); bottom: var(--space-md); display: flex; flex-direction: column; gap: var(--space-sm); z-index: 50; }
.toast {
  background: var(--bg-primary); border: 1px solid var(--border-primary);
  border-radius: var(--radius-md); box-shadow: var(--shadow-lg);
  padding: 10px var(--space-md); font-size: 13px; max-width: 330px;
}
.toast.bad { border-color: var(--error-color); color: var(--error-color); }
.toast.ok { border-color: var(--success-color); }

ol.steps { margin: 0; padding-left: 20px; color: var(--text-secondary); font-size: 13px; }
ol.steps li { margin: 5px 0; }
`

// uiScript drives the panel.
//
// It mirrors the host's theme, keeps the active tab across reloads, and routes
// every control through a single data-call dispatch so adding a control never
// means adding another listener.
const uiScript = `
// adoptHostTheme mirrors the host's theme onto this document.
//
// The panel renders inside CPA's management UI, which marks its own <html> with
// data-theme="dark" / "white", or removes it when the user chose "follow system".
// Reading that attribute keeps an explicit choice in sync; when it is absent (or the
// frame is cross-origin, where the read throws) the stylesheet's media query takes
// over, which is exactly what "follow system" means.
//
// Re-checked on a timer as well as at load: the host swaps the attribute without
// reloading the iframe.
function adoptHostTheme() {
  var hostTheme = '';
  try {
    if (window.parent && window.parent !== window && window.parent.document) {
      hostTheme = window.parent.document.documentElement.getAttribute('data-theme') || '';
    }
  } catch (e) {
    hostTheme = '';
  }
  var root = document.documentElement;
  if (hostTheme === 'dark' || hostTheme === 'white') {
    root.setAttribute('data-theme', hostTheme);
  } else {
    root.removeAttribute('data-theme');
  }
}
adoptHostTheme();
setInterval(adoptHostTheme, 3000);

function showTab(id) {
  id = String(id || '');
  if (id && id.indexOf('view-') !== 0) id = 'view-' + id;

  var pages = document.querySelectorAll('.view');
  var shown = false;
  for (var i = 0; i < pages.length; i++) {
    var match = pages[i].id === id;
    pages[i].hidden = !match;
    if (match) shown = true;
  }
  // Never leave the page blank: an unknown id falls back to the first page.
  if (!shown && pages.length) {
    pages[0].hidden = false;
    id = pages[0].id;
  }
  var tabs = document.querySelectorAll('.tabbar .tab[data-view]');
  for (var j = 0; j < tabs.length; j++) {
    if (tabs[j].getAttribute('data-view') === id) tabs[j].classList.add('on');
    else tabs[j].classList.remove('on');
  }
  try { localStorage.setItem('doubao-panel-view', id); } catch (e) {}

  // The account list is fetched when its tab is opened rather than at load, so a
  // visitor who never looks at it does not pay for the host RPCs it needs.
  if (id === 'view-accounts' && typeof loadAccounts === 'function') loadAccounts();

  if (window.scrollY > 0) window.scrollTo(0, 0);
}

function restoreTab() {
  var saved = '';
  try { saved = localStorage.getItem('doubao-panel-view') || ''; } catch (e) {}
  var page = saved ? document.getElementById(saved) : null;
  if (page) { showTab(saved); return; }
  var first = document.querySelector('.view');
  if (first) showTab(first.id);
}

// ---- toast ----
function toast(message, kind) {
  var host = document.getElementById('toasts');
  if (!host) return;
  var el = document.createElement('div');
  el.className = 'toast' + (kind ? ' ' + kind : '');
  el.textContent = message;
  host.appendChild(el);
  setTimeout(function () { el.remove(); }, 4200);
}

// ---- API ----
// Reads and writes both go to /v0/management/doubao/*, behind CPA's management
// middleware. The page itself is served from the public resource path so it renders
// in the host's iframe, but every JSON call needs the management key.
//
// A resource route only answers GET and only for the exact path it registered, so
// putting the JSON there would need one resource entry per endpoint and would still
// refuse writes.
var API = '/v0/management/doubao';

function managementKey() {
  try { return localStorage.getItem('cpa-management-key') || ''; } catch (e) { return ''; }
}

function call(path, options) {
  options = options || {};
  var headers = options.headers || {};
  var key = managementKey();
  if (key) headers['Authorization'] = 'Bearer ' + key;
  options.headers = headers;
  return fetch(API + path, options).then(function (resp) {
    return resp.text().then(function (text) {
      var data = null;
      try { data = text ? JSON.parse(text) : null; } catch (e) { data = null; }
      if (!resp.ok) {
        var msg = (data && (data.error || data.message)) || ('HTTP ' + resp.status);
        if (resp.status === 401 || resp.status === 403) {
          msg = '未授权：请在「设置」里填入 CPA 管理密钥';
        }
        var err = new Error(msg);
        // Carry the parsed body: an authorisation failure returns a structured 400
        // whose error_hint is the actionable half of the message, and rethrowing a
        // bare string would drop it.
        err.data = data;
        err.status = resp.status;
        throw err;
      }
      return data;
    });
  });
}

function callWrite(path, body) {
  return call(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
}

// ---- settings ----
// loadSettings pulls the live values on open.
//
// The page is rendered from the settings the plugin held at render time, which is
// already correct — but a failure to load is worth surfacing, because it is the same
// call the saves use, so a broken key shows up here before the user tries to save.
function loadSettings() {
  if (!managementKey()) return;
  call('/settings').then(function (data) {
    if (data && data.settings) applySettings(data.settings);
  }).catch(function (err) {
    if (String(err.message).indexOf('未授权') === 0) setNote('keyMsg', err.message, 'bad');
  });
}

// setSetting writes one value and reports the outcome next to the control that
// changed, so the message appears where the user is looking.
function setSetting(name, value, messageId) {
  var body = {};
  body[name] = value;
  callWrite('/settings', body).then(function (data) {
    if (data && data.settings) applySettings(data.settings);
    if (data && data.persisted === false) {
      setNote(messageId, '已生效，但未能写入配置：' + (data.error || '未知原因'), 'bad');
      return;
    }
    setNote(messageId, '已保存', 'ok');
  }).catch(function (err) {
    setNote(messageId, '保存失败：' + err.message, 'bad');
  });
}

function setNote(id, text, kind) {
  var el = document.getElementById(id);
  if (!el) return;
  el.textContent = text;
  el.className = 'note' + (kind ? ' ' + kind : '');
  if (text) setTimeout(function () {
    if (el.textContent === text) { el.textContent = ''; el.className = 'note'; }
  }, 3200);
}

// applySettings repaints every control that reflects a setting, so a save updates
// the whole page rather than just the control that was touched.
function applySettings(s) {
  if (!s) return;
  segSelect('realmSeg', s.realm_default || 'doubao');
  segSelect('exposeSeg', s.expose_models ? 'on' : 'off');
  segSelect('debugSeg', s.debug ? 'on' : 'off');
  var map = {
    request_timeout_seconds: 'setTimeout',
    reply_poll_seconds: 'setReply',
    media_poll_seconds: 'setMedia',
    reply_poll_interval_ms: 'setIntervalMs',
    default_model: 'setDefaultModel'
  };
  for (var key in map) {
    var el = document.getElementById(map[key]);
    if (el && s[key] !== undefined) el.value = s[key];
  }
  var effect = document.getElementById('realmEffect');
  if (effect && effect.dataset[s.realm_default]) effect.textContent = effect.dataset[s.realm_default];
}

function segSelect(id, value) {
  var seg = document.getElementById(id);
  if (!seg) return;
  var buttons = seg.querySelectorAll('button');
  for (var i = 0; i < buttons.length; i++) {
    if (buttons[i].getAttribute('data-value') === value) buttons[i].classList.add('on');
    else buttons[i].classList.remove('on');
  }
}

function numSetting(id, name, messageId) {
  var el = document.getElementById(id);
  if (!el) return;
  var v = parseInt(el.value, 10);
  if (isNaN(v) || v <= 0) { setNote(messageId, '请输入正整数', 'bad'); return; }
  setSetting(name, v, messageId);
}

function textSetting(id, name, messageId) {
  var el = document.getElementById(id);
  if (!el) return;
  setSetting(name, el.value, messageId);
}

// ---- named actions ----
// The dispatcher looks these up on window by the name in data-call, so each one is
// a plain global function.

// setRealm switches the default upstream and isolates the other side's accounts.
//
// It calls /realm/switch rather than /settings because the two effects belong
// together: changing the upstream while the other realm's accounts stay routable
// produces calls that fail with a country error, which reads as a broken credential.
function setRealm(value) {
  segSelect('realmSeg', value);
  setNote('realmMsg', '切换中…', '');
  callWrite('/realm/switch', { realm: value }).then(function (data) {
    applySettings(data.settings);
    if (data.accounts) renderAccounts(data.accounts);
    var parts = [];
    if (data.disabled_other) parts.push('已禁用 ' + data.disabled_other + ' 个另一侧账号');
    if (data.reenabled) parts.push('已恢复 ' + data.reenabled + ' 个本侧账号');
    if (data.isolate_error) parts.push('账号同步失败：' + data.isolate_error);
    if (data.persisted === false) parts.push('未能写入配置：' + (data.error || ''));
    setNote('realmMsg', parts.length ? parts.join('；') : '已切换', parts.some(function (p) {
      return p.indexOf('失败') >= 0 || p.indexOf('未能') >= 0;
    }) ? 'bad' : 'ok');
  }).catch(function (err) {
    setNote('realmMsg', '切换失败：' + err.message, 'bad');
  });
}

function setExpose(value) {
  segSelect('exposeSeg', value);
  setSetting('expose_models', value === 'on', 'exposeMsg');
}

function setDebug(value) {
  segSelect('debugSeg', value);
  setSetting('debug', value === 'on', 'debugMsg');
}

function saveNum(inputId, name) {
  numSetting(inputId, name, 'callMsg');
}

// ---- plugin-side authorisation ----
// The form talks to /doubao/authorize, which validates the cookie against the live
// upstream and writes the auth record. CPA's own OAuth callback box is not involved:
// it insists on a callback URL and answers "state is required" to a pasted cookie.

function pickAuthRealm(value) {
  segSelect('authRealmSeg', value);
  setNote('authResult', '', '');
}

function selectedAuthRealm() {
  var seg = document.getElementById('authRealmSeg');
  if (!seg) return '';
  var on = seg.querySelector('button.on');
  return on ? on.getAttribute('data-value') : '';
}

function clearAuthForm() {
  var ta = document.getElementById('authCookies');
  var lb = document.getElementById('authLabel');
  if (ta) ta.value = '';
  if (lb) lb.value = '';
  setNote('authResult', '', '');
}

function submitAuthorize() {
  var ta = document.getElementById('authCookies');
  var lb = document.getElementById('authLabel');
  var btn = document.getElementById('authSubmit');
  if (!ta) return;
  var cookies = (ta.value || '').trim();
  if (!cookies) { setNote('authResult', '请先粘贴 Cookie。', 'bad'); return; }

  if (btn) { btn.disabled = true; btn.textContent = '校验中…'; }
  setNote('authResult', '正在向上游校验…', '');

  callWrite('/authorize', {
    cookies: cookies,
    realm: selectedAuthRealm(),
    label: lb ? (lb.value || '').trim() : ''
  }).then(function (data) {
    showAuthorizeResult(data, true);
    if (data && data.ok) {
      ta.value = '';
      if (lb) lb.value = '';
      loadAccounts();
    }
  }).catch(function (err) {
    // A rejected credential comes back as a structured 400, so the body carries the
    // detail (including the actionable hint) even though the status is an error.
    showAuthorizeResult(err.data, false);
    if (!err.data) setNote('authResult', err.message, 'bad');
  }).then(function () {
    if (btn) { btn.disabled = false; btn.textContent = '校验并授权'; }
  });
}

function showAuthorizeResult(data, ok) {
  var host = document.getElementById('authResult');
  if (!host) return;
  if (!data) { host.innerHTML = ''; return; }

  if (!data.ok) {
    var msg = esc(data.error || '授权失败');
    if (data.error_hint) msg += '<div class="note" style="margin-top:6px">' + esc(data.error_hint) + '</div>';
    host.innerHTML = '<div class="toast bad" style="max-width:none">' + msg + '</div>';
    return;
  }

  var lines = [];
  lines.push('<div><b>授权成功</b> — ' + esc(data.realm_name || data.realm) + '</div>');
  if (data.label) lines.push('<div class="note">账号：' + esc(data.label) + '</div>');
  if (data.model_count) lines.push('<div class="note">可用模型：' + esc(data.model_count) + ' 个</div>');
  if (data.expires_at) lines.push('<div class="note">会话有效期至：' + esc(data.expires_at) + '</div>');
  (data.warnings || []).forEach(function (w) {
    lines.push('<div class="note bad">' + esc(w) + '</div>');
  });
  host.innerHTML = '<div class="toast ok" style="max-width:none">' + lines.join('') + '</div>';
}

// ---- accounts ----
// renderAccounts replaces the table body.
//
// The script rebuilds each row because the buttons carry a data-call the dispatcher
// resolves at click time, and innerHTML on the wrapper is what keeps the markup in the
// backend's Go source rather than duplicated here.
function renderAccounts(list) {
  var wrap = document.getElementById('acctWrap');
  if (!wrap) return;
  if (!list || !list.length) {
    wrap.innerHTML = '<div class="empty">还没有账号。展开下面的「新增授权」按步骤添加。</div>';
    return;
  }
  var html = '<table><thead><tr><th>账号</th><th>上游</th><th>状态</th><th>操作</th></tr></thead><tbody>';
  list.forEach(function (a) {
    var realmTag = a.realm === 'dola'
      ? '<span class="tag intl">' + esc(a.realm_name) + '</span>'
      : '<span class="tag cn">' + esc(a.realm_name) + '</span>';
    var state = a.disabled
      ? '<span class="tag">已禁用</span>'
      : '<span class="tag intl">启用中</span>';
    var action = a.disabled ? '启用' : '禁用';
    html += '<tr>'
      + '<td>' + esc(a.label || a.name || a.id) + '</td>'
      + '<td>' + realmTag + '</td>'
      + '<td>' + state + (a.status_message ? '<div class="note">' + esc(a.status_message) + '</div>' : '') + '</td>'
      + '<td><button type="button" class="ghost"'
      + ' data-call="toggleAccount"'
      + ' data-arg0="' + esc(a.auth_index || '') + '"'
      + ' data-arg1=""'
      + ' data-arg2="' + (a.disabled ? 'true' : 'false') + '">' + action + '</button></td>'
      + '</tr>';
  });
  wrap.innerHTML = html + '</tbody></table>';
}

function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
  });
}

function loadAccounts() {
  call('/accounts').then(function (data) {
    if (data && data.accounts) renderAccounts(data.accounts);
    else setNote('acctMsg', '未返回账号列表', 'bad');
  }).catch(function (err) {
    setNote('acctMsg', err.message, 'bad');
    var wrap = document.getElementById('acctWrap');
    if (wrap) wrap.innerHTML = '<div class="empty">' + esc(err.message) + '</div>';
  });
}

function reloadAccounts() {
  setNote('acctMsg', '读取中…', '');
  call('/accounts').then(function (data) {
    if (data && data.accounts) renderAccounts(data.accounts);
    setNote('acctMsg', '已刷新', 'ok');
  }).catch(function (err) {
    setNote('acctMsg', err.message, 'bad');
  });
}

function toggleAccount(authIndex, path, disabled) {
  callWrite('/account/toggle', {
    auth_index: authIndex,
    path: path,
    disabled: disabled === 'true' || disabled === true
  }).then(function (data) {
    if (data.accounts) renderAccounts(data.accounts);
    setNote('acctMsg', (data.disabled ? '已禁用 ' : '已启用 ') + (data.changed || ''), 'ok');
  }).catch(function (err) {
    setNote('acctMsg', err.message, 'bad');
  });
}

function bulkAccounts(mode) {
  var disable = mode === 'disable';
  call('/accounts').then(function (data) {
    var list = (data && data.accounts) || [];
    var targets = list.filter(function (a) { return a.disabled !== disable; });
    if (!targets.length) { setNote('acctMsg', '无需改动', ''); return; }
    setNote('acctMsg', '处理中…', '');
    // Sequential rather than parallel: each call rewrites a credential file, and
    // fanning out would race on the shared auth store.
    var i = 0;
    function next() {
      if (i >= targets.length) {
        reloadAccounts();
        setNote('acctMsg', (disable ? '已禁用 ' : '已启用 ') + targets.length + ' 个账号', 'ok');
        return;
      }
      var a = targets[i++];
      callWrite('/account/toggle', {
        auth_index: a.auth_index, disabled: disable
      }).then(next).catch(function (err) {
        setNote('acctMsg', '批量操作中断：' + err.message, 'bad');
      });
    }
    next();
  }).catch(function (err) { setNote('acctMsg', err.message, 'bad'); });
}

// ---- management key ----
function saveKey() {
  var el = document.getElementById('mgmtKey');
  if (!el) return;
  var v = (el.value || '').trim();
  if (!v) { setNote('keyMsg', '请输入密钥', 'bad'); return; }
  try { localStorage.setItem('cpa-management-key', v); } catch (e) {
    setNote('keyMsg', '浏览器拒绝写入 localStorage', 'bad');
    return;
  }
  el.value = '';
  showKeyState();
  setNote('keyMsg', '已保存', 'ok');
}

function clearKey() {
  try { localStorage.removeItem('cpa-management-key'); } catch (e) {}
  showKeyState();
  setNote('keyMsg', '已清除', 'ok');
}

function showKeyState() {
  var el = document.getElementById('keyState');
  if (!el) return;
  var has = false;
  try { has = !!localStorage.getItem('cpa-management-key'); } catch (e) {}
  el.textContent = has ? '当前：已保存密钥，可以直接保存设置。' : '当前：未保存密钥，保存设置会失败。';
}

// ---- single dispatch ----
// One listener for the whole document: every control declares data-call plus
// positional data-argN attributes, so adding a control is a markup change only.
document.addEventListener('click', function (ev) {
  var tab = ev.target.closest ? ev.target.closest('.tab[data-view]') : null;
  if (tab) { showTab(tab.getAttribute('data-view')); return; }

  var el = ev.target.closest ? ev.target.closest('[data-call]') : null;
  if (!el) return;
  var action = el.getAttribute('data-call');
  var args = [];
  for (var i = 0; el.getAttribute('data-arg' + i) !== null; i++) {
    args.push(el.getAttribute('data-arg' + i));
  }
  if (typeof window[action] === 'function') window[action].apply(null, args);
});

document.addEventListener('DOMContentLoaded', function () {
  restoreTab();
  showKeyState();
  loadSettings();
});
if (document.readyState !== 'loading') { restoreTab(); showKeyState(); loadSettings(); }
`
