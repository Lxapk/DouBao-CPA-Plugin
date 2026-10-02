package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// This file builds the OpenAI-compatible response shapes.

// buildChatCompletion assembles a chat.completion object.
func buildChatCompletion(model, text, reasoning, conversationID string, started time.Time) map[string]any {
	message := map[string]any{
		"role":    "assistant",
		"content": text,
	}
	// Reasoning is exposed under the field name reasoning clients already
	// understand. It is omitted when empty so a non-reasoning reply is
	// byte-identical to what a plain model returns.
	if reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	return map[string]any{
		"id":      "chatcmpl-" + randomUUID(),
		"object":  "chat.completion",
		"created": started.Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"message":       message,
				"finish_reason": "stop",
			},
		},
		// The upstream conversation id is exposed as an extension so a caller can
		// follow up in the same thread if it wants to; clients that do not know
		// the field ignore it.
		"conversation_id": conversationID,
	}
}

// streamChunk assembles one chat.completion.chunk object.
func streamChunk(model, conversationID string, delta map[string]any, finishReason string) map[string]any {
	choice := map[string]any{
		"index": 0,
		"delta": delta,
	}
	if finishReason != "" {
		choice["finish_reason"] = finishReason
	} else {
		choice["finish_reason"] = nil
	}
	return map[string]any{
		"id":              "chatcmpl-" + randomUUID(),
		"object":          "chat.completion.chunk",
		"created":         time.Now().Unix(),
		"model":           model,
		"choices":         []map[string]any{choice},
		"conversation_id": conversationID,
	}
}

// estimateUsage approximates a token count.
//
// The upstream reports no usage, so the numbers are estimated rather than
// measured. CJK text is roughly one token per character and Latin text roughly
// one per four characters, which is close enough for the accounting surfaces
// that read these fields.
func estimateUsage(prompt, completion string) map[string]any {
	p := estimateTokens(prompt)
	c := estimateTokens(completion)
	return map[string]any{
		"prompt_tokens":     p,
		"completion_tokens": c,
		"total_tokens":      p + c,
		// Marked so a consumer can tell these apart from measured counts.
		"estimated": true,
	}
}

// estimateTokens approximates the token count of a string.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	var cjk, other int
	for _, r := range s {
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	// CJK: about one token per character. Latin: about one per four characters,
	// with a floor so short strings do not round to zero.
	latin := (other + 3) / 4
	total := cjk + latin
	if total == 0 && utf8.RuneCountInString(s) > 0 {
		total = 1
	}
	return total
}

// isCJK reports whether a rune is in a CJK block.
func isCJK(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF, // CJK Unified Ideographs
		r >= 0x3400 && r <= 0x4DBF, // Extension A
		r >= 0x3000 && r <= 0x303F, // CJK punctuation
		r >= 0xFF00 && r <= 0xFFEF, // Fullwidth forms
		r >= 0xAC00 && r <= 0xD7AF, // Hangul
		r >= 0x3040 && r <= 0x30FF: // Hiragana/Katakana
		return true
	}
	return false
}

// executorCountTokens answers executor.count_tokens.
//
// A real tokenizer is not available, so this returns the same estimate the
// usage block reports. Returning the estimate keeps the two consistent; a
// client that counts tokens before sending sees the number it will be billed.
func executorCountTokens(request []byte) ([]byte, error) {
	var probe struct {
		Messages json.RawMessage `json:"messages"`
		Prompt   string          `json:"prompt"`
	}
	if len(request) > 0 {
		// The request shape varies by caller, so a lenient decode is used: a
		// strict one would reject a request that carries extra fields.
		_ = json.Unmarshal(request, &probe)
	}
	text := probe.Prompt
	if len(probe.Messages) > 0 {
		if built, errPrompt := buildPrompt(probe.Messages); errPrompt == nil {
			text = built
		}
	}
	return okEnvelope(map[string]any{
		"token_count": estimateTokens(text),
		"estimated":   true,
	})
}

// summarizeModelRoute renders a short description of where a model resolves,
// used by the panel and the debug log.
func summarizeModelRoute(requested string) string {
	r, upstream := resolveUpstreamModel(requested)
	profile := profileFor(r)
	if upstream == "" {
		return fmt.Sprintf("%s → %s（账号默认助手）", requested, profile.DisplayName)
	}
	return fmt.Sprintf("%s → %s/%s", requested, profile.DisplayName, upstream)
}

// modelDisplayList renders the catalogue for the panel.
func modelDisplayList() []map[string]any {
	var out []map[string]any
	for _, m := range buildCatalogue() {
		out = append(out, map[string]any{
			"id":          m.ID,
			"owned_by":    m.OwnedBy,
			"description": m.Description,
		})
	}
	return out
}

// normalizeModelName trims whitespace and a trailing :latest suffix.
func normalizeModelName(name string) string {
	return strings.TrimSuffix(strings.TrimSpace(name), ":latest")
}
