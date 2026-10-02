package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// client performs IM calls for one credential.
//
// Every call is an independent HTTP POST carrying a JSON envelope. There is no
// session object to hold and no handshake to keep alive, which is what lets the
// plugin do all its work with the host's HTTP transport.
type client struct {
	creds *credentials
	http  *http.Client
	// debug, when set, is called with request/response summaries.
	debug func(string, ...any)
}

func newClient(creds *credentials) *client {
	return &client{
		creds: creds.withDefaults(),
		http: &http.Client{
			Timeout: 90 * time.Second,
		},
	}
}

func (c *client) logf(format string, args ...any) {
	if c.debug != nil {
		c.debug(format, args...)
	}
}

// postBytes posts a pre-encoded body and returns the raw response.
//
// It exists for endpoints that answer with a bare JSON object rather than the
// command envelope, where decoding into downlinkMessage would lose the payload.
func (c *client) postBytes(ctx context.Context, path string, body []byte) ([]byte, error) {
	url := endpointURL(c.creds, path, nil)
	req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if errReq != nil {
		return nil, errReq
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", c.creds.sanitizedCookies())
	req.Header.Set("Origin", c.creds.profile().Host)
	req.Header.Set("Referer", c.creds.profile().Host+"/chat/")
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Agw-Js-Conv", "str")

	resp, errDo := c.http.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("请求上游失败: %w", errDo)
	}
	defer resp.Body.Close()

	payload, errRead := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if errRead != nil {
		return nil, errRead
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &upstreamError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode),
			Body:       payload,
		}
	}
	return payload, nil
}

// post sends one envelope and decodes the response envelope.
func (c *client) post(ctx context.Context, path string, body any, extraQuery map[string]string) (*downlinkMessage, error) {
	raw, errMarshal := json.Marshal(body)
	if errMarshal != nil {
		return nil, fmt.Errorf("编码请求失败: %w", errMarshal)
	}

	url := endpointURL(c.creds, path, extraQuery)
	req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if errReq != nil {
		return nil, errReq
	}

	// The Content-Type is load-bearing. The gateway validates it before parsing
	// the body and answers 712012002 "不支持编码类型" to anything but this exact
	// value — including the conventional "charset=UTF-8" spelling.
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Cookie", c.creds.sanitizedCookies())
	req.Header.Set("Origin", c.creds.profile().Host)
	req.Header.Set("Referer", c.creds.profile().Host+"/chat/")
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Agw-Js-Conv", "str")

	c.logf("→ POST %s body=%s", path, truncate(string(raw), 400))

	resp, errDo := c.http.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("请求上游失败: %w", errDo)
	}
	defer resp.Body.Close()

	payload, errRead := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if errRead != nil {
		return nil, fmt.Errorf("读取上游响应失败: %w", errRead)
	}
	c.logf("← %d %s", resp.StatusCode, truncate(string(payload), 400))

	if resp.StatusCode != http.StatusOK {
		return nil, &upstreamError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode),
			Body:       payload,
		}
	}

	var env downlinkMessage
	if errUnmarshal := json.Unmarshal(payload, &env); errUnmarshal != nil {
		// A non-envelope body means the gateway answered with something else
		// (an HTML error page, a plain-text rejection). Surface it verbatim —
		// it is the only diagnostic available.
		return nil, &upstreamError{
			StatusCode: resp.StatusCode,
			Message:    "上游响应不是预期的 JSON：" + truncate(string(payload), 200),
			Body:       payload,
		}
	}

	if !env.ok() {
		return &env, &upstreamError{
			StatusCode: resp.StatusCode,
			Code:       env.StatusCode,
			Message:    statusCodeHint(env.StatusCode, env.StatusDesc),
			Body:       payload,
		}
	}
	return &env, nil
}

// upstreamError is a failure attributed to the upstream, carrying enough detail
// for the caller to classify it (retryable, auth, rate limit).
type upstreamError struct {
	StatusCode int
	Code       int
	Message    string
	Body       []byte
}

func (e *upstreamError) Error() string { return e.Message }

// isAuthFailure reports whether the error means the session is no longer valid.
func (e *upstreamError) isAuthFailure() bool {
	if e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden {
		return true
	}
	switch e.Code {
	case 710012001: // LoginInvalid
		return true
	}
	return false
}

// isRateLimit reports whether the upstream is throttling this account.
func (e *upstreamError) isRateLimit() bool {
	if e.StatusCode == http.StatusTooManyRequests {
		return true
	}
	switch e.Code {
	case 710022004, 710022005:
		return true
	}
	return false
}

// launch probes the account and returns its identity and model list.
//
// /alice/user/launch is the same call the web client makes on load. It doubles
// as the cheapest way to check whether a stored session still works.
func (c *client) launch(ctx context.Context) (*launchResult, error) {
	var raw struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			SecUserID      string        `json:"sec_user_id"`
			AssistantBotID string        `json:"assistant_bot_id"`
			Country        string        `json:"country"`
			ModelList      []launchModel `json:"model_list"`
		} `json:"data"`
	}

	body := map[string]any{}
	if err := c.postJSONInto(ctx, "/alice/user/launch", body, &raw); err != nil {
		return nil, err
	}
	if raw.Code != 0 {
		return nil, &upstreamError{Code: raw.Code, Message: fmt.Sprintf("启动接口返回 code=%d %s", raw.Code, raw.Msg)}
	}
	out := &launchResult{
		SecUserID:      raw.Data.SecUserID,
		AssistantBotID: raw.Data.AssistantBotID,
		Country:        raw.Data.Country,
		Models:         raw.Data.ModelList,
	}
	return out, nil
}

// launchModel is one entry of the model catalogue.
type launchModel struct {
	Name      string `json:"name"`
	ModelName string `json:"model_name"`
	ModelType int64  `json:"model_type"`
	IsDefault bool   `json:"is_default"`
	Desc      string `json:"desc"`
}

// launchResult is the decoded launch response.
type launchResult struct {
	SecUserID      string
	AssistantBotID string
	Country        string
	Models         []launchModel
}

// postJSONInto posts to an /alice/* endpoint, which answers with a bare JSON
// object rather than the IM envelope.
func (c *client) postJSONInto(ctx context.Context, path string, body any, out any) error {
	raw, errMarshal := json.Marshal(body)
	if errMarshal != nil {
		return errMarshal
	}
	url := endpointURL(c.creds, path, nil)
	req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if errReq != nil {
		return errReq
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", c.creds.sanitizedCookies())
	req.Header.Set("Origin", c.creds.profile().Host)
	req.Header.Set("Referer", c.creds.profile().Host+"/chat/")
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Agw-Js-Conv", "str")

	resp, errDo := c.http.Do(req)
	if errDo != nil {
		return fmt.Errorf("请求上游失败: %w", errDo)
	}
	defer resp.Body.Close()
	payload, errRead := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if errRead != nil {
		return errRead
	}
	if resp.StatusCode != http.StatusOK {
		return &upstreamError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode), Body: payload}
	}
	if errUnmarshal := json.Unmarshal(payload, out); errUnmarshal != nil {
		return &upstreamError{StatusCode: resp.StatusCode, Message: "上游响应不是预期的 JSON：" + truncate(string(payload), 200), Body: payload}
	}
	return nil
}

// sendMessage posts one chat turn and returns the acknowledgement.
//
// An empty conversationID starts a new conversation; the ack carries the id it
// was assigned. The ack also carries the sse_token used to drain the reply.
//
// skill activates an upstream capability such as image generation. It matters
// because the model treats a plain "draw me a cat" as a request to *describe* a
// picture; only the skill makes it produce one.
func (c *client) sendMessage(ctx context.Context, conversationID, text string, skill int) (*sendMessageAck, error) {
	content, errContent := messageTextPayload(text)
	if errContent != nil {
		return nil, errContent
	}

	convID := strings.TrimSpace(conversationID)
	if convID == "" || convID == "0" {
		// The web client sends "0" for a brand-new conversation and lets the
		// server allocate the real id.
		convID = "0"
	}

	body := &sendMessageBody{
		ConversationID:      convID,
		ConversationType:    conversationTypeOneToOneBot,
		LocalConversationID: "local_" + randomUUID(),
		LocalMessageID:      randomUUID(),
		BotID:               c.creds.BotID,
		ContentType:         contentTypeText,
		Content:             content,
		SenderID:            "0",
		CreateTime:          nowMillis(),
		Status:              0,
	}
	if skill > 0 {
		body.Skill = &messageSkill{SkillID: skill}
	}

	env := uplinkMessage{
		Cmd:        cmdSendMessage,
		Channel:    2,
		SequenceID: randomUUID(),
		Version:    "",
		UplinkBody: uplinkBody{SendMessageBody: body},
	}

	resp, errPost := c.post(ctx, "/im/send/message", env, nil)
	if errPost != nil {
		return nil, errPost
	}

	var ack sendMessageAck
	if errUnmarshal := decodeDownlink(resp, "send_message_ack_downlink_body", &ack); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	return &ack, nil
}

// pullChain reads conversation history.
//
// This is how a reply is read back. It is also the fallback whenever the
// streaming path is unavailable: sendMessage assigns the turn a message id, and
// the assistant's answer appears in the same conversation shortly after.
func (c *client) pullChain(ctx context.Context, conversationID string, limit int) ([]conversationMessage, error) {
	if limit <= 0 {
		limit = 20
	}
	env := uplinkMessage{
		Cmd:        cmdPullSingleChain,
		Channel:    2,
		SequenceID: randomUUID(),
		Version:    "",
		UplinkBody: uplinkBody{PullSingleChainBody: &pullSingleChainBody{
			ConversationID:   conversationID,
			ConversationType: conversationTypeOneToOneBot,
			Direction:        0,
			Limit:            limit,
		}},
	}
	resp, errPost := c.post(ctx, "/im/chain/single", env, nil)
	if errPost != nil {
		return nil, errPost
	}
	var ack pullSingleChainAck
	if errUnmarshal := decodeDownlink(resp, "pull_singe_chain_downlink_body", &ack); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	return ack.Messages, nil
}

// fetchChunk drains the streaming reply for a send acknowledgement.
//
// The upstream answers with the accumulated chunks for the token. seq_st is the
// sequence cursor: sending 0 asks for everything from the start, which is what
// a caller that has not consumed any of the stream wants.
func (c *client) fetchChunk(ctx context.Context, token string, seq int) (*fetchChunkResult, error) {
	env := uplinkMessage{
		Cmd:        cmdFetchChunk,
		Channel:    2,
		SequenceID: randomUUID(),
		Version:    "",
		UplinkBody: uplinkBody{FetchChunkMessageBody: &fetchChunkMessageBody{
			Token:            token,
			SeqSt:            seq,
			AcceptBinaryType: 0,
		}},
	}
	resp, errPost := c.post(ctx, "/im/message/fetch_chunk", env, nil)
	if errPost != nil {
		return nil, errPost
	}
	var out fetchChunkResult
	if errUnmarshal := json.Unmarshal(resp.DownlinkBody, &struct {
		Fetch *fetchChunkResult `json:"fetch_chunk_message_downlink_body"`
	}{Fetch: &out}); errUnmarshal != nil {
		// An empty body is normal while the model has not produced anything yet.
		return &fetchChunkResult{}, nil
	}
	return &out, nil
}

// fetchChunkResult is the accumulated stream state for a token.
type fetchChunkResult struct {
	Messages []conversationMessage `json:"messages"`
	Finish   bool                  `json:"finish"`
	Seq      int                   `json:"seq"`
}

// markConversationRead marks a conversation as read, mirroring the client.
// Failures are non-fatal and are only logged by the caller.
func (c *client) markConversationRead(ctx context.Context, conversationID string) error {
	env := uplinkMessage{
		Cmd:        cmdMarkConvRead,
		Channel:    2,
		SequenceID: randomUUID(),
		Version:    "",
		UplinkBody: uplinkBody{MarkConvReadBody: &markConvReadBody{
			ConversationID:   conversationID,
			ConversationType: conversationTypeOneToOneBot,
		}},
	}
	_, err := c.post(ctx, "/im/message/mark_conv_read", env, nil)
	return err
}

// decodeDownlink pulls one named object out of downlink_body.
func decodeDownlink(resp *downlinkMessage, key string, out any) error {
	if len(resp.DownlinkBody) == 0 {
		return errors.New("上游返回了空的 downlink_body")
	}
	var wrapper map[string]json.RawMessage
	if errUnmarshal := json.Unmarshal(resp.DownlinkBody, &wrapper); errUnmarshal != nil {
		return fmt.Errorf("解析 downlink_body 失败: %w", errUnmarshal)
	}
	raw, ok := wrapper[key]
	if !ok || len(raw) == 0 {
		return fmt.Errorf("上游响应缺少字段 %s", key)
	}
	return json.Unmarshal(raw, out)
}

// webUserAgent matches the desktop web client.
//
// The gateway does not strictly require it, but a browser-shaped UA keeps the
// request in the same risk bucket as a real client.
const webUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
