package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
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
	chat.Surface = surfaceOf(&req)
	chat.RequestID = videoJobIDFromPayload(req.Payload)
	return &req, creds, chat, nil
}

// The three execution surfaces CPA can route here, as named in the host's
// executor request. A plugin that declares a format it cannot answer simply never
// receives it, so the dispatch below and the capability declaration must agree.
const (
	surfaceChat  = "chat"
	surfaceImage = "image"
	surfaceVideo = "video"
)

// surfaceOf maps the host's request format to the surface being served.
//
// CPA hands over "chat-completions" for the chat endpoint, "openai-image" for
// /v1/images/generations and "openai-video" for /v1/videos. The formats arrive
// exactly as declared in executor_input_formats, with unknown spellings falling
// back to chat so a host that names a new surface does not silently break text.
func surfaceOf(req *pluginapi.ExecutorRequest) string {
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "" {
		format = strings.ToLower(strings.TrimSpace(req.SourceFormat))
	}
	switch format {
	case "openai-image", "image", "images", "images-generations":
		return surfaceImage
	case "openai-video", "video", "videos":
		return surfaceVideo
	}
	return surfaceChat
}

// videoJobIDFromPayload recognises a status poll.
//
// CPA's video retrieve endpoint re-enters the executor with {"request_id": …} and
// no prompt. Treating that as a generation would produce a new clip on every
// poll, so the id is what tells the two apart.
func videoJobIDFromPayload(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var probe struct {
		RequestID string `json:"request_id"`
		ID        string `json:"id"`
	}
	if errUnmarshal := json.Unmarshal(payload, &probe); errUnmarshal != nil {
		return ""
	}
	if id := strings.TrimSpace(probe.RequestID); id != "" {
		return id
	}
	return strings.TrimSpace(probe.ID)
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
//
// The same struct carries all three surfaces: the chat endpoint sends messages,
// /v1/images/generations sends a prompt, and /v1/videos sends a prompt with a
// duration and an aspect ratio. Reading them together keeps one decoder instead
// of three that would drift as fields are added.
type chatRequest struct {
	Model    string          `json:"model"`
	Messages json.RawMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	// N is accepted for image requests where a client asks for several images.
	N int `json:"n"`
	// Prompt is the image and video endpoints' input text.
	Prompt string `json:"prompt"`
	// Size / AspectRatio / Seconds are video request hints. The upstream decides
	// the real values, so these are advisory and only logged.
	Size        string `json:"size"`
	AspectRatio string `json:"aspect_ratio"`
	Seconds     int    `json:"seconds"`
	// ResponseFormat selects url or b64_json on the image endpoint.
	ResponseFormat string `json:"response_format"`
	// RequestID is set by a video status poll: it names a job this plugin already
	// finished, not a new generation.
	RequestID string `json:"request_id"`
	// Surface records which endpoint produced the request.
	Surface string `json:"-"`
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
	return out, nil
}

// requireMessages rejects a chat request that carries no conversation.
//
// The image and video endpoints legitimately have no messages, so this check
// belongs to the chat surface rather than to the decoder.
func (r *chatRequest) requireMessages() error {
	if len(r.Messages) == 0 {
		return errors.New("请求中没有 messages")
	}
	return nil
}

// promptText returns the text to send upstream for whichever surface arrived.
//
// Chat folds its transcript into one prompt; the media endpoints send their
// prompt field as-is, because a generation has no turns to preserve.
func (r *chatRequest) promptText() (string, error) {
	if r.Surface == surfaceChat {
		if err := r.requireMessages(); err != nil {
			return "", err
		}
		return buildPrompt(r.Messages)
	}
	return strings.TrimSpace(r.Prompt), nil
}

// executorExecute answers executor.execute (non-streaming).
func executorExecute(request []byte) ([]byte, error) {
	_, creds, chat, errDecode := decodeExecutorRequest(request)
	if errDecode != nil {
		return nil, errDecode
	}

	// Each surface answers in its own OpenAI shape, which is what the caller's
	// endpoint promised: a chat completion, an images object, or a video job.
	var payload map[string]any
	switch chat.Surface {
	case surfaceImage:
		resp, errRun := executeImageOnce(creds, chat)
		if errRun != nil {
			return nil, errRun
		}
		payload = resp
	case surfaceVideo:
		// The video endpoint answers with a job and expects the client to poll it.
		// This upstream generates synchronously, so the finished asset is returned
		// as a completed job rather than a pending one.
		resp, errRun := executeVideoOnce(creds, chat)
		if errRun != nil {
			return nil, errRun
		}
		payload = resp
	default:
		resp, errRun := executeOnce(creds, chat, nil)
		if errRun != nil {
			return nil, errRun
		}
		payload = resp
	}

	raw, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return okEnvelope(pluginapi.ExecutorResponse{
		Payload: raw,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	})
}

// executeImageOnce answers /v1/images/generations.
//
// The body is the images shape — {"created":…,"data":[{"url":…}]} — not a chat
// completion: returning a chat body here leaves an images client with nothing it
// can parse. CPA reads data[].url or data[].b64_json and rebuilds the response it
// promised, so an upstream URL is the only thing that has to be right.
func executeImageOnce(creds *credentials, chat *chatRequest) (map[string]any, error) {
	resp, assets, errRun := executeOnceWithMedia(creds, chat, nil)
	if errRun != nil {
		return nil, errRun
	}
	_ = resp

	items := make([]map[string]any, 0, len(assets))
	for _, asset := range assets {
		if asset.URL == "" || asset.isVideo() {
			continue
		}
		items = append(items, map[string]any{"url": asset.URL})
	}
	if len(items) == 0 {
		return nil, errors.New("上游没有返回图片")
	}

	out := map[string]any{
		"created": time.Now().Unix(),
		"data":    items,
	}
	if usage, ok := resp["usage"]; ok {
		out["usage"] = usage
	}
	return out, nil
}

// executeVideoOnce answers /v1/videos.
//
// CPA's video endpoint returns a job object that a client polls by id; this
// upstream has no asynchronous job, so the finished clip is reported as a
// completed job with its URL already filled in. A client that polls still works
// — the same id keeps answering "completed" — and a client that reads the URL
// directly gets it immediately.
func executeVideoOnce(creds *credentials, chat *chatRequest) (map[string]any, error) {
	// A status poll names a job that already finished. Answering it from the store
	// is what keeps one generation from becoming one per poll.
	if chat.RequestID != "" {
		if job, ok := videoJobs.get(chat.RequestID); ok {
			return videoJobPayload(chat.RequestID, job), nil
		}
		return nil, fmt.Errorf("找不到视频任务 %s：结果只保存在内存中，重启后需要重新生成", chat.RequestID)
	}

	_, assets, errRun := executeOnceWithMedia(creds, chat, nil)
	if errRun != nil {
		return nil, errRun
	}

	var video creationAsset
	for _, asset := range assets {
		if asset.URL != "" && asset.isVideo() {
			video = asset
			break
		}
	}
	if video.URL == "" {
		// Some captures report a clip without the video flag; a URL that is not a
		// still image is still the answer.
		for _, asset := range assets {
			if asset.URL != "" {
				video = asset
				break
			}
		}
	}
	if video.URL == "" {
		return nil, errors.New("上游没有返回视频")
	}

	job := newVideoJobID()
	videoJobs.putWithModel(job, video.URL, chat.Model)

	payload := videoJobPayload(job, videoJob{URL: video.URL, Model: chat.Model, CreatedAt: time.Now()})
	seconds := chat.Seconds
	if seconds <= 0 {
		seconds = 4
	}
	// The duration is advisory: the upstream decides the real clip length, so the
	// requested value is echoed rather than measured.
	payload["seconds"] = fmtInt64(int64(seconds))
	return payload, nil
}

// Each payload is an OpenAI chat.completion.chunk object without SSE framing: the
// host's stream writer prepends "data: " and appends "\n\n", then closes the
// stream with "data: [DONE]" itself. Sending pre-framed text here would put
// "data: data: {...}" on the wire and break every client's parser.
func executorExecuteStream(request []byte) ([]byte, error) {
	_, creds, chat, errDecode := decodeExecutorRequest(request)
	if errDecode != nil {
		return nil, errDecode
	}
	// Only the chat surface streams. The media endpoints answer with one object,
	// so a stream request that arrived for them is a routing mistake rather than
	// something to emulate with a single chunk.
	if chat.Surface != surfaceChat {
		return nil, fmt.Errorf("%s 端点不支持流式响应", chat.Surface)
	}

	// The RPC is request/response rather than an open channel, so deltas cannot be
	// handed to the host as they arrive. They are accumulated and turned into
	// finished chunk frames below.
	var (
		mu       sync.Mutex
		delta    strings.Builder
		reasoned strings.Builder
	)

	emit := func(piece string) {
		if piece == "" {
			return
		}
		mu.Lock()
		delta.WriteString(piece)
		mu.Unlock()
	}

	result, media, errRun := executeStreamExchange(creds, chat, emit)
	if errRun != nil {
		return nil, errRun
	}

	mu.Lock()
	full := delta.String()
	thinking := reasoned.String()
	mu.Unlock()

	// A reasoning model streams its thinking before the answer, and the exchange
	// already separates the two: reuse that split rather than draining a second
	// channel the upstream does not label.
	if result != nil && result.reasoning != "" {
		thinking = result.reasoning
	}

	frames := buildStreamFrames(chat.Model, full, thinking, media)
	wire := make([]pluginapi.ExecutorStreamChunk, 0, len(frames))
	for _, frame := range frames {
		wire = append(wire, pluginapi.ExecutorStreamChunk{Payload: frame})
	}
	return okEnvelope(map[string]any{
		"headers": map[string][]string{"Content-Type": {"text/event-stream"}},
		"chunks":  wire,
	})
}

// executeStreamExchange runs one turn for the streaming path.
//
// It is executeOnceWithMedia with the delta callback kept separate: the panel must
// count streaming calls too, and the callback has to survive the wrapper.
func executeStreamExchange(creds *credentials, chat *chatRequest, onDelta func(string)) (*conversationResult, []creationAsset, error) {
	started := time.Now()
	mode := modeForModel(chat.Model)

	result, errRun := executeExchangeResult(creds, chat, onDelta)
	if errRun != nil {
		state.logs.add(callEntry{
			Account:  creds.label(),
			Model:    chat.Model,
			Realm:    string(creds.Realm),
			Kind:     mode.Kind,
			Status:   "error",
			Duration: time.Since(started).Milliseconds(),
			Detail:   errRun.Error(),
		})
		stats.noteFailure(creds, errRun.Error(), classifyFailure(errRun))
		return nil, nil, errRun
	}

	state.logs.add(callEntry{
		Account:  creds.label(),
		Model:    chat.Model,
		Realm:    string(creds.Realm),
		Kind:     mode.Kind,
		Status:   "ok",
		Duration: time.Since(started).Milliseconds(),
	})
	stats.noteSuccess(creds)
	if key := statsKey(creds); key != "" {
		rotation.remember(key)
	}
	return result, result.assets, nil
}

// buildStreamFrames renders one finished answer as chat.completion.chunk frames.
//
// The text is cut into fixed slices rather than emitted as a single frame: a long
// answer sent at once still works, but clients that render as they read lose the
// effect of streaming. Reasoning, when present, goes first as reasoning_content.
func buildStreamFrames(model, text, reasoning string, media []creationAsset) [][]byte {
	frames := make([][]byte, 0, 8)
	appendDelta := func(delta map[string]any) {
		frame, errMarshal := json.Marshal(streamChunk(model, "", delta, ""))
		if errMarshal == nil {
			frames = append(frames, frame)
		}
	}

	if reasoning != "" {
		appendDelta(map[string]any{"reasoning_content": reasoning})
	}

	const step = 120
	runes := []rune(text)
	for idx := 0; idx < len(runes); idx += step {
		end := idx + step
		if end > len(runes) {
			end = len(runes)
		}
		appendDelta(map[string]any{"content": string(runes[idx:end])})
	}

	// Generated media cannot ride in a text delta, so it gets its own frames plus a
	// text part carrying the URL.
	for _, asset := range media {
		if asset.URL == "" {
			continue
		}
		delta := map[string]any{"media": []map[string]any{mediaItem(asset)}}
		if asset.isVideo() {
			delta["content"] = "[video] " + asset.URL
		} else {
			delta["content"] = ""
		}
		appendDelta(delta)
	}

	final, errFinal := json.Marshal(streamChunk(model, "", map[string]any{}, "stop"))
	if errFinal == nil {
		frames = append(frames, final)
	}
	return frames
}

// executeOnce performs one exchange and returns the OpenAI response object.
func executeOnce(creds *credentials, chat *chatRequest, onDelta func(string)) (map[string]any, error) {
	resp, _, err := executeOnceWithMedia(creds, chat, onDelta)
	return resp, err
}

// The streaming path needs the assets separately because it emits them as their
// own frames rather than reading them back out of the response object.
//
// One exchange is also the unit the panel reports on: the elapsed time, the
// account and the outcome are recorded here so both the text and the streaming
// paths stay accountable without duplicating the bookkeeping.
func executeOnceWithMedia(creds *credentials, chat *chatRequest, onDelta func(string)) (map[string]any, []creationAsset, error) {
	started := time.Now()
	mode := modeForModel(chat.Model)

	resp, assets, errRun := executeExchange(creds, chat, onDelta)
	if errRun != nil {
		state.logs.add(callEntry{
			Account:  creds.label(),
			Model:    chat.Model,
			Realm:    string(creds.Realm),
			Kind:     mode.Kind,
			Status:   "error",
			Duration: time.Since(started).Milliseconds(),
			Detail:   errRun.Error(),
		})
		stats.noteFailure(creds, errRun.Error(), classifyFailure(errRun))
		return nil, nil, errRun
	}

	state.logs.add(callEntry{
		Account:  creds.label(),
		Model:    chat.Model,
		Realm:    string(creds.Realm),
		Kind:     mode.Kind,
		Status:   "ok",
		Duration: time.Since(started).Milliseconds(),
	})
	stats.noteSuccess(creds)
	if key := statsKey(creds); key != "" {
		rotation.remember(key)
	}
	return resp, assets, nil
}

// executeExchange is the upstream turn itself, with no bookkeeping.
func executeExchange(creds *credentials, chat *chatRequest, onDelta func(string)) (map[string]any, []creationAsset, error) {
	settings := state.settings.get()

	// A realm prefix selects the upstream; otherwise the credential's own realm
	// is used, and only failing that the configured default.
	targetRealm, _ := resolveUpstreamModel(chat.Model)
	if creds.Realm == "" {
		creds.Realm = targetRealm
	}

	prompt, errPrompt := chat.promptText()
	if errPrompt != nil {
		return nil, nil, errPrompt
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, nil, errors.New("请求中没有可发送的文本内容")
	}

	// The model alias decides whether this is chat or a generation.
	mode := modeForRequest(chat)

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

// executeExchangeResult is executeExchange for the streaming path: the caller
// needs the assembled text and thinking alongside the media, which the response
// object hides inside choices[0].
func executeExchangeResult(creds *credentials, chat *chatRequest, onDelta func(string)) (*conversationResult, error) {
	settings := state.settings.get()

	targetRealm, _ := resolveUpstreamModel(chat.Model)
	if creds.Realm == "" {
		creds.Realm = targetRealm
	}

	prompt, errPrompt := chat.promptText()
	if errPrompt != nil {
		return nil, errPrompt
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("请求中没有可发送的文本内容")
	}

	mode := modeForRequest(chat)
	budget := settings.ReplyPollSeconds
	if mode.Skill > 0 {
		budget = settings.MediaPollSeconds
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(budget+30)*time.Second)
	defer cancel()

	conversationID := ""
	needCreate := false
	if id, errConv := newClient(creds).latestConversationID(ctx); errConv == nil && id != "" {
		conversationID = id
	} else {
		if errConv != nil {
			logInfo("doubao: 获取会话列表失败：" + errConv.Error())
		}
		needCreate = true
	}

	result, errRun := runCompletionWith(ctx, creds, prompt, mode, conversationID, needCreate, onDelta)
	if errRun != nil {
		return nil, errRun
	}
	if strings.TrimSpace(result.text) == "" && len(result.assets) > 0 {
		result.text = describeAssets(result.assets)
	}
	return result, nil
}

// modeForRequest picks the generation mode from the surface and the alias.
//
// The endpoint is authoritative: /v1/images/generations is a picture request even
// when the model name is a bare one, because the client already chose the image
// surface. The alias then decides which capability the upstream should run.
func modeForRequest(r *chatRequest) generationMode {
	switch r.Surface {
	case surfaceImage:
		return generationMode{Skill: skillImageGen, Kind: kindImage}
	case surfaceVideo:
		return generationMode{Skill: skillVideoGen, Kind: kindVideo}
	}
	return modeForModel(r.Model)
}

// modeForModel maps a client model name to a generation mode.
func modeForModel(model string) generationMode {
	skill := skillForModel(model)
	if skill == 0 {
		return generationMode{Skill: 0, Kind: "text", DeepThink: deepThinkForModel(model)}
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

// mediaItem renders one generated asset for the media extension field.
//
// The OpenAI content array cannot carry dimensions, the generator name or the
// resource key, so those travel in this structured form alongside the URL.
func mediaItem(a creationAsset) map[string]any {
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
	return item
}

// failureKind classifies a failed call so the statistics can tell an expired
// session apart from a transient network blip.
type failureKind int

const (
	failureTransient failureKind = iota
	failureAuth
	failureRate
)

// classifyFailure attributes a failure to the most useful category.
//
// The panel shows "需重新授权" only for an auth failure, so everything that is
// merely slow or throttled has to be classified as transient — otherwise one
// timeout would make a healthy account look broken until it is called again.
func classifyFailure(err error) failureKind {
	if err == nil {
		return failureTransient
	}
	var up *upstreamError
	if errors.As(err, &up) {
		switch {
		case up.isAuthFailure():
			return failureAuth
		case up.isRateLimit():
			return failureRate
		}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "登录态"), strings.Contains(msg, "logininvalid"),
		strings.Contains(msg, "重新授权"), strings.Contains(msg, "cookie"):
		return failureAuth
	case strings.Contains(msg, "限流"), strings.Contains(msg, "人机验证"),
		strings.Contains(msg, "too many requests"):
		return failureRate
	}
	return failureTransient
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
