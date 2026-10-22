package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// 从真实抓包复制来的豆包资源地址：uri 是 host 之后、`~` 之前那段。
func TestResourceURIFromURL(t *testing.T) {
	raw := "https://p3-flow-imagex-sign.byteimg.com/tos-cn-i-a9rns2rl98/rc_gen_image/643e326d977f4212affe53d976e73d82.jpeg~tplv-a9rns2rl98-image_raw.png?lk3s=8e244e95&x-expires=2107998783"
	want := "tos-cn-i-a9rns2rl98/rc_gen_image/643e326d977f4212affe53d976e73d82.jpeg"
	if got := resourceURIFromURL(raw); got != want {
		t.Errorf("resourceURIFromURL =\n  %q\n期望\n  %q", got, want)
	}

	// 非豆包域名不认。
	if got := resourceURIFromURL("https://example.com/a.png"); got != "" {
		t.Errorf("外部域名不应解析出 uri: %q", got)
	}
	if got := resourceURIFromURL("data:image/png;base64,AAA"); got != "" {
		t.Errorf("data URL 不应解析出 uri: %q", got)
	}
}

// OpenAI 的 image_url 有两种写法，都要能读出来。
func TestExtractImagesBothShapes(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		wantURI  string
		wantData bool
	}{
		{
			name: "对象形式 + 豆包资源",
			content: `[{"type":"text","text":"这是什么"},
				{"type":"image_url","image_url":{"url":"https://p3-flow-imagex-sign.byteimg.com/tos-cn-i-a9rns2rl98/a.jpeg~tplv-x.png?x=1"}}]`,
			wantURI: "tos-cn-i-a9rns2rl98/a.jpeg",
		},
		{
			name:    "字符串形式",
			content: `[{"type":"image_url","image_url":"https://p3-flow-imagex-sign.byteimg.com/tos-cn-i-a9rns2rl98/b.png~tplv-y.png"}]`,
			wantURI: "tos-cn-i-a9rns2rl98/b.png",
		},
		{
			name:     "data URL",
			content:  `[{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]`,
			wantData: true,
		},
	}
	for _, c := range cases {
		m := &chatMessage{Role: "user", Content: json.RawMessage(c.content)}
		imgs := m.extractImages()
		if len(imgs) != 1 {
			t.Errorf("%s: 取到 %d 张图", c.name, len(imgs))
			continue
		}
		if imgs[0].URI != c.wantURI {
			t.Errorf("%s: uri = %q, 期望 %q", c.name, imgs[0].URI, c.wantURI)
		}
		if imgs[0].DataURL != c.wantData {
			t.Errorf("%s: dataURL = %v", c.name, imgs[0].DataURL)
		}
	}
}

// 纯文本消息不应产生图片。
func TestExtractImagesFromPlainText(t *testing.T) {
	m := &chatMessage{Role: "user", Content: json.RawMessage(`"你好"`)}
	if imgs := m.extractImages(); len(imgs) != 0 {
		t.Errorf("纯文本产生了 %d 张图", len(imgs))
	}
}

// 附件块必须和真实抓包结构一致：block_type=10052，
// content.attachment_block.attachments[0].image.uri 是资源标识。
func TestAttachmentBlockMatchesCapturedShape(t *testing.T) {
	blocks, errBuild := buildAttachmentBlocks([]imagePart{{URI: "tos-cn-i-a9rns2rl98/abc.png"}})
	if errBuild != nil {
		t.Fatalf("构造失败: %v", errBuild)
	}
	if len(blocks) != 1 {
		t.Fatalf("块数 = %d", len(blocks))
	}
	if blocks[0].BlockType != 10052 {
		t.Errorf("block_type = %d, 期望 10052", blocks[0].BlockType)
	}

	raw, _ := json.Marshal(blocks[0].Content)
	var decoded struct {
		AttachmentBlock struct {
			Attachments []struct {
				Type  int `json:"type"`
				Image struct {
					Name     string `json:"name"`
					URI      string `json:"uri"`
					ImageOri struct {
						Format string `json:"format"`
					} `json:"image_ori"`
				} `json:"image"`
				ReviewState int `json:"review_state"`
				UploadState int `json:"upload_status"`
				Progress    int `json:"progress"`
			} `json:"attachments"`
		} `json:"attachment_block"`
	}
	if errUnmarshal := json.Unmarshal(raw, &decoded); errUnmarshal != nil {
		t.Fatalf("结构解析失败: %v", errUnmarshal)
	}
	att := decoded.AttachmentBlock.Attachments
	if len(att) != 1 {
		t.Fatalf("attachments = %d", len(att))
	}
	if att[0].Type != 1 || att[0].Image.URI != "tos-cn-i-a9rns2rl98/abc.png" {
		t.Errorf("附件内容错误: %+v", att[0])
	}
	if att[0].Image.ImageOri.Format != "png" {
		t.Errorf("format = %q", att[0].Image.ImageOri.Format)
	}
	if att[0].ReviewState != 1 || att[0].UploadState != 1 || att[0].Progress != 100 {
		t.Errorf("状态字段错误: %+v", att[0])
	}
}

// 本地文件、外链、base64 现在走上传，不再直接拒绝；
// 只有拿不到任何内容的空图片才报错。
func TestEmptyImageRejected(t *testing.T) {
	if err := checkImagesSupported([]imagePart{{}}); err == nil {
		t.Error("空图片应被拒绝")
	}
	for _, ok := range []imagePart{
		{URL: "https://example.com/a.png"},
		{DataURL: true, URL: "data:image/png;base64,AAA"},
		{LocalPath: "/tmp/a.jpg"},
		{URI: "tos-cn-i-x/a.png"},
	} {
		if err := checkImagesSupported([]imagePart{ok}); err != nil {
			t.Errorf("不应拒绝 %+v: %v", ok, err)
		}
	}
}

// 一条消息里同时有图片和文字时，附件块在前、文本块在后（与网页端一致）。
func TestContentBlocksOrder(t *testing.T) {
	blocks := buildContentBlocks("这是什么", []imagePart{{URI: "tos-cn-i-x/a.jpeg"}})
	if len(blocks) != 2 {
		t.Fatalf("块数 = %d", len(blocks))
	}
	if blocks[0].BlockType != blockTypeAttachment {
		t.Errorf("第一个块应是附件: %d", blocks[0].BlockType)
	}
	if blocks[1].BlockType != blockTypeText {
		t.Errorf("第二个块应是文本: %d", blocks[1].BlockType)
	}
	if !strings.Contains(blocks[1].Content.TextBlock.Text, "这是什么") {
		t.Errorf("文本丢失")
	}
}

// 只发图片、不带文字也要能构造出请求。
func TestImageOnlyMessage(t *testing.T) {
	blocks := buildContentBlocks("", []imagePart{{URI: "tos-cn-i-x/a.png"}})
	if len(blocks) != 1 || blocks[0].BlockType != blockTypeAttachment {
		t.Errorf("纯图片消息构造错误: %+v", blocks)
	}
}
