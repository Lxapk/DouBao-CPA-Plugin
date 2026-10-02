package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// This file implements /chat/completion, the web client's streaming endpoint.
//
// It is a different protocol from the /im/* family. Where /im/send/message takes
// a command envelope and returns a single JSON acknowledgement, this endpoint
// takes a chat request and answers with a Server-Sent Events stream. Everything
// the web client does — text chat, image generation, video generation — flows
// through here.
//
// The request shape is the part that matters most, because the server validates
// it strictly and reports failure with a generic 710020202. Two details are easy
// to get wrong and both were found by diffing against a real captured request:
//
//  1. messages carry content_block, not content. A message whose text is in
//     "content" is treated as plain chat and silently produces text instead of
//     invoking a generation. The text goes in
//     content_block[0].content.text_block.text.
//
//  2. option.aggregate_params.mode_id must be set ("1"). Without it the request
//     is accepted but the generation path is not selected.
//
// Sending the wrong shape does not error; it produces a text answer. That is why
// image generation appeared impossible for so long while the endpoint was in fact
// working.

// completionRequest is the /chat/completion body.
type completionRequest struct {
	ClientMeta  clientMeta        `json:"client_meta"`
	Messages    []completionMsg   `json:"messages"`
	Option      completionOption  `json:"option"`
	UserContext []any             `json:"user_context"`
	Ext         map[string]string `json:"ext"`
}

// clientMeta anchors the request to a conversation.
//
// last_section_id and last_message_index tell the server which point of the
// conversation this turn continues from. They are optional for a brand-new
// conversation and required when continuing one.
type clientMeta struct {
	ConversationID   string       `json:"conversation_id"`
	BotID            string       `json:"bot_id"`
	LastSectionID    string       `json:"last_section_id,omitempty"`
	LastMessageIndex int64        `json:"last_message_index,omitempty"`
	LocalPermissions []permission `json:"local_permissions,omitempty"`
}

type permission struct {
	PermissionName string `json:"permission_name"`
	Status         int    `json:"status"`
}

// completionMsg is one message in the request.
//
// ContentBlock is what the server reads. A plain "content" field is accepted but
// only produces chat behaviour.
type completionMsg struct {
	LocalMessageID string         `json:"local_message_id"`
	ContentBlock   []requestBlock `json:"content_block"`
	MessageStatus  int            `json:"message_status"`
}

// requestBlock is one block of an outbound message.
type requestBlock struct {
	BlockType    int              `json:"block_type"`
	Content      requestBlockBody `json:"content"`
	BlockID      string           `json:"block_id"`
	ParentID     string           `json:"parent_id"`
	MetaInfo     []any            `json:"meta_info"`
	AppendFields []any            `json:"append_fields"`
}

type requestBlockBody struct {
	TextBlock    *textBlockPayload `json:"text_block,omitempty"`
	PCEventBlock string            `json:"pc_event_block"`
}

type textBlockPayload struct {
	Text        string `json:"text"`
	IconURL     string `json:"icon_url"`
	IconURLDark string `json:"icon_url_dark"`
	Summary     string `json:"summary"`
}

// completionOption carries the behavioural switches.
type completionOption struct {
	SendMessageScene  string `json:"send_message_scene"`
	CreateTimeMS      int64  `json:"create_time_ms"`
	CollectID         string `json:"collect_id"`
	IsAudio           bool   `json:"is_audio"`
	AnswerWithSuggest bool   `json:"answer_with_suggest"`
	AgentMode         int    `json:"agent_mode"`
	TTSSwitch         bool   `json:"tts_switch"`
	NeedDeepThink     int    `json:"need_deep_think"`

	// AggregateParams is required. mode_id selects the generation mode and its
	// absence makes the server answer as plain chat.
	AggregateParams aggregateParams `json:"aggregate_params"`

	ModelConfig modelConfig `json:"model_config"`

	SSERecvEventOptions struct {
		SupportChunkDelta bool `json:"support_chunk_delta"`
	} `json:"sse_recv_event_options"`
	SupportLazyFetchStream bool `json:"support_lazy_fetch_stream"`

	UniqueKey string `json:"unique_key"`
	StartSeq  int    `json:"start_seq"`

	// NeedCreateConversation asks the server to allocate a conversation, which is
	// how a new thread is started. Without it an empty conversation_id is
	// rejected with a generic "common invalid param".
	NeedCreateConversation bool `json:"need_create_conversation"`

	ConversationMode string `json:"conversation_mode,omitempty"`
}

type aggregateParams struct {
	MentionSkillList  string `json:"mention_skill_list"`
	MentionPluginList string `json:"mention_plugin_list"`
	MentionExt        string `json:"mention_ext"`
	ConversationMode  string `json:"conversation_mode"`
	ModeID            string `json:"mode_id"`
	ModelItemKey      string `json:"model_item_key"`
	AgentMode         string `json:"agent_mode"`
	ReasoningEffort   string `json:"reasoning_effort"`
	ProviderID        string `json:"provider_id"`
}

type modelConfig struct {
	ModelItemKey     string         `json:"model_item_key"`
	ModelExtraParams map[string]any `json:"model_extra_params"`
	ReasoningEffort  int            `json:"reasoning_effort"`
}

// buildCompletionRequest assembles the body for one turn.
//
// mode selects the generation behaviour: empty for plain chat, a skill id for a
// generation. It is what distinguishes "describe the picture" from "draw the
// picture".
func buildCompletionRequest(creds *credentials, text string, mode generationMode, conversationID, lastSectionID string, lastMessageIndex int64) *completionRequest {
	now := nowMillis()

	option := completionOption{
		SendMessageScene:  "",
		CreateTimeMS:      now,
		CollectID:         "",
		IsAudio:           false,
		AnswerWithSuggest: false,
		AgentMode:         2,
		TTSSwitch:         false,
		NeedDeepThink:     0,
		AggregateParams: aggregateParams{
			MentionSkillList:  "[]",
			MentionPluginList: "[]",
			MentionExt:        "[{}]",
			ConversationMode:  "",
			ModeID:            "1",
			ModelItemKey:      "0",
			AgentMode:         "2",
			ReasoningEffort:   "3",
			ProviderID:        "",
		},
		ModelConfig: modelConfig{
			ModelItemKey:     "0",
			ModelExtraParams: map[string]any{},
			ReasoningEffort:  3,
		},
		UniqueKey: randomUUID(),
		StartSeq:  0,
	}
	option.SSERecvEventOptions.SupportChunkDelta = true
	option.SupportLazyFetchStream = true

	// A generation request names its skill so the server routes it to the
	// generation pipeline rather than the chat pipeline.
	if mode.Skill > 0 {
		option.AggregateParams.MentionSkillList = fmt.Sprintf("[%d]", mode.Skill)
	}

	generalTaskParam, _ := json.Marshal(map[string]any{
		"runtime_type":     0,
		"agent_task_param": map[string]any{},
		"agent_task_param_change": map[string]any{
			"runtime_changed":           false,
			"device_changed":            false,
			"sandbox_auth_type_changed": false,
		},
		"project_id":               "",
		"need_modify_conversation": false,
	})

	ext := map[string]string{
		"agent_mode":                    "2",
		"use_deep_think":                "0",
		"general_task_param":            string(generalTaskParam),
		"collection_id":                 "",
		"is_finish":                     "1",
		"commerce_credit_config_enable": "0",
	}

	return &completionRequest{
		ClientMeta: clientMeta{
			ConversationID:   conversationID,
			BotID:            creds.BotID,
			LastSectionID:    lastSectionID,
			LastMessageIndex: lastMessageIndex,
			LocalPermissions: []permission{
				{PermissionName: "ACCESS_COARSE_LOCATION", Status: 3},
				{PermissionName: "ACCESS_FINE_LOCATION", Status: 3},
				{PermissionName: "ACCESS_BACKGROUND_LOCATION", Status: 3},
			},
		},
		Messages: []completionMsg{{
			LocalMessageID: randomUUID(),
			MessageStatus:  0,
			ContentBlock: []requestBlock{{
				BlockType:    blockTypeText,
				BlockID:      randomUUID(),
				ParentID:     "",
				MetaInfo:     []any{},
				AppendFields: []any{},
				Content: requestBlockBody{
					TextBlock: &textBlockPayload{
						Text:        text,
						IconURL:     "",
						IconURLDark: "",
						Summary:     "",
					},
					PCEventBlock: "",
				},
			}},
		}},
		Option:      option,
		UserContext: []any{},
		Ext:         ext,
	}
}

// generationMode describes what kind of answer a request asks for.
type generationMode struct {
	// Skill is the upstream skill id, or 0 for plain chat.
	Skill int
	// Kind is "text", "image" or "video", used to pick the reply parser.
	Kind string
}

// completionEvent is one decoded SSE event.
type completionEvent struct {
	// Name is the "event:" field.
	Name string
	// Data is the raw "data:" payload.
	Data []byte
}

// streamCompletion posts a request and delivers decoded SSE events.
//
// The callback returns false to stop early, which is how a caller that already
// has what it needs avoids draining the rest of a long generation.
func (c *client) streamCompletion(ctx context.Context, body *completionRequest, onEvent func(completionEvent) bool) error {
	raw, errMarshal := json.Marshal(body)
	if errMarshal != nil {
		return fmt.Errorf("编码请求失败: %w", errMarshal)
	}

	url := endpointURL(c.creds, "/chat/completion", nil)
	req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if errReq != nil {
		return errReq
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cookie", c.creds.sanitizedCookies())
	req.Header.Set("Origin", c.creds.profile().Host)
	req.Header.Set("Referer", c.creds.profile().Host+"/chat/")
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Agw-Js-Conv", "str")

	c.logf("→ POST /chat/completion text=%q skill=%d", firstTextBlock(body), body.Option.AggregateParams.MentionSkillList)

	// The client timeout is cleared for streaming: the response is long-lived and
	// the context deadline is what should bound it.
	streamClient := &http.Client{}
	resp, errDo := streamClient.Do(req)
	if errDo != nil {
		return fmt.Errorf("请求上游失败: %w", errDo)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return &upstreamError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode),
			Body:       payload,
		}
	}

	scanner := bufio.NewScanner(resp.Body)
	// Generous buffer: a creation block carrying image data easily exceeds the
	// 64 KiB default and would otherwise be dropped as an over-long line.
	scanner.Buffer(make([]byte, 0, 1<<20), 8<<20)

	var current completionEvent
	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case line == "":
			// Blank line terminates an event.
			if current.Name != "" || len(current.Data) > 0 {
				if !onEvent(current) {
					return nil
				}
			}
			current = completionEvent{}
		case strings.HasPrefix(line, "event:"):
			current.Name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			chunk := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if len(current.Data) > 0 {
				current.Data = append(current.Data, '\n')
			}
			current.Data = append(current.Data, chunk...)
		case strings.HasPrefix(line, "id:"):
			// Event id, not needed.
		}
	}
	// A stream that ends without a trailing blank line still carries a final
	// event.
	if current.Name != "" || len(current.Data) > 0 {
		onEvent(current)
	}
	if errScan := scanner.Err(); errScan != nil {
		// A canceled context surfaces here as a read error; the caller's deadline
		// already explains it.
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("读取上游流失败: %w", errScan)
	}
	return nil
}

func firstTextBlock(body *completionRequest) string {
	if len(body.Messages) == 0 || len(body.Messages[0].ContentBlock) == 0 {
		return ""
	}
	tb := body.Messages[0].ContentBlock[0].Content.TextBlock
	if tb == nil {
		return ""
	}
	return tb.Text
}

// streamError is the payload of a STREAM_ERROR event.
type streamError struct {
	ErrorCode int    `json:"error_code"`
	ErrorMsg  string `json:"error_msg"`
}

// parseStreamError decodes a STREAM_ERROR event.
func parseStreamError(data []byte) (*streamError, bool) {
	var e streamError
	if json.Unmarshal(data, &e) != nil || e.ErrorCode == 0 {
		return nil, false
	}
	return &e, true
}
