package main

import (
	"regexp"
	"strings"
)

// The upstream returns the model's reasoning inside the answer text.
//
// A reply arrives as "<think>…</think>收到": the chain of thought is inlined
// ahead of the answer, wrapped in  tags. An OpenAI caller expects `content` to
// be the answer, so the reasoning is separated out here rather than passed
// through — otherwise every client displays the model's scratch work.
//
// The tags are not part of the documented protocol; they are what the upstream
// emits, and both an empty pair ("" as seen when the model did not
// reason) and a populated one occur.

// thinkTagPattern matches a reasoning block, including a nested-looking prefix.
//
// The match is non-greedy and spans newlines, because reasoning often contains
// line breaks.
var thinkTagPattern = regexp.MustCompile(`(?s)<think[^>]*>.*?</think[^>]*>`)

// splitReasoning separates the reasoning block from the answer.
//
// It returns the visible answer and the reasoning text. When the reply has no
// reasoning block the reasoning is empty and the answer is returned unchanged.
func splitReasoning(text string) (answer string, reasoning string) {
	if !strings.Contains(text, "<think") {
		return text, ""
	}

	var collected []string
	answer = thinkTagPattern.ReplaceAllStringFunc(text, func(match string) string {
		inner := match
		if open := strings.Index(inner, ">"); open >= 0 {
			inner = inner[open+1:]
		}
		if closeIdx := strings.LastIndex(inner, "</think"); closeIdx >= 0 {
			inner = inner[:closeIdx]
		}
		inner = strings.TrimSpace(inner)
		if inner != "" {
			collected = append(collected, inner)
		}
		return ""
	})

	answer = strings.TrimSpace(answer)
	if len(collected) > 0 {
		reasoning = strings.Join(collected, "\n\n")
	}

	// An unclosed  tag means the stream was cut mid-reasoning. Everything from
	// the tag onward is reasoning, and there is no answer to show.
	if strings.Contains(answer, "<think") {
		if idx := strings.Index(answer, "<think"); idx >= 0 {
			tail := answer[idx:]
			if open := strings.Index(tail, ">"); open >= 0 {
				tail = tail[open+1:]
			}
			tail = strings.TrimSpace(tail)
			if tail != "" {
				if reasoning == "" {
					reasoning = tail
				} else {
					reasoning += "\n\n" + tail
				}
			}
			answer = strings.TrimSpace(answer[:idx])
		}
	}
	return answer, reasoning
}

// cleanAnswer returns only the visible answer.
func cleanAnswer(text string) string {
	answer, _ := splitReasoning(text)
	return answer
}
