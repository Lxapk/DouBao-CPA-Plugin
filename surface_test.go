package main

import (
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// The declared capabilities and the dispatch must agree. A format that is
// declared but not routed here reaches the executor and falls through to chat,
// which answers an image request with a text description.
func TestDeclaredFormatsAreAllDispatched(t *testing.T) {
	caps := buildRegistration().Capabilities
	for _, format := range caps.ExecutorInputFormats {
		req := &pluginapi.ExecutorRequest{Format: format}
		surface := surfaceOf(req)
		if surface == "" {
			t.Errorf("格式 %s 没有对应的请求面", format)
		}
	}

	cases := map[string]string{
		"chat-completions": surfaceChat,
		"openai-image":     surfaceImage,
		"openai-video":     surfaceVideo,
		"":                 surfaceChat,
		"something-new":    surfaceChat,
	}
	for format, want := range cases {
		if got := surfaceOf(&pluginapi.ExecutorRequest{Format: format}); got != want {
			t.Errorf("surfaceOf(%q) = %q, 期望 %q", format, got, want)
		}
	}
}

// The source format is the fallback when a host omits the resolved format, which
// is how the image and video handlers reach a plugin.
func TestSurfaceFallsBackToSourceFormat(t *testing.T) {
	req := &pluginapi.ExecutorRequest{SourceFormat: "openai-image"}
	if got := surfaceOf(req); got != surfaceImage {
		t.Errorf("source format 未生效: %q", got)
	}
}

// The image catalogue entries must carry the registry type that admits them to
// /v1/images/generations; chat entries must not, or a client could ask the chat
// endpoint for a picture and receive prose.
func TestCatalogueTypesFollowCapability(t *testing.T) {
	models := buildCatalogue()
	seenImage, seenChat := false, false
	for _, model := range models {
		switch {
		case strings.HasSuffix(model.ID, "/image"), model.ID == "image":
			seenImage = true
			if model.Type != "openai-image" {
				t.Errorf("%s 的类型 = %q, 期望 openai-image", model.ID, model.Type)
			}
		case strings.HasSuffix(model.ID, "/default"), model.ID == "default":
			seenChat = true
			if model.Type != "" {
				t.Errorf("%s 不应带图片类型: %q", model.ID, model.Type)
			}
		}
	}
	if !seenImage || !seenChat {
		t.Fatalf("目录缺少样本: image=%t chat=%t", seenImage, seenChat)
	}
}

// The surface decides the capability: /v1/images/generations is a picture request
// even when the client sends a bare model name.
func TestModeForRequestPrefersSurface(t *testing.T) {
	if mode := modeForRequest(&chatRequest{Surface: surfaceImage, Model: "default"}); mode.Skill != skillImageGen || mode.Kind != kindImage {
		t.Errorf("图片端点未选中图像能力: %+v", mode)
	}
	if mode := modeForRequest(&chatRequest{Surface: surfaceVideo, Model: "default"}); mode.Skill != skillVideoGen || mode.Kind != kindVideo {
		t.Errorf("视频端点未选中视频能力: %+v", mode)
	}
	if mode := modeForRequest(&chatRequest{Surface: surfaceChat, Model: "doubao/image"}); mode.Skill != skillImageGen {
		t.Errorf("对话端点应尊重模型别名: %+v", mode)
	}
}

// A chat request without messages is an error; a media request legitimately has
// none and must not be rejected by the same check.
func TestPromptTextPerSurface(t *testing.T) {
	chat := &chatRequest{Surface: surfaceChat}
	if _, err := chat.promptText(); err == nil {
		t.Error("对话请求缺少 messages 时应报错")
	}

	image := &chatRequest{Surface: surfaceImage, Prompt: "一只橘猫"}
	text, err := image.promptText()
	if err != nil || text != "一只橘猫" {
		t.Errorf("图片提示词 = %q, err=%v", text, err)
	}

	video := &chatRequest{Surface: surfaceVideo, Prompt: "海浪"}
	if text, err := video.promptText(); err != nil || text != "海浪" {
		t.Errorf("视频提示词 = %q, err=%v", text, err)
	}
}

// A status poll must be recognised by its request id, otherwise every poll would
// start another generation.
func TestVideoRetrieveIsDetected(t *testing.T) {
	if id := videoJobIDFromPayload([]byte(`{"request_id":"video-abc"}`)); id != "video-abc" {
		t.Errorf("request_id 未识别: %q", id)
	}
	if id := videoJobIDFromPayload([]byte(`{"id":"video-xyz"}`)); id != "video-xyz" {
		t.Errorf("id 未识别: %q", id)
	}
	if id := videoJobIDFromPayload([]byte(`{"prompt":"海浪"}`)); id != "" {
		t.Errorf("生成请求被误判为查询: %q", id)
	}
}

// A finished job answers a poll with the same URL and a completed status.
func TestVideoJobStoreRoundTrip(t *testing.T) {
	videoJobs.mu.Lock()
	videoJobs.items["job-1"] = videoJob{
		ID: "job-1", Status: jobCompleted, Progress: 100,
		URL: "https://example.test/v.mp4", Model: "doubao/video",
		CreatedAt: time.Now(), Finished: time.Now(),
	}
	videoJobs.mu.Unlock()

	job, ok := videoJobs.get("job-1")
	if !ok || job.URL != "https://example.test/v.mp4" {
		t.Fatalf("任务未保存: %+v ok=%t", job, ok)
	}
	payload := videoJobPayload(job, 5)
	if payload["status"] != "completed" || payload["url"] != job.URL {
		t.Errorf("任务载荷异常: %+v", payload)
	}
	if _, ok := videoJobs.get("missing"); ok {
		t.Error("不存在的任务被返回")
	}
}

// 还在生成的任务要返回 in_progress 而不是报错，客户端才会继续轮询。
func TestVideoJobInProgressPayload(t *testing.T) {
	job := videoJob{ID: "job-2", Status: jobInProgress, Model: "doubao/video", CreatedAt: time.Now()}
	payload := videoJobPayload(job, 5)
	if payload["status"] != "in_progress" {
		t.Errorf("状态应为 in_progress: %+v", payload)
	}
	if _, hasURL := payload["url"]; hasURL {
		t.Error("未完成的任务不该带 url")
	}
	if payload["seconds"] != "5" {
		t.Errorf("时长缺失: %+v", payload["seconds"])
	}
}

// 失败的任务要带上错误信息。
func TestVideoJobFailedPayload(t *testing.T) {
	job := videoJob{ID: "job-3", Status: jobFailed, Error: "额度不足", CreatedAt: time.Now()}
	payload := videoJobPayload(job, 4)
	if payload["status"] != "failed" {
		t.Errorf("状态应为 failed: %+v", payload)
	}
	errObj, _ := payload["error"].(map[string]any)
	if errObj["message"] != "额度不足" {
		t.Errorf("错误信息丢失: %+v", payload)
	}
}
