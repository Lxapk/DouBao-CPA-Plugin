package main

import (
	"strings"
	"testing"
)

func TestRatioFromSize(t *testing.T) {
	cases := map[string]string{
		"1024x1024": "1:1",
		"2048x1152": "16:9",
		"1152x2048": "9:16",
		"1536x1024": "3:2",
		"1024x1536": "2:3",
		"16:9":      "16:9",
		"9：16":      "9:16",
		"2048x1000": "",
		"":          "",
		"auto":      "",
	}
	for in, want := range cases {
		if got := ratioFromSize(in); got != want {
			t.Errorf("ratioFromSize(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestNormalizeRatio(t *testing.T) {
	cases := map[string]string{
		"16:9": "16:9", "16x9": "16:9", "16：9": "16:9",
		" 1 : 1 ": "1:1", "2048x1152": "16:9", "": "",
	}
	for in, want := range cases {
		if got := normalizeRatio(in); got != want {
			t.Errorf("normalizeRatio(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestNormalizeSeconds(t *testing.T) {
	cases := map[int]int{0: 0, -3: 0, 1: 4, 5: 5, 10: 10, 30: 12}
	for in, want := range cases {
		if got := normalizeSeconds(in); got != want {
			t.Errorf("normalizeSeconds(%d) = %d, 期望 %d", in, got, want)
		}
	}
}

// 图片只要比例，视频要比例加时长。
func TestGenerationHints(t *testing.T) {
	req := &chatRequest{AspectRatio: "16:9", Seconds: 5}

	img := generationHints(kindImage, req)
	if !strings.Contains(img, "16:9") {
		t.Errorf("图片应带比例: %q", img)
	}
	if strings.Contains(img, "秒") {
		t.Errorf("图片不该带时长: %q", img)
	}

	vid := generationHints(kindVideo, req)
	if !strings.Contains(vid, "16:9") || !strings.Contains(vid, "5 秒") {
		t.Errorf("视频应带比例和时长: %q", vid)
	}

	// 用 size 指定比例也应识别
	if got := generationHints(kindImage, &chatRequest{Size: "2048x1152"}); !strings.Contains(got, "16:9") {
		t.Errorf("size 未转成比例: %q", got)
	}

	// 没有参数时不动提示词
	if got := generationHints(kindImage, &chatRequest{}); got != "" {
		t.Errorf("无参数不该有提示: %q", got)
	}
}

func TestWithGenerationHints(t *testing.T) {
	req := &chatRequest{AspectRatio: "1:1"}
	got := withGenerationHints("画一只猫", kindImage, req)
	if !strings.HasPrefix(got, "画一只猫") || !strings.Contains(got, "1:1") {
		t.Errorf("提示词拼接不对: %q", got)
	}
	// 无参数时原样返回
	if got := withGenerationHints("画一只猫", kindImage, &chatRequest{}); got != "画一只猫" {
		t.Errorf("无参数时提示词被改动: %q", got)
	}
}
