package main

// Live tests against the real upstream.
//
// They are skipped unless DOUBAO_LIVE_COOKIES is set, so `go test ./...` stays
// hermetic and CI never depends on an account.

import (
	"context"
	"os"
	"testing"
	"time"
)

func liveCredentials(t *testing.T) *credentials {
	t.Helper()
	cookie := os.Getenv("DOUBAO_LIVE_COOKIES")
	if cookie == "" {
		t.Skip("DOUBAO_LIVE_COOKIES 未设置，跳过实时测试")
	}
	r := realmDoubao
	if v := os.Getenv("DOUBAO_LIVE_REALM"); v != "" {
		r = realm(v)
	}
	c := &credentials{Realm: r, Cookies: cookie}
	return c.withDefaults()
}

// TestLiveLaunch proves the credential works: it is the cheapest authenticated
// call and the gateway checks the same session bindings a chat would.
func TestLiveLaunch(t *testing.T) {
	creds := liveCredentials(t)
	client := newClient(creds)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := client.launch(ctx)
	if err != nil {
		t.Fatalf("launch 失败: %v", err)
	}
	if res.SecUserID == "" {
		t.Fatalf("launch 未返回 sec_user_id")
	}
	t.Logf("账号 %s，助手 %s，模型 %d 条", res.SecUserID, res.AssistantBotID, len(res.Models))
}

// TestLiveChatRoundTrip proves the streaming chat path end to end.
func TestLiveChatRoundTrip(t *testing.T) {
	creds := liveCredentials(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	res, err := runCompletion(ctx, creds, "只回复两个字：收到", generationMode{Kind: "text"}, "0", nil)
	if err != nil {
		t.Fatalf("对话失败: %v", err)
	}
	t.Logf("会话 %s，回复: %q", res.conversationID, res.text)
	if res.text == "" {
		t.Fatalf("回复为空")
	}
}

// TestLiveImageGeneration proves the image path: the request must select the
// generation skill, and the reply arrives as a creation block rather than text.
//
// This is the test that would have caught the original bug, where the message
// text was placed in "content" instead of "content_block" and the upstream
// answered with a description of the picture instead of the picture.
func TestLiveImageGeneration(t *testing.T) {
	if os.Getenv("DOUBAO_LIVE_IMAGE") == "" {
		t.Skip("DOUBAO_LIVE_IMAGE 未设置，跳过生图测试（会消耗额度）")
	}
	creds := liveCredentials(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	res, err := runCompletion(ctx, creds, "一只橘猫", generationMode{Skill: skillImageGen, Kind: "image"}, "0", nil)
	if err != nil {
		t.Fatalf("生图失败: %v", err)
	}
	t.Logf("会话 %s，文本 %q，素材 %d 个", res.conversationID, res.text, len(res.assets))
	if len(res.assets) == 0 {
		t.Fatalf("没有返回图片素材")
	}
	for _, a := range res.assets {
		t.Logf("  素材: type=%d key=%s url=%.80s model=%s %dx%d",
			a.Type, a.Key, a.URL, a.Model, a.Width, a.Height)
		if a.URL == "" {
			t.Errorf("素材没有可用的 URL")
		}
	}
}

// TestLiveImageNoWatermark proves the no-watermark resolver returns a usable
// full-size URL for a resource key.
func TestLiveImageNoWatermark(t *testing.T) {
	key := os.Getenv("DOUBAO_LIVE_IMAGE_KEY")
	if key == "" {
		t.Skip("DOUBAO_LIVE_IMAGE_KEY 未设置，跳过无水印测试")
	}
	creds := liveCredentials(t)
	client := newClient(creds)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resolved, err := client.resolveNoWatermark(ctx, []string{key})
	if err != nil {
		t.Fatalf("解析无水印失败: %v", err)
	}
	v, ok := resolved[key]
	if !ok || v.URL == "" {
		t.Fatalf("未返回 URL: %+v", resolved)
	}
	t.Logf("无水印 URL: %.120s (%dx%d)", v.URL, v.Width, v.Height)
}
