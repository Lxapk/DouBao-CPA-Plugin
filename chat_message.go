package main

import (
	"encoding/json"
	"strings"
)

// chatMessage is one OpenAI-format message.
//
// Content is kept raw because it is either a string or an array of parts, and
// the array form is what multimodal clients send.
type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Name    string          `json:"name,omitempty"`
}

// text flattens the content field.
//
// Text parts are concatenated. Non-text parts (images, audio) contribute
// nothing here; the upstream accepts only text on this path, and describing an
// attachment would invent a claim about it that the model cannot verify. An
// image-only turn therefore yields an empty prompt, which the caller reports as
// "no text to send" rather than silently posting a blank message.
func (m *chatMessage) text() string {
	if len(m.Content) == 0 {
		return ""
	}
	var s string
	if errUnmarshal := json.Unmarshal(m.Content, &s); errUnmarshal == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if errUnmarshal := json.Unmarshal(m.Content, &parts); errUnmarshal == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Text != "" {
				b.WriteString(p.Text)
				b.WriteString("\n")
			}
		}
		return strings.TrimSpace(b.String())
	}
	return strings.TrimSpace(string(m.Content))
}
