# doubao-cpa-plugin

把**豆包（国内版）**与 **Dola（国际版）**反代为 OpenAI 兼容接口的 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 动态插件。

参考 [Lxapk/workbuddy-cpa-plugin](https://github.com/Lxapk/workbuddy-cpa-plugin) 的架构。

---

## 能力

| 能力 | 状态 | 说明 |
|---|---|---|
| 文本对话 | ✅ 可用 | `/v1/chat/completions`，支持流式 |
| 深度思考 | ✅ 可用 | 思维链以 `reasoning_content` 返回 |
| **图片生成** | ✅ 可用 | `doubao/image`，输出 OpenAI 多模态 `image_url` |
| **视频生成** | ✅ 可用 | `doubao/video`，URL 以文本 + `media` 扩展返回 |
| 双上游 | ✅ 可用 | 同一插件支持豆包与 Dola 两套账号 |
| 会话列表 | ✅ 可用 | 自动获取会话，无需手工配置 |

---

## 快速开始

### 1. 构建

```bash
CGO_ENABLED=1 go build -buildmode=c-shared -o doubao.so .
rm -f doubao.h          # 生成的头文件不需要
```

插件 ID 取自文件名，因此必须叫 `doubao.so`。

### 2. 部署

把 `doubao.so` 放进 CPA 的 `plugins/` 目录，并在 `config.yaml` 中启用：

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    doubao:
      enabled: true
      priority: 20
      debug: false
      realm_default: doubao    # doubao=国内版豆包, dola=国际版
      expose_models: true
```

重启 CPA。CPA 必须是 **cgo 版本**（`CGO_ENABLED=1` 编译），否则无法加载插件。

### 3. 授权

在 CPA 的插件面板中授权。需要粘贴**登录后的完整 Cookie**：

1. 浏览器打开 `www.doubao.com`（国内版）或 `www.dola.com`（国际版）并登录
2. 按 `F12` → **Network** → 刷新页面
3. 点任意一条该站点的请求 → **Request Headers** → 找到 `Cookie`
4. 复制完整值（**不要带 `Cookie:` 前缀**）粘贴进来

**必须包含** `flow_cur_user_sec_id` 与 `sessionid`，否则网关认为会话无效。

Cookie 有效期见 `sid_guard`（豆包约 30 天）。

---

## 调用

### 模型名

模型名可加前缀指定上游，不加前缀时使用配置的默认上游：

```
doubao/default    豆包 · 默认助手
doubao/pro        豆包 · 深度思考
doubao/image      豆包 · 图像生成
doubao/video      豆包 · 视频生成
dola/default      Dola · 默认助手
dola/image        Dola · 图像生成
default           默认上游的默认助手
```

### 文本

```bash
curl http://localhost:PORT/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"doubao/default","messages":[{"role":"user","content":"你好"}]}'
```

### 生图

```bash
curl http://localhost:PORT/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"doubao/image","messages":[{"role":"user","content":"一只橘猫"}]}'
```

返回 OpenAI 多模态格式：

```json
{
  "choices": [{
    "message": {
      "role": "assistant",
      "content": [
        {"type": "text", "text": "已生成图片（Seedream 5.0 Flash）2048×2048"},
        {"type": "image_url", "image_url": {"url": "https://..."}}
      ]
    }
  }],
  "media": [{
    "url": "https://...", "type": "image", "model": "Seedream 5.0 Flash",
    "width": 2048, "height": 2048,
    "key": "tos-cn-i-.../rc_gen_image/<hash>.jpeg"
  }]
}
```

图片 URL 是**无水印原图**的签名直链。签名有时效，`media[].key` 是稳定标识，可再次换取 URL。

---

## 技术说明

### 两个上游只差四个字段

豆包与 Dola 运行同一套 IM 协议，差异被收敛成一张表：

| | doubao | dola |
|---|---|---|
| 站点 | `www.doubao.com` | `www.dola.com` |
| aid | `497858` | `495671` |
| region | `CN` | `JP` |
| Cookie 域 | `.doubao.com` | `.dola.com` |

因此一个执行器服务两个上游。注意**同一账号不能跨区**：用豆包账号打 Dola 会返回 `710022003 CountryRestricted`。

### 关键协议细节

1. **`Content-Type` 必须是 `application/json; encoding=utf-8`**
   参数名是 `encoding`，不是标准的 `charset`。网关在解析 body **之前**校验它，用 `charset` 一定返回 `712012002 不支持编码类型`。

2. **消息用 `content_block` 数组，不是 `content` 字符串**
   文本放在 `content_block[0].content.text_block.text`。用 `content` 字符串会被当成普通聊天 —— 这正是"要它画图它却只描述图片"的原因。

3. **`option.aggregate_params.mode_id` 必须为 `"1"`**

4. **`conversation_id` 必须是真实会话 ID**
   传 `"0"` 会返回 `710020202`。插件通过 `/im/chain/recent_conv` 自动获取。

5. **不需要 `a_bogus` / `msToken` 签名**
   带上从别处复制的签名反而会触发人机验证 —— 签名与设备指纹绑定，跨会话复用必然不匹配。

### 请求链路

```
/v1/chat/completions
  └ 插件执行器
      └ POST /chat/completion        (SSE)
          ├ STREAM_MSG_NOTIFY        整条消息
          ├ STREAM_CHUNK             patch_op 增量
          ├ CHUNK_DELTA              文本增量
          └ SSE_REPLY_END
      └ 生图时：block_type 2074 (CREATION)
          └ POST /creativity/resource/get_without_watermark  → 高清原图
```

---

## 限制

- **不能设置 system prompt / temperature / top_p** —— 上游是聊天客户端，不是补全 API。多条消息会被折叠成一个提示词。
- **无 token 计量** —— 上游不返回用量，`usage` 是估算值（标有 `"estimated": true`）。
- **流式不是逐 token** —— 上游确实流式推送，但媒体结果是整块到达的。
- **每请求一个新会话语义** —— 插件复用账号最近会话，但每次请求都替换其内容，不读取历史。副作用是该会话在上游 UI 中会累积记录。
- **生图/生视频消耗账号额度**。
- 图片 URL 是签名直链，**有时效**。

---

## 开发

### 测试

```bash
CGO_ENABLED=0 go test ./...            # 单元测试（离线）
```

实时测试需要账号，默认跳过：

```bash
export DOUBAO_LIVE_COOKIES='sessionid=...; flow_cur_user_sec_id=...'
export DOUBAO_LIVE_REALM=doubao        # 或 dola
CGO_ENABLED=0 go test -run TestLiveLaunch ./...
CGO_ENABLED=0 go test -run TestLiveChatRoundTrip ./...

# 生图会消耗额度，需显式开启
DOUBAO_LIVE_IMAGE=1 CGO_ENABLED=0 go test -run TestLiveImageGeneration ./...
```

### 代码结构

```
cabi.go               C ABI 入口（cgo trampoline）
rpc.go                CPA RPC 方法路由
realm.go              双上游抽象
credentials.go        凭据模型与 Cookie 解析
completion.go         /chat/completion SSE 客户端
conversation.go       流式执行器（文本 + 媒体）
creation.go           CREATION block 解析
watermark.go          无水印原图解析
conversation_list.go  会话列表 → conversation_id
executor.go           执行器：OpenAI ↔ 上游
model_provider.go     模型目录
auth_provider.go      授权流程
management.go         管理面板
```

### 协议来源

协议来自对客户端 APK 的反编译与对线上接口的实测，关键依据：

```
ov2/a.java                                  HTTP 传输 + Content-Type
com/larus/im/internal/protocol/bean/        IM 信封与命令
com/larus/im/bean/message/block/BlockType   block 类型枚举
com/larus/im/bean/message/ChatAbilityType   技能枚举
com/larus/canvas/service/bean/              创作块结构
```

---

## 致谢

- [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) —— 插件宿主
- [workbuddy-cpa-plugin](https://github.com/Lxapk/workbuddy-cpa-plugin) —— 架构参考

## 许可

仅供个人学习与研究使用。使用本插件需自行承担账号风险，请遵守相关服务条款。
