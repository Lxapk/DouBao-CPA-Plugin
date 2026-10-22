package main

import (
	"fmt"
	"html"
	"strings"
)

// The usage page: totals, a trend strip, and the recent call log.
//
// The log answers the question the host's own request log cannot: which account
// served a request, and whether it succeeded. That attribution is what makes the
// account pool debuggable — a run of failures on one row is visible here before it
// becomes a support question.

// renderUsageView is the statistics page.
func renderUsageView() string {
	entries := accounts.get()
	var totalCalls, totalOK, totalFail int64
	for _, e := range entries {
		record := stats.lookup(e.SecUserID)
		if record == nil {
			record = stats.lookup(e.displayLabel())
		}
		if record == nil {
			continue
		}
		totalCalls += record.CallCount
		totalOK += record.SuccessCount
		totalFail += record.FailureCount
	}
	recent := state.logs.recent(200)

	var b strings.Builder
	b.WriteString(`<section class="view" id="view-usage" hidden>`)

	b.WriteString(`<div class="stats">`)
	statCard(&b, "", "总调用", totalCalls)
	statCard(&b, "good", "成功", totalOK)
	statCard(&b, "bad", "失败", totalFail)
	statCard(&b, "", "保留记录", len(recent))
	b.WriteString(`</div>`)

	b.WriteString(`<div class="box">`)
	b.WriteString(`<header><h3>调用趋势 <span class="hint">最近 40 条</span></h3>`)
	b.WriteString(`<span class="grow"></span><span class="note" id="trendCaption"></span>`)
	b.WriteString(`<button type="button" class="xs" data-call="refreshUsage">刷新</button>`)
	b.WriteString(`</header>`)
	b.WriteString(`<div class="trend" id="usageTrend">` + renderTrend(recent) + `</div>`)
	b.WriteString(`<div class="trend-legend">`)
	b.WriteString(`<span><i class="sw sw-ok"></i>成功</span>`)
	b.WriteString(`<span><i class="sw sw-bad"></i>失败</span>`)
	b.WriteString(`</div></div>`)

	b.WriteString(`<div class="box">`)
	b.WriteString(`<header><h3>调用记录 <span class="hint">最近的在前</span></h3><span class="grow"></span>`)
	b.WriteString(`<span class="note" id="callMsg"></span>`)
	b.WriteString(`<button type="button" class="xs" data-call="refreshUsage">刷新</button>`)
	b.WriteString(`<button type="button" class="xs danger" data-call="clearCalls">清空</button>`)
	b.WriteString(`</header>`)
	if len(recent) == 0 {
		b.WriteString(`<div class="empty">还没有调用记录。</div>`)
	} else {
		b.WriteString(`<div class="tbl-wrap"><table><thead><tr>`)
		b.WriteString(`<th>时间</th><th>账号</th><th>模型</th><th class="state">上游</th>`)
		b.WriteString(`<th class="state">状态</th><th class="num">耗时</th>`)
		b.WriteString(`</tr></thead><tbody>`)
		for _, entry := range recent {
			statusCell := `<span class="pill on">成功</span>`
			if entry.Status != "ok" {
				statusCell = `<span class="pill off">` + html.EscapeString(entry.Status) + `</span>`
			}
			detail := ""
			if entry.Detail != "" {
				detail = `<div class="note bad" style="margin-top:2px">` +
					html.EscapeString(trimText(entry.Detail, 90)) + `</div>`
			}
			b.WriteString(`<tr>` +
				`<td class="note">` + html.EscapeString(entry.Time) + `</td>` +
				`<td class="ell">` + html.EscapeString(shortAccount(entry.Account)) + detail + `</td>` +
				`<td>` + html.EscapeString(entry.Model) + `</td>` +
				`<td class="state">` + realmLabel(normalizeRealm(entry.Realm)) + `</td>` +
				`<td class="state">` + statusCell + `</td>` +
				`<td class="num">` + fmtInt(entry.Duration) + `ms</td>` +
				`</tr>`)
		}
		b.WriteString(`</tbody></table></div>`)
	}
	b.WriteString(`<div class="foot"><span class="note">` +
		`记录只保存在内存里，重启 CPA 后清空；账号页的累计次数不受影响。</span></div>`)
	b.WriteString(`</div>`)

	b.WriteString(`</section>`)
	return b.String()
}

// renderTrend draws one bar per log entry, coloured by outcome.
func renderTrend(entries []callEntry) string {
	if len(entries) == 0 {
		return `<div class="empty">暂无数据</div>`
	}
	if len(entries) > 40 {
		entries = entries[:40]
	}
	var b strings.Builder
	for i := len(entries) - 1; i >= 0; i-- {
		cls := "bar"
		if entries[i].Status != "ok" {
			cls += " bad"
		}
		height := 30 + len(entries[i].Model)%40
		if entries[i].Duration > 0 {
			// A long generation shows up as a tall bar, which is the only signal
			// in the strip that distinguishes an answer from a media job.
			height += int(minInt64(entries[i].Duration/250, 30))
		}
		b.WriteString(`<div class="` + cls + `" title="` + html.EscapeString(entries[i].Time) +
			` · ` + html.EscapeString(entries[i].Model) + `" style="height:` +
			fmtInt(int64(height)) + `%"></div>`)
	}
	return b.String()
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// shortAccount keeps the table narrow when the label is long.
func shortAccount(name string) string {
	if len(name) > 24 {
		return trimText(name, 22) + "…"
	}
	return name
}

// trimText truncates on a rune boundary so a multi-byte label is not cut mid-way.
func trimText(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

// guideCapability documents one alias: what it answers, and a request written
// against exactly the fields the plugin reads.
//
// The parameter sets are transcribed from the plugin's own request builder, not
// from the app's screens: the app offers resolution pickers the web endpoint does
// not accept, and listing a field the gateway ignores would be advice that fails.
type guideCapability struct {
	Model    string
	Name     string
	Kind     string
	Summary  string
	Params   []string
	Endpoint string
	Example  string
}

var guideCapabilities = []guideCapability{
	{
		Model: "default", Name: "默认助手", Kind: "文字",
		Summary: "等同于在网页端直接提问。多条消息会被折叠成一个提示词。",
		Params: []string{
			"messages：必填，只取文本内容",
			"system / developer：作为前置上下文折叠进提示词",
		},
		Endpoint: "/v1/chat/completions",
		Example: `{
  "model": "doubao/default",
  "messages": [{"role":"user","content":"你好"}]
}`,
	},
	{
		Model: "pro", Name: "深度思考", Kind: "文字",
		Summary: "开启深度思考：回复更慢，思维链以 reasoning_content 返回。",
		Params: []string{
			"messages：必填，只取文本内容",
			"无需额外参数，别名本身就打开了开关",
		},
		Endpoint: "/v1/chat/completions",
		Example: `{
  "model": "doubao/pro",
  "messages": [{"role":"user","content":"证明根号 2 是无理数"}]
}`,
	},

	// ---- 图片 ----
	{
		Model: "image", Name: "图像生成（默认）", Kind: "图片",
		Summary: "用账号默认的出图模型，等价于网页端直接出图。",
		Params: []string{
			"prompt：必填",
			"具体模型见下面几个 Seedream 别名",
		},
		Endpoint: "/v1/images/generations",
		Example: `{
  "model": "doubao/image",
  "prompt": "一只橘猫，水墨风格"
}`,
	},
	{
		Model: "seedream-5.0-pro", Name: "Seedream 5.0 Pro", Kind: "图片",
		Summary:  "专业出图，4 倍消耗。需要豆包订阅，额度不足时上游会拒绝。",
		Params:   []string{"prompt：必填", "aspect_ratio：可选，会写进生成要求", "model_item_key：豆包 seedream50Pro / Dola seedream_5.0_global"},
		Endpoint: "/v1/images/generations",
		Example: `{
  "model": "doubao/seedream-5.0-pro",
  "prompt": "赛博朋克风格的城市夜景，宽幅"
}`,
	},
	{
		Model: "seedream-5.0-flash", Name: "Seedream 5.0 Flash", Kind: "图片",
		Summary:  "专业快速，速度快于 Pro，消耗更低。",
		Params:   []string{"prompt：必填", "aspect_ratio：可选，会写进生成要求", "model_item_key：豆包 seedream50Lite / Dola seedream_5.0_global"},
		Endpoint: "/v1/images/generations",
		Example: `{
  "model": "doubao/seedream-5.0-flash",
  "prompt": "一张产品海报，简洁留白"
}`,
	},
	{
		Model: "seedream-4.5", Name: "Seedream 4.5", Kind: "图片",
		Summary:  "日常生成，网页端默认选中的就是它。",
		Params:   []string{"prompt：必填", "aspect_ratio：可选，会写进生成要求", "model_item_key：豆包 seedream_4.5 / Dola seedream_4.5_global"},
		Endpoint: "/v1/images/generations",
		Example: `{
  "model": "doubao/seedream-4.5",
  "prompt": "一只橘猫"
}`,
	},
	{
		Model: "seedream-4.0", Name: "Seedream 4.0", Kind: "图片",
		Summary:  "基础生图，兼容性最好。",
		Params:   []string{"prompt：必填", "aspect_ratio：可选，会写进生成要求", "model_item_key：豆包 seedream_4.0 / Dola seedream_4.0_global"},
		Endpoint: "/v1/images/generations",
		Example: `{
  "model": "doubao/seedream-4.0",
  "prompt": "一只橘猫"
}`,
	},

	// ---- 视频 ----
	{
		Model: "video", Name: "视频生成（默认）", Kind: "视频",
		Summary:  "用账号默认的视频模型。",
		Params:   []string{"prompt：必填", "aspect_ratio / seconds：可选", "具体模型见下面几个 Seedance 别名"},
		Endpoint: "/v1/videos",
		Example: `{
  "model": "doubao/video",
  "prompt": "海浪拍打礁石，慢镜头"
}`,
	},
	{
		Model: "seedance-2.5", Name: "Seedance 2.5", Kind: "视频",
		Summary:  "旗舰视频标杆，5 倍消耗。人像生成需先完成分身认证。",
		Params:   []string{"prompt：必填", "aspect_ratio / seconds：可选，会写进生成要求", "model_item_key：豆包 seedance25 / Dola seedance_2.5"},
		Endpoint: "/v1/videos",
		Example: `{
  "model": "doubao/seedance-2.5",
  "prompt": "火球从掌心升起，慢镜头",
  "seconds": 6
}`,
	},
	{
		Model: "seedance-2.0", Name: "Seedance 2.0", Kind: "视频",
		Summary:  "进阶画面表现，2 倍消耗。人像生成需先完成分身认证。",
		Params:   []string{"prompt：必填", "aspect_ratio / seconds：可选", "model_item_key：豆包 seedance20Standard / Dola seedance_2.0_pc_dola"},
		Endpoint: "/v1/videos",
		Example: `{
  "model": "doubao/seedance-2.0",
  "prompt": "海边日落延时",
  "seconds": 5
}`,
	},
	{
		Model: "seedance-2.0-fast", Name: "Seedance 2.0 Fast", Kind: "视频",
		Summary:  "快速出片选择，等待时间短。",
		Params:   []string{"prompt：必填", "aspect_ratio / seconds：可选", "model_item_key：豆包 seedance20Fast / Dola seedance_2.0_pc_dola"},
		Endpoint: "/v1/videos",
		Example: `{
  "model": "doubao/seedance-2.0-fast",
  "prompt": "一杯咖啡冒热气"
}`,
	},
	{
		Model: "seedance-2.0-mini", Name: "Seedance 2.0 Mini", Kind: "视频",
		Summary:  "日常生成使用，消耗最低。",
		Params:   []string{"prompt：必填", "aspect_ratio / seconds：可选", "model_item_key：豆包 seedance20Mini / Dola seedance_2.0_pc_dola"},
		Endpoint: "/v1/videos",
		Example: `{
  "model": "doubao/seedance-2.0-mini",
  "prompt": "窗外的雨"
}`,
	},
}

// renderEndpointTable lists the three endpoints this plugin answers on.
//
// The split matters to a client: an image generation sent to the chat endpoint
// comes back as a text description instead of a picture, because the chat
// pipeline never activates the generation capability.
func renderEndpointTable() string {
	var b strings.Builder
	b.WriteString(`<div class="box"><div class="pad" style="padding:0"><div class="tbl-wrap">`)
	b.WriteString(`<table><thead><tr><th>端点</th><th>请求体</th><th>响应</th></tr></thead><tbody>`)
	for _, row := range []struct{ path, req, resp string }{
		{
			"/v1/chat/completions",
			"<code>messages</code>，可选 <code>stream</code>",
			"<code>chat.completion</code> / <code>chat.completion.chunk</code>",
		},
		{
			"/v1/images/generations",
			"<code>prompt</code>，可选 <code>n</code>、<code>response_format</code>",
			"<code>{created, data:[{url}]}</code>",
		},
		{
			"/v1/videos",
			"<code>prompt</code>，可选 <code>seconds</code>、<code>size</code>",
			"<code>{id, status, url}</code>，查询同 id 仍返回该结果",
		},
	} {
		b.WriteString(`<tr><td><code>` + html.EscapeString(row.path) + `</code></td><td>` +
			row.req + `</td><td>` + row.resp + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div></div></div>`)
	return b.String()
}

// renderGuideView is the capability reference page.
//
// One card per alias, each ending with a request example written against exactly
// the fields this plugin reads. A shared table was tried first and abandoned: the
// parameter sets differ per capability, and the examples answer "what do I type"
// far better than a column of field names.
func renderGuideView() string {
	var b strings.Builder
	b.WriteString(`<section class="view" id="view-guide" hidden>`)

	b.WriteString(`<div class="group-head"><h2>三个 OpenAI 端点</h2>` +
		`<span class="desc">同一个上游，按端点分别返回标准的 OpenAI 结构</span></div>`)
	b.WriteString(renderEndpointTable())

	b.WriteString(`<div class="group-head"><h2>模型与请求示例</h2>` +
		`<span class="desc">模型列表跟着账号走：有哪一侧的账号就列出哪一侧；下面以上游前缀为 <code>doubao/</code> 为例</span></div>`)
	for _, item := range guideCapabilities {
		b.WriteString(renderGuideCard(item))
	}

	// ---- upstreams ----
	b.WriteString(`<div class="group-head"><h2>两个上游</h2>` +
		`<span class="desc">共用一套协议，只有站点与区域不同</span></div>`)
	b.WriteString(`<div class="box"><div class="pad" style="padding:0">`)
	b.WriteString(`<div class="tbl-wrap"><table><thead><tr><th>标识</th><th>名称</th><th>站点</th><th>aid</th><th>区域</th></tr></thead><tbody>`)
	for _, r := range allRealms {
		p := profileFor(r)
		b.WriteString(fmt.Sprintf(
			`<tr><td><code>%s</code></td><td>%s</td><td><code>%s</code></td><td><code>%s</code></td><td>%s / %s</td></tr>`,
			r, realmLabel(r), htmlEscape(p.Host), p.AID, p.Region, p.SysRegion,
		))
	}
	b.WriteString(`</tbody></table></div></div></div>`)
	b.WriteString(`<div class="note" style="margin:-4px 0 14px">` +
		`两个上游的账号彼此独立：豆包账号不能用于 Dola，反之亦然（会返回 ` +
		`<code>710022003 CountryRestricted</code>）。</div>`)

	// ---- login ----
	// 两侧的 passport 不是同一套：豆包有独立账号中心，Dola 挂在站点本身，
	// aid 也不同。写错过一次（拿豆包的 aid 去登 Dola，得到「该应用无权限」），
	// 所以把这几个值摊开写清楚。
	b.WriteString(`<div class="group-head"><h2>登录：两侧的 passport</h2>` +
		`<span class="desc">手机号 + 短信验证码，接口与 aid 各不相同</span></div>`)
	b.WriteString(`<div class="box"><div class="pad" style="padding:0">`)
	b.WriteString(`<div class="tbl-wrap"><table><thead><tr>` +
		`<th>上游</th><th>账号中心</th><th>aid</th><th>发码 / 登录接口</th></tr></thead><tbody>`)
	for _, r := range allRealms {
		profile := profileFor(r)
		b.WriteString(fmt.Sprintf(
			`<tr><td>%s</td><td><code>%s</code></td><td><code>%s</code></td>`+
				`<td class="note mono">/passport/web/send_code/<br>/passport/web/sms_login/</td></tr>`,
			realmLabel(r), htmlEscape(profile.PassportHost), profile.PassportAID,
		))
	}
	b.WriteString(`</tbody></table></div></div></div>`)
	b.WriteString(`<div class="note" style="margin:-4px 0 14px">` +
		`走的是<b>网页版</b> passport 接口。App 侧的 <code>/passport/mobile/send_code/v1/</code> ` +
		`按应用签名鉴权，第三方请求一律返回 <code>error_code 16 该应用无权限</code>。</div>`)

	// ---- limits ----
	b.WriteString(`<div class="group-head"><h2>图片输入</h2>` +
		`<span class="desc">在 messages 里放 image_url 即可</span></div>`)
	b.WriteString(`<div class="box"><div class="pad">`)
	b.WriteString(`<pre class="code">{
  "model": "doubao/default",
  "messages": [{
    "role": "user",
    "content": [
      {"type": "text", "text": "这张图里是什么"},
      {"type": "image_url", "image_url": {"url": "https://p3-flow-imagex-sign.byteimg.com/tos-cn-i-.../x.jpeg"}}
    ]
  }]
}</pre>`)
	b.WriteString(`<div class="note" style="margin-top:10px">图片会作为附件块（block_type 10052）随消息发出，` +
		`与网页端上传图片后的请求一致。只发图片、不带文字也可以。</div>`)
	b.WriteString(`<div class="note" style="margin-top:9px">图片地址可以是：<code>http(s)</code> 外链、` +
		`<code>data:image/…;base64,…</code> 内联图、本地文件路径，或豆包资源 uri` +
		`（<code>tos-cn-i-…</code>）。前三种由插件先上传到豆包再发送，最后一种直接使用。</div>`)
	b.WriteString(`</div></div>`)

	// ---- video polling ----
	// 视频要几十秒到十分钟，一次请求等不到结果，所以这里把"提交 + 轮询"写清楚；
	// 只给一个请求示例会让人以为它能同步返回。
	b.WriteString(`<div class="group-head"><h2>视频：提交与轮询</h2>` +
		`<span class="desc">生成要 5–10 分钟，提交后按 id 轮询</span></div>`)
	b.WriteString(`<div class="box"><div class="pad">`)
	b.WriteString(`<pre class="code"># 1. 提交，立刻返回 queued
curl -s http://127.0.0.1:8317/v1/videos \
  -H "Authorization: Bearer $KEY" \
  -d '{"model":"doubao/seedance-2.0-fast","prompt":"一只橘猫走过草地",
       "aspect_ratio":"16:9","seconds":5}'

# {"id":"video-3f2a…","status":"queued","progress":0,"seconds":"5"}

# 2. 轮询，直到 status 变成 completed
curl -s http://127.0.0.1:8317/v1/videos/video-3f2a… \
  -H "Authorization: Bearer $KEY"

# {"id":"video-3f2a…","status":"in_progress","progress":0}
# {"id":"video-3f2a…","status":"completed","progress":100,
#  "video":{"url":"https://…mp4"}}</pre>`)
	b.WriteString(`<div class="note" style="margin-top:10px">提交时会话可能先回一段参数确认（模型、时长、比例），` +
		`这是上游 agent 的正常行为；任务在后台继续跑，轮询会看到它变成 <code>completed</code>。</div>`)
	b.WriteString(`<div class="note" style="margin-top:9px"><b>为什么必须轮询</b>：豆包生成视频时会先推一段文字，` +
		`然后<b>主动断开连接</b>，成片要几分钟后才在会话里出现。连接断开不等于失败，` +
		`插件在断开会回查会话（<code>PULL_SINGLE_CHAIN</code>）取结果，所以提交那次请求` +
		`永远拿不到地址，只能靠轮询。</div>`)
	b.WriteString(`</div></div>`)

	b.WriteString(`<div class="group-head"><h2>已知限制</h2></div>`)
	b.WriteString(`<div class="box"><div class="pad">`)
	for _, line := range []string{
		"不能设置 system prompt 的优先级、temperature、top_p —— 上游是聊天客户端，不是补全 API。",
		"没有 token 计量 —— 上游不返回用量，<code>usage</code> 是按字符估算的，标有 <code>estimated: true</code>。",
		"流式不是逐 token —— 上游确实流式推送，但媒体结果是整块到达的。",
		"每请求复用账号最近的会话，并替换其内容；副作用是该会话在上游 UI 中会累积记录。",
		"生图 / 生视频消耗账号额度；图片地址是带时效的签名直链，<code>media[].key</code> 才是稳定标识。",
		"视频是异步任务：提交后立刻返回 <code>queued</code>，客户端用返回的 <code>id</code> 轮询才拿到 <code>completed</code> 与地址。生成通常要 5–10 分钟，且上游会中途断开连接 —— 断开会回查会话补读结果，所以别把断开当失败。",
	} {
		b.WriteString(`<div class="note" style="margin:5px 0;line-height:1.7">· ` + line + `</div>`)
	}
	b.WriteString(`</div></div>`)

	b.WriteString(`</section>`)
	return b.String()
}

// renderGuideCard draws one alias with its parameters and example.
func renderGuideCard(item guideCapability) string {
	var b strings.Builder
	b.WriteString(`<div class="box"><div class="pad">`)

	b.WriteString(`<div class="guide-head"><strong class="mono">` + html.EscapeString(item.Model) + `</strong>`)
	b.WriteString(`<span class="pill ` + map[string]string{
		"文字": "on", "图片": "wait", "视频": "off",
	}[item.Kind] + `">` + html.EscapeString(item.Kind) + `</span></div>`)
	b.WriteString(`<div class="note" style="margin-top:5px">` +
		html.EscapeString(item.Name) + ` · ` + html.EscapeString(item.Summary) + `</div>`)

	b.WriteString(`<dl class="guide-meta" style="margin-top:12px">`)
	for _, param := range item.Params {
		b.WriteString(`<dt>参数</dt><dd>` + html.EscapeString(param) + `</dd>`)
	}
	b.WriteString(`<dt>接口</dt><dd class="mono note">` + html.EscapeString(item.Endpoint) + `</dd>`)
	b.WriteString(`</dl>`)

	b.WriteString(`<pre class="code" style="margin-top:12px">` + html.EscapeString(item.Example) + `</pre>`)
	b.WriteString(`</div></div>`)
	return b.String()
}
