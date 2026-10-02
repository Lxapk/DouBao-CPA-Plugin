package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Execution: OpenAI request in, OpenAI response out.
//
// The upstream is a chat client on the web, so the translation is not one to
// one. What it can do:
//
//   - answer a prompt (POST /chat/completion, streamed)
//   - generate an image or a video when the matching skill is selected
//
// What it cannot do: accept a system prompt, honour temperature, or replay a
// caller-supplied transcript. The conversation history belongs to the server, so
// a multi-turn OpenAI request is folded into one prompt.
//
// The host hands over an ExecutorRequest: the credential arrives in StorageJSON
// and the OpenAI body in Payload. The answer goes back in ExecutorResponse.
// Payload as raw OpenAI JSON, which the host forwards untouched.

// executorIdentifier answers executor.identifier.
func executorIdentifier() ([]byte, error) {
	return okEnvelope(identifierResponse{Identifier: pluginName})
}

// decodeExecutorRequest pulls the credential and chat request apart.
func decodeExecutorRequest(request []byte) (*pluginapi.ExecutorRequest, *credentials, *chatRequest, error) {
	var req pluginapi.ExecutorRequest
	if len(request) > 0 {
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return nil, nil, nil, errUnmarshal
		}
	}

	creds := credentialsFromExecution(&req)
	if creds == nil {
		return nil, nil, nil, errors.New("没有可用的账号：请先在插件面板中授权豆包或 Dola 账号")
	}
	if errValidate := creds.validate(); errValidate != nil {
		return nil, nil, nil, errValidate
	}

	// The realm may also be stated in the auth attributes, which are written when
	// the credential is created and survive a storage round trip.
	if attrs := req.AuthAttributes; len(attrs) > 0 {
		if r := normalizeRealm(attrs["realm"]); r != "" {
			creds.Realm = r
		} else if r := normalizeRealm(attrs["provider"]); r != "" {
			creds.Realm = r
		}
	}

	chat, errChat := decodeChatRequest(req.Payload, req.Model)
	if errChat != nil {
		return nil, nil, nil, errChat
	}
	return &req, creds, chat, nil
}

// credentialsFromExecution finds the credential in an execution request.
func credentialsFromExecution(req *pluginapi.ExecutorRequest) *credentials {
	if len(req.StorageJSON) > 0 {
		if creds, err := credentialsFromStorage(req.StorageJSON); err == nil {
			return creds
		}
	}
	// Fall back to the auth metadata, which some host wirings populate instead.
	if len(req.AuthMetadata) > 0 {
		if raw, errMarshal := json.Marshal(req.AuthMetadata); errMarshal == nil {
			if creds, err := credentialsFromStorage(raw); err == nil {
				return creds
			}
		}
	}
	return nil
}

// chatRequest is the subset of the OpenAI body this plugin reads.
type chatRequest struct {
	Model    string          `json:"model"`
	Messages json.RawMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	// N is accepted for image requests where a client asks for several images.
	N int `json:"n"`
}

func decodeChatRequest(payload []byte, fallbackModel string) (*chatRequest, error) {
	out := &chatRequest{}
	if len(payload) > 0 {
		if errUnmarshal := json.Unmarshal(payload, out); errUnmarshal != nil {
			return nil, fmt.Errorf("解析请求体失败: %w", errUnmarshal)
		}
	}
	if out.Model == "" {
		out.Model = fallbackModel
	}
	if len(out.Messages) == 0 {
		return nil, errors.New("请求中没有 messages")
	}
	return out, nil
}

// executorExecute answers executor.execute (non-streaming).
func executorExecute(request []byte) ([]byte, error) {
	_, creds, chat, errDecode := decodeExecutorRequest(request)
	if errDecode != nil {
		return nil, errDecode
	}

	resp, errRun := executeOnce(creds, chat, nil)
	if errRun != nil {
		return nil, errRun
	}

	raw, errMarshal := json.Marshal(resp)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return okEnvelope(pluginapi.ExecutorResponse{
		Payload: raw,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	})
}

// executorExecuteStream answers executor.execute_stream.
//
// The upstream streams the answer, so the chunks are forwarded as they arrive
// rather than buffered. Each chunk is an OpenAI chat.completion.chunk frame; the
// host adds the SSE framing.
func executorExecuteStream(request []byte) ([]byte, error) {
	_, creds, chat, errDecode := decodeExecutorRequest(request)
	if errDecode != nil {
		return nil, errDecode
	}

	// Deltas are collected here and handed to the host after the call, because
	// the RPC is request/response rather than an open channel.
	var chunks [][]byte

	emit := func(delta string) {
		frame, errMarshal := json.Marshal(streamChunk(chat.Model, "", map[string]any{"content": delta}, ""))
		if errMarshal == nil {
			chunks = append(chunks, frame)
		}
	}

	_, media, errRun := executeOnceWithMedia(creds, chat, emit)
	if errRun != nil {
		return nil, errRun
	}

	// Media cannot be streamed as a delta: it arrives as one assembled block, so
	// the dedicated final frames carry it.
	if len(chunks) == 0 {
		final, errFinal := finalStreamFrames(chat.Model, media)
		if errFinal != nil {
			return nil, errFinal
		}
		chunks = final
	}

	wire := make([]pluginapi.ExecutorStreamChunk, 0, len(chunks))
	for _, c := range chunks {
		wire = append(wire, pluginapi.ExecutorStreamChunk{Payload: c})
	}
	return okEnvelope(map[string]any{
		"headers": map[string][]string{"Content-Type": {"text/event-stream"}},
		"chunks":  wire,
	})
}

// executeOnce performs one exchange and returns the OpenAI response object.
func executeOnce(creds *credentials, chat *chatRequest, onDelta func(string)) (map[string]any, error) {
	resp, _, err := executeOnceWithMedia(creds, chat, onDelta)
	return resp, err
}

// The streaming path needs the assets separately because it emits them as their
// own frames rather than reading them back out of the response object.
func executeOnceWithMedia(creds *credentials, chat *chatRequest, onDelta func(string)) (map[string]any, []creationAsset, error) {
	settings := state.settings.get()

	// A realm prefix selects the upstream; otherwise the credential's own realm
	// is used, and only failing that the configured default.
	targetRealm, _ := resolveUpstreamModel(chat.Model)
	if creds.Realm == "" {
		creds.Realm = targetRealm
	}

	prompt, errPrompt := buildPrompt(chat.Messages)
	if errPrompt != nil {
		return nil, nil, errPrompt
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, nil, errors.New("请求中没有可发送的文本内容")
	}

	// The model alias decides whether this is chat or a generation.
	mode := modeForModel(chat.Model)

	budget := settings.ReplyPollSeconds
	if mode.Skill > 0 {
		// A generation takes much longer than an answer.
		budget = settings.MediaPollSeconds
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(budget+30)*time.Second)
	defer cancel()

	// The conversation id must name a real thread: the endpoint rejects a
	// placeholder with a generic param error. The account's most recent
	// conversation is reused, which is safe because every request replaces its
	// contents rather than reading prior turns back.
	conversationID := ""
	needCreate := false
	if id, errConv := newClient(creds).latestConversationID(ctx); errConv == nil && id != "" {
		conversationID = id
	} else {
		if errConv != nil {
			logInfo("doubao: 获取会话列表失败：" + errConv.Error())
		}
		// With no existing thread the server can be asked to allocate one.
		needCreate = true
	}

	started := time.Now()
	result, errRun := runCompletionWith(ctx, creds, prompt, mode, conversationID, needCreate, onDelta)
	if errRun != nil {
		return nil, nil, errRun
	}

	text := result.text
	// An image reply has no text of its own. Give the caller a description so
	// the answer is not an empty message alongside the media.
	if strings.TrimSpace(text) == "" && len(result.assets) > 0 {
		text = describeAssets(result.assets)
	}

	resp := buildChatCompletion(chat.Model, text, result.reasoning, result.conversationID, started)
	attachCreationAssets(resp, result.assets)
	resp["usage"] = estimateUsage(prompt, text)
	return resp, result.assets, nil
}

// modeForModel maps a client model name to a generation mode.
func modeForModel(model string) generationMode {
	skill := skillForModel(model)
	if skill == 0 {
		return generationMode{Skill: 0, Kind: "text"}
	}
	name, _ := stripModelPrefix(normalizeModelName(model))
	kind := "image"
	if strings.EqualFold(name, "video") {
		kind = "video"
	}
	return generationMode{Skill: skill, Kind: kind}
}

// attachCreationAssets converts generated media into OpenAI multimodal content.
//
// A text-only reply keeps a plain string content, which is what every existing
// client expects. Only when media is present does content become the array form,
// because switching unconditionally would change the shape of ordinary replies.
func attachCreationAssets(resp map[string]any, assets []creationAsset) {
	if len(assets) == 0 {
		return
	}
	choices, ok := resp["choices"].([]map[string]any)
	if !ok || len(choices) == 0 {
		return
	}
	msg, ok := choices[0]["message"].(map[string]any)
	if !ok {
		return
	}

	parts := make([]map[string]any, 0, len(assets)+1)
	if text, isStr := msg["content"].(string); isStr && text != "" {
		parts = append(parts, map[string]any{"type": "text", "text": text})
	}
	for _, a := range assets {
		if a.URL == "" {
			continue
		}
		if a.isVideo() {
			// OpenAI has no video content part. The URL goes in a text part so
			// the client can still surface it, and the structured form is
			// available in the media extension.
			parts = append(parts, map[string]any{"type": "text", "text": "[video] " + a.URL})
			continue
		}
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": a.URL},
		})
	}
	msg["content"] = parts

	// The structured list is exposed because the OpenAI content array cannot
	// carry dimensions, the generator name, or the resource key.
	raw := make([]map[string]any, 0, len(assets))
	for _, a := range assets {
		item := map[string]any{"url": a.URL, "type": "image"}
		if a.isVideo() {
			item["type"] = "video"
		}
		if a.Key != "" {
			item["key"] = a.Key
		}
		if a.Model != "" {
			item["model"] = a.Model
		}
		if a.Width > 0 {
			item["width"] = a.Width
		}
		if a.Height > 0 {
			item["height"] = a.Height
		}
		if len(a.Kinds) > 0 {
			item["formats"] = a.Kinds
		}
		raw = append(raw, item)
	}
	resp["media"] = raw
}

// finalStreamFrames renders generated media as stream frames.
//
// A generation produces no text delta, so without this the stream would be empty
// even though the answer succeeded. Images go out as an image_url content part;
// a video has no OpenAI representation and is emitted as a URL in a text part,
// with the structured form in the media extension.
func finalStreamFrames(model string, assets []creationAsset) ([][]byte, error) {
	var out [][]byte
	appendFrame := func(v any) error {
		raw, errMarshal := json.Marshal(v)
		if errMarshal != nil {
			return errMarshal
		}
		out = append(out, raw)
		return nil
	}

	if len(assets) == 0 {
		return out, nil
	}

	for _, a := range assets {
		if a.URL == "" {
			continue
		}
		item := map[string]any{"url": a.URL, "type": "image"}
		if a.isVideo() {
			item["type"] = "video"
		}
		if a.Key != "" {
			item["key"] = a.Key
		}
		if a.Model != "" {
			item["model"] = a.Model
		}
		if a.Width > 0 {
			item["width"] = a.Width
		}
		if a.Height > 0 {
			item["height"] = a.Height
		}

		delta := map[string]any{"media": []map[string]any{item}}
		if a.isVideo() {
			delta["content"] = "[video] " + a.URL
		} else {
			delta["content"] = ""
		}
		if err := appendFrame(streamChunk(model, "", delta, "")); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// wrapExecutorError turns an upstream failure into an actionable message.
func wrapExecutorError(err error) error {
	var up *upstreamError
	if errors.As(err, &up) {
		if up.isAuthFailure() {
			return fmt.Errorf("账号登录态已失效，请重新授权：%w", err)
		}
		if up.isRateLimit() {
			return fmt.Errorf("上游限流或触发人机验证，请稍后重试：%w", err)
		}
	}
	return err
}

// buildPrompt collapses the OpenAI message list into one upstream prompt.
//
// The upstream owns the conversation history, so the caller's transcript cannot
// be replayed as separate turns. System and developer instructions are folded in
// as leading context; the remaining turns are rendered as a labelled transcript
// so the model can still tell who said what.
func buildPrompt(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var msgs []chatMessage
	if errUnmarshal := json.Unmarshal(raw, &msgs); errUnmarshal != nil {
		return "", fmt.Errorf("解析 messages 失败: %w", errUnmarshal)
	}
	if len(msgs) == 0 {
		return "", nil
	}

	var system []string
	type turn struct{ role, text string }
	var turns []turn

	for _, m := range msgs {
		text := strings.TrimSpace(m.text())
		if text == "" {
			continue
		}
		switch strings.ToLower(m.Role) {
		case "system", "developer":
			system = append(system, text)
		case "assistant":
			turns = append(turns, turn{"assistant", text})
		case "tool":
			// Tool output is folded in as a user-visible note: the upstream has
			// no tool protocol, so presenting it as context is the only way the
			// model can see it.
			turns = append(turns, turn{"user", "工具返回：\n" + text})
		default:
			turns = append(turns, turn{"user", text})
		}
	}

	// A single user turn is the common case and needs no framing at all.
	if len(system) == 0 && len(turns) == 1 && turns[0].role == "user" {
		return turns[0].text, nil
	}

	var b strings.Builder
	if len(system) > 0 {
		b.WriteString("【系统指令】\n")
		b.WriteString(strings.Join(system, "\n\n"))
		b.WriteString("\n\n")
	}
	if len(turns) > 1 || len(system) > 0 {
		b.WriteString("【对话记录】\n")
	}
	for _, t := range turns {
		if t.role == "assistant" {
			b.WriteString("助手：")
		} else {
			b.WriteString("用户：")
		}
		b.WriteString(t.text)
		b.WriteString("\n")
	}
	out := strings.TrimSpace(b.String())
	if out == "" && len(turns) > 0 {
		return turns[len(turns)-1].text, nil
	}
	return out, nil
}
