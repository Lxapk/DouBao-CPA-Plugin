package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStreamFramesCarryImageMarkdown(t *testing.T) {
	assets := []creationAsset{{
		URL: "https://p26-flow-imagex-sign.byteimg.com/tos-cn-i-x/a.jpeg", Model: "Seedream 5.0 Flash", Key: "tos-cn-i-x/a.jpeg",
	}}
	frames := buildStreamFrames("doubao/image", "画好了", "", assets)
	var all strings.Builder
	var hasMedia bool
	for _, f := range frames {
		var chunk map[string]any
		if err := json.Unmarshal(f, &chunk); err != nil { t.Fatal(err) }
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 { continue }
		d, _ := choices[0].(map[string]any)["delta"].(map[string]any)
		if c, ok := d["content"].(string); ok { all.WriteString(c) }
		if _, ok := d["media"]; ok { hasMedia = true }
	}
	got := all.String()
	if !strings.Contains(got, "![") || !strings.Contains(got, assets[0].URL) {
		t.Errorf("流式 content 里没有 markdown 图片: %q", got)
	}
	if !strings.Contains(got, "画好了") {
		t.Errorf("文字丢了: %q", got)
	}
	if !hasMedia {
		t.Error("media 字段丢了")
	}
}
