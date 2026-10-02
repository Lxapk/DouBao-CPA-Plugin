package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Running one conversation over /chat/completion.
//
// The endpoint streams the answer. This file consumes that stream and produces
// either a complete result (non-streaming callers) or a sequence of deltas
// (streaming callers) from the same pass.
//
// One detail drives the design: the reply arrives both as whole-message events
// (STREAM_MSG_NOTIFY, FULL_MSG_NOTIFY) and as incremental ones (STREAM_CHUNK,
// CHUNK_DELTA), and their contents overlap. Text is therefore accumulated from
// the incremental events only when the event stream opts into deltas, and from
// the whole messages otherwise; concatenating both would duplicate the answer.

// conversationResult is the outcome of one exchange.
type conversationResult struct {
	text           string
	reasoning      string
	conversationID string
	sectionID      string
	messageIndex   int64
	assets         []creationAsset
	// firstDelta, when set, is called as incremental text arrives.
	firstDelta func(string)
}

// runCompletion performs one turn and returns the assembled reply.
//
// When onDelta is non-nil the caller wants incremental output and it is invoked
// as text arrives. The returned result is still complete, so both call styles
// share one code path.
//
// A placeholder conversation id is resolved to a real thread here, because the
// endpoint rejects a placeholder with a generic param error.
func runCompletion(ctx context.Context, creds *credentials, text string, mode generationMode, conversationID string, onDelta func(string)) (*conversationResult, error) {
	needCreate := conversationID == "" || conversationID == "0"
	if needCreate {
		// Reuse the account's most recent thread. This is safe because every
		// request replaces the conversation's contents rather than reading prior
		// turns back.
		if id, errConv := newClient(creds).latestConversationID(ctx); errConv == nil && id != "" {
			conversationID = id
			needCreate = false
		}
	}
	return runCompletionWith(ctx, creds, text, mode, conversationID, needCreate, onDelta)
}

// runCompletionWith is runCompletion with explicit conversation control.
func runCompletionWith(ctx context.Context, creds *credentials, text string, mode generationMode, conversationID string, needCreate bool, onDelta func(string)) (*conversationResult, error) {
	client := newClient(creds)
	client.debug = debugLogger()

	body := buildCompletionRequest(creds, text, mode, conversationID, "", 0)
	if needCreate {
		body.Option.NeedCreateConversation = true
	}

	var (
		mu        sync.Mutex
		deltaText strings.Builder
		fullText  string
		reasoning strings.Builder
		assets    []creationAsset
		sectionID string
		msgIndex  int64
		convID    = conversationID
		streamErr error
		sawFull   bool
		sawDelta  bool
		replyDone bool
	)

	handleEvent := func(ev completionEvent) bool {
		switch ev.Name {
		case "SSE_ACK":
			var ack struct {
				AckClientMeta struct {
					ConversationID string `json:"conversation_id"`
					SectionID      string `json:"section_id"`
				} `json:"ack_client_meta"`
				QueryList []struct {
					MessageIndex int64 `json:"message_index"`
				} `json:"query_list"`
			}
			if json.Unmarshal(ev.Data, &ack) == nil {
				mu.Lock()
				if ack.AckClientMeta.ConversationID != "" {
					convID = ack.AckClientMeta.ConversationID
				}
				sectionID = ack.AckClientMeta.SectionID
				if len(ack.QueryList) > 0 {
					msgIndex = ack.QueryList[0].MessageIndex
				}
				mu.Unlock()
			}

		case "STREAM_MSG_NOTIFY", "FULL_MSG_NOTIFY":
			var payload struct {
				Content streamMessageContent `json:"content"`
			}
			if json.Unmarshal(ev.Data, &payload) == nil {
				mu.Lock()
				if t := payload.Content.textOf(); t != "" {
					fullText = t
					sawFull = true
				}
				if r := payload.Content.reasoningOf(); r != "" {
					reasoning.Reset()
					reasoning.WriteString(r)
				}
				if a := payload.Content.creationsOf(); len(a) > 0 {
					assets = mergeAssets(assets, a)
				}
				mu.Unlock()
			}

		case "STREAM_CHUNK", "CHUNK_DELTA":
			var payload struct {
				Content streamMessageContent `json:"content"`
				PatchOp []patchOp            `json:"patch_op"`
				Delta   string               `json:"delta"`
				Text    string               `json:"text"`
			}
			if json.Unmarshal(ev.Data, &payload) == nil {
				mu.Lock()

				// STREAM_CHUNK carries a list of patch operations rather than a
				// whole message.
				var piece string
				for _, op := range payload.PatchOp {
					piece += op.PatchValue.textOf()
				}
				if piece == "" {
					piece = payload.Content.textOf()
				}

				switch {
				case payload.Delta != "":
					deltaText.WriteString(payload.Delta)
					if onDelta != nil {
						onDelta(payload.Delta)
					}
				case payload.Text != "":
					deltaText.WriteString(payload.Text)
					if onDelta != nil {
						onDelta(payload.Text)
					}
				case piece != "":
					// A whole-message event replaces the accumulation; a patch
					// appends to it.
					if ev.Name == "STREAM_MSG_NOTIFY" {
						fullText = piece
						sawFull = true
					} else {
						deltaText.WriteString(piece)
						sawDelta = true
						if onDelta != nil {
							onDelta(piece)
						}
					}
				}

				if a := payload.Content.creationsOf(); len(a) > 0 {
					assets = mergeAssets(assets, a)
				}
				for _, op := range payload.PatchOp {
					if a := op.PatchValue.creationsOf(); len(a) > 0 {
						assets = mergeAssets(assets, a)
					}
				}
				mu.Unlock()
			}

		case "STREAM_ERROR", "ERROR":
			if e, ok := parseStreamError(ev.Data); ok {
				mu.Lock()
				streamErr = &upstreamError{
					Code:    e.ErrorCode,
					Message: statusCodeHint(e.ErrorCode, e.ErrorMsg),
				}
				mu.Unlock()
				return false
			}

		case "SSE_REPLY_END":
			mu.Lock()
			replyDone = true
			mu.Unlock()
			return false
		}
		return true
	}

	errStream := client.streamCompletion(ctx, body, handleEvent)
	if errStream != nil {
		return nil, wrapExecutorError(errStream)
	}

	mu.Lock()
	defer mu.Unlock()

	if streamErr != nil {
		return nil, streamErr
	}

	// Prefer the incremental accumulation when the stream opted into deltas,
	// otherwise the whole-message text. Concatenating both duplicates the reply.
	final := fullText
	if sawDelta && deltaText.Len() > 0 {
		final = deltaText.String()
	} else if deltaText.Len() > 0 && !sawFull {
		final = deltaText.String()
	}

	_ = replyDone

	// An image reply carries no text at all; a missing answer is only an error
	// when there is no media either.
	if strings.TrimSpace(final) == "" && len(assets) == 0 {
		return nil, errors.New("上游没有返回内容")
	}

	// Upgrade images to full-size no-watermark URLs.
	if len(assets) > 0 {
		assets = client.hydrateAssets(ctx, assets)
	}

	answer, think := splitReasoning(final)
	if t := strings.TrimSpace(reasoning.String()); t != "" && think == "" {
		think = t
	}

	return &conversationResult{
		text:           answer,
		reasoning:      think,
		conversationID: convID,
		sectionID:      sectionID,
		messageIndex:   msgIndex,
		assets:         assets,
	}, nil
}

// patchOp is one entry of a STREAM_CHUNK's patch_op list.
//
// The streaming endpoint expresses incremental updates as patches rather than
// whole messages, so the text and the generated media of a streaming answer
// arrive here rather than in a top-level content object.
//
// PatchValue carries content_block directly and also exposes an ext object,
// alongside a legacy content wrapper that some frames still use. Both spellings
// are modelled.
type patchOp struct {
	PatchObject int        `json:"patch_object"`
	PatchType   int        `json:"patch_type"`
	PatchValue  patchValue `json:"patch_value"`
}

type patchValue struct {
	// ContentBlock is the current shape: blocks sit directly on patch_value.
	ContentBlock []responseBlock `json:"content_block"`
	// Content is the older wrapper, still emitted for some patch types.
	Content messageContentBody `json:"content"`
	Ext     json.RawMessage    `json:"ext"`
}

// blocks returns every block the patch carries, under either spelling.
func (p *patchValue) blocks() []responseBlock {
	if len(p.ContentBlock) > 0 {
		return p.ContentBlock
	}
	return p.Content.ContentBlock
}

// textOf concatenates the text blocks of a patch.
//
// An empty text_block is a no-op that only keeps the block id alive; it must
// not be treated as content.
func (p *patchValue) textOf() string {
	var out strings.Builder
	for _, b := range p.blocks() {
		if b.BlockType != blockTypeText {
			continue
		}
		var c blockContent
		if json.Unmarshal(b.Content, &c) != nil || c.TextBlock == nil {
			continue
		}
		out.WriteString(c.TextBlock.Text)
	}
	return out.String()
}

// creationsOf collects every generated asset the patch carries.
func (p *patchValue) creationsOf() []creationAsset {
	var out []creationAsset
	for _, b := range p.blocks() {
		if b.BlockType != blockTypeCreation {
			continue
		}
		var c blockContent
		if json.Unmarshal(b.Content, &c) != nil {
			continue
		}
		out = append(out, parseCreationBlock(c.CreationBlock)...)
	}
	return out
}

// messageContentBody is the content object shared by patches and whole messages.
type messageContentBody struct {
	ContentBlock []responseBlock `json:"content_block"`
}

func (b *messageContentBody) textOf() string {
	var out strings.Builder
	for i := range b.ContentBlock {
		if b.ContentBlock[i].BlockType != blockTypeText {
			continue
		}
		var c blockContent
		if json.Unmarshal(b.ContentBlock[i].Content, &c) != nil || c.TextBlock == nil {
			continue
		}
		out.WriteString(c.TextBlock.Text)
	}
	return out.String()
}

func (b *messageContentBody) creationsOf() []creationAsset {
	var out []creationAsset
	for i := range b.ContentBlock {
		if b.ContentBlock[i].BlockType != blockTypeCreation {
			continue
		}
		var c blockContent
		if json.Unmarshal(b.ContentBlock[i].Content, &c) != nil {
			continue
		}
		out = append(out, parseCreationBlock(c.CreationBlock)...)
	}
	return out
}

// mergeAssets folds newly seen assets into the accumulated list.
//
// The same creation is reported repeatedly as it progresses, so entries are
// keyed by ID and the newer, more complete version replaces the older one. The
// first report of an image carries only dimensions; the last carries the key and
// URL.
func mergeAssets(existing, incoming []creationAsset) []creationAsset {
	index := make(map[string]int, len(existing))
	for i, a := range existing {
		if a.ID != "" {
			index[a.ID] = i
		}
	}
	for _, a := range incoming {
		if a.ID == "" {
			existing = append(existing, a)
			continue
		}
		if i, ok := index[a.ID]; ok {
			// Keep whichever field is populated; a later frame may add the URL
			// without repeating the model name.
			merged := existing[i]
			if a.Key != "" {
				merged.Key = a.Key
			}
			if a.URL != "" {
				merged.URL = a.URL
			}
			if a.Model != "" {
				merged.Model = a.Model
			}
			if a.Width > 0 {
				merged.Width = a.Width
			}
			if a.Height > 0 {
				merged.Height = a.Height
			}
			if a.Type != 0 {
				merged.Type = a.Type
			}
			if a.Publishable {
				merged.Publishable = true
			}
			if len(a.Kinds) > 0 {
				merged.Kinds = a.Kinds
			}
			existing[i] = merged
			continue
		}
		index[a.ID] = len(existing)
		existing = append(existing, a)
	}
	return existing
}

// describeAssets renders a short human-readable summary, used when a caller
// wants text and the answer was media.
func describeAssets(assets []creationAsset) string {
	if len(assets) == 0 {
		return ""
	}
	var parts []string
	for _, a := range assets {
		kind := "图片"
		if a.isVideo() {
			kind = "视频"
		}
		desc := fmt.Sprintf("已生成%s", kind)
		if a.Model != "" {
			desc += "（" + a.Model + "）"
		}
		if a.Width > 0 && a.Height > 0 {
			desc += fmt.Sprintf(" %d×%d", a.Width, a.Height)
		}
		parts = append(parts, desc)
	}
	return strings.Join(parts, "；")
}
