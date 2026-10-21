package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every stream payload must be a bare JSON chunk. The host's writer adds the
// "data: " prefix and the "[DONE]" terminator; a payload that already looked like
// an SSE frame would reach the client as "data: data: {...}" and break parsing.
func TestStreamFramesAreBareJSONChunks(t *testing.T) {
	frames := buildStreamFrames("doubao/default", "你好，世界", "先想一想", []creationAsset{
		{URL: "https://example.test/a.jpg", Key: "tos-cn-i-x/rc_gen_image/a.jpeg", Width: 1024, Height: 1024},
	})
	if len(frames) < 3 {
		t.Fatalf("分片太少: %d", len(frames))
	}

	var sawReasoning, sawContent, sawMedia, sawStop bool
	for _, frame := range frames {
		if strings.Contains(string(frame), "data:") {
			t.Fatalf("分片自带 SSE 前缀: %s", frame)
		}
		var chunk struct {
			Object  string `json:"object"`
			Choices []struct {
				Delta        map[string]any `json:"delta"`
				FinishReason *string        `json:"finish_reason"`
			} `json:"choices"`
		}
		if errUnmarshal := json.Unmarshal(frame, &chunk); errUnmarshal != nil {
			t.Fatalf("分片不是 JSON: %v (%s)", errUnmarshal, frame)
		}
		if chunk.Object != "chat.completion.chunk" {
			t.Errorf("object = %q", chunk.Object)
		}
		if len(chunk.Choices) != 1 {
			t.Fatalf("choices 长度 = %d", len(chunk.Choices))
		}
		delta := chunk.Choices[0].Delta
		if _, ok := delta["reasoning_content"]; ok {
			sawReasoning = true
		}
		if text, ok := delta["content"].(string); ok && text != "" {
			sawContent = true
		}
		if _, ok := delta["media"]; ok {
			sawMedia = true
		}
		if chunk.Choices[0].FinishReason != nil && *chunk.Choices[0].FinishReason == "stop" {
			sawStop = true
		}
	}
	if !sawReasoning || !sawContent || !sawMedia || !sawStop {
		t.Errorf("分片内容不完整: reasoning=%t content=%t media=%t stop=%t",
			sawReasoning, sawContent, sawMedia, sawStop)
	}
}

// The assembled text must be recoverable from the content deltas alone, which is
// what a streaming client does.
func TestStreamFramesRebuildTheAnswer(t *testing.T) {
	answer := strings.Repeat("长回复", 100)
	frames := buildStreamFrames("doubao/pro", answer, "", nil)
	var built strings.Builder
	for _, frame := range frames {
		var chunk struct {
			Choices []struct {
				Delta map[string]any `json:"delta"`
			} `json:"choices"`
		}
		if errUnmarshal := json.Unmarshal(frame, &chunk); errUnmarshal != nil {
			t.Fatalf("分片不是 JSON: %v", errUnmarshal)
		}
		if text, ok := chunk.Choices[0].Delta["content"].(string); ok {
			built.WriteString(text)
		}
	}
	if built.String() != answer {
		t.Errorf("拼回的文本不一致: %d vs %d 字符", built.Len(), len(answer))
	}
}
