package main

import (
	"strings"
	"testing"
)

// file_type 必须是 MIME 类型，写 "png" 会被判参数错误（实测 code 710010202）。
func TestDetectMime(t *testing.T) {
	cases := []struct {
		head []byte
		hint string
		want string
	}{
		{[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, "", "image/png"},
		{[]byte{0xFF, 0xD8, 0xFF, 0xE0}, "", "image/jpeg"},
		{[]byte("RIFF\x00\x00\x00\x00WEBP"), "", "image/webp"},
		{[]byte("GIF89a"), "", "image/gif"},
		{[]byte{0, 1, 2}, "image/png", "image/png"},
		{[]byte{0, 1, 2}, "application/octet-stream", "image/jpeg"},
	}
	for _, c := range cases {
		if got := detectMime(c.head, c.hint); got != c.want {
			t.Errorf("detectMime(%x,%q) = %q, 期望 %q", c.head[:4], c.hint, got, c.want)
		}
	}
}

// data URL 要能解出字节与 MIME。
func TestDecodeDataURL(t *testing.T) {
	// "PNG" 的 base64
	raw, mime, ok := decodeDataURL("data:image/png;base64,UE5H")
	if !ok || string(raw) != "PNG" || mime != "image/png" {
		t.Errorf("decodeDataURL = %q %q %v", raw, mime, ok)
	}
	if _, _, ok := decodeDataURL("https://x/a.png"); ok {
		t.Error("非 data URL 不应成功")
	}
}

// 资源 uri 可以直接作为图片地址传入（上传接口返回的就是这种形式）。
func TestResourceURIAcceptsBareURI(t *testing.T) {
	uri := "tos-cn-i-a9rns2rl98/rc/bot_create/x.image/png"
	m := &chatMessage{Role: "user", Content: []byte(`[{"type":"image_url","image_url":{"url":"` + uri + `"}}]`)}
	imgs := m.extractImages()
	if len(imgs) != 1 || imgs[0].URI != uri {
		t.Fatalf("解析失败: %+v", imgs)
	}
}

// 本地路径要能识别出来，交给上传流程。
func TestLocalPathRecognised(t *testing.T) {
	m := &chatMessage{Role: "user", Content: []byte(`[{"type":"image_url","image_url":{"url":"/tmp/a.jpg"}}]`)}
	imgs := m.extractImages()
	if len(imgs) != 1 || imgs[0].LocalPath != "/tmp/a.jpg" {
		t.Fatalf("本地路径未识别: %+v", imgs)
	}

	// file:// 形式
	m2 := &chatMessage{Role: "user", Content: []byte(`[{"type":"image_url","image_url":{"url":"file:///sdcard/a.png"}}]`)}
	imgs2 := m2.extractImages()
	if len(imgs2) != 1 || imgs2[0].LocalPath != "/sdcard/a.png" {
		t.Fatalf("file:// 未识别: %+v", imgs2)
	}
}

// 外链与 base64 现在会走上传，不再被拒绝。
func TestUploadableImagesAccepted(t *testing.T) {
	for _, img := range []imagePart{
		{URL: "https://example.com/a.png"},
		{DataURL: true, URL: "data:image/png;base64,AAA"},
		{LocalPath: "/tmp/a.jpg"},
		{URI: "tos-cn-i-x/a.png"},
	} {
		if err := checkImagesSupported([]imagePart{img}); err != nil {
			t.Errorf("不应拒绝: %+v", img)
		}
	}
}

// 上传接口的请求体形状：file_head{file_type,biz_type} + data(base64)。
func TestUploadPayloadShape(t *testing.T) {
	src := readSourceForTest(t, "image_upload.go")
	for _, want := range []string{
		`"file_head"`, `"file_type"`, `"biz_type"`, `"data"`,
		"base64.StdEncoding.EncodeToString", "botUploadPath",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("上传实现缺少 %s", want)
		}
	}
}
