package main

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// 图片输入。
//
// OpenAI 的多模态消息把图片放在 content 数组里：
//
//	{"type":"image_url","image_url":{"url":"https://…"}}
//	{"type":"image_url","image_url":{"url":"data:image/png;base64,…"}}
//
// 豆包这条链路要求图片先成为一个"资源"：上传到字节 ImageX 之后得到一个
// uri（形如 tos-cn-i-<service>/<hash>.png），发送消息时放在
// block_type=10052 的附件块里，image.uri 指向它。
//
// 上传本身需要服务端签发 Authorization（ImageX 的签名），插件离线复现不了，
// 因此这里分两种情况处理：
//
//  1. 客户端直接给了豆包的资源 uri/url —— 直接构造附件块，无需上传。
//  2. 给了外链 URL 或 base64 —— 无法上传，明确报错，不静默丢图。
//
// 第 2 种以前是静默丢弃的，用户看到的是模型说"没收到图"，很难排查。

// imagePart 是一张待发送的图片。
type imagePart struct {
	// URI 是豆包资源标识（tos-cn-i-…），有它就能直接发。
	URI string
	// URL 是图片地址；URI 为空时用它判断能否直接从已有资源引用。
	URL string
	// DataURL 是 data: 开头的内联图片，无法直接使用。
	DataURL bool
	// Detail 是 OpenAI 的 low/high 提示，仅用于日志。
	Detail string
}

// extractImages 取出消息里的所有图片。
func (m *chatMessage) extractImages() []imagePart {
	if len(m.Content) == 0 {
		return nil
	}
	// 字符串形式不可能带图片。
	var s string
	if errUnmarshal := json.Unmarshal(m.Content, &s); errUnmarshal == nil {
		return nil
	}

	var parts []struct {
		Type     string          `json:"type"`
		ImageURL json.RawMessage `json:"image_url"`
	}
	if errUnmarshal := json.Unmarshal(m.Content, &parts); errUnmarshal != nil {
		return nil
	}

	var out []imagePart
	for _, p := range parts {
		raw, detail := parseImageURLField(p.ImageURL)
		if raw == "" {
			continue
		}

		part := imagePart{Detail: detail, URL: raw}
		if strings.HasPrefix(raw, "data:") {
			part.DataURL = true
			part.URL = ""
		} else if uri := resourceURIFromURL(raw); uri != "" {
			// 豆包的资源地址：uri 才是发送时要的东西。
			part.URI = uri
		}
		out = append(out, part)
	}
	return out
}

// parseImageURLField 兼容 image_url 的两种写法。
//
// OpenAI 规范是对象 {"url":..., "detail":...}；也有客户端直接给字符串。
func parseImageURLField(raw json.RawMessage) (url string, detail string) {
	if len(raw) == 0 {
		return "", ""
	}
	var asString string
	if errUnmarshal := json.Unmarshal(raw, &asString); errUnmarshal == nil {
		return strings.TrimSpace(asString), ""
	}
	var asObject struct {
		URL    string `json:"url"`
		Detail string `json:"detail"`
	}
	if errUnmarshal := json.Unmarshal(raw, &asObject); errUnmarshal == nil {
		return strings.TrimSpace(asObject.URL), strings.TrimSpace(asObject.Detail)
	}
	return "", ""
}

// resourceURIFromURL 从豆包图片地址里取出资源 uri。
//
// 形如：
//
//	https://p3-flow-imagex-sign.byteimg.com/tos-cn-i-a9rns2rl98/xxx.jpeg~tplv-…?…
//
// 取 host 后面、`~` 之前的那段，就是 uri。不是豆包域名则返回空。
func resourceURIFromURL(raw string) string {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return ""
	}
	if !strings.Contains(s, "byteimg.com") && !strings.Contains(s, "doubao.com") {
		return ""
	}
	idx := strings.Index(s, "://")
	if idx < 0 {
		return ""
	}
	rest := s[idx+3:]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return ""
	}
	path := rest[slash+1:]
	if cut := strings.IndexByte(path, '~'); cut >= 0 {
		path = path[:cut]
	}
	if cut := strings.IndexByte(path, '?'); cut >= 0 {
		path = path[:cut]
	}
	// 资源 uri 一定以 tos-cn-i- 开头。
	if !strings.HasPrefix(path, "tos-cn-i-") {
		return ""
	}
	return path
}

// attachmentBlock 是一个附件块（block_type=10052）。
//
// 字段取自真实抓包（网页端发图时的请求体），层级是：
//
//	content.attachment_block.attachments[0]
//	  type=1, identifier=<字符串>, image{name,uri,image_ori{url,width,height,format,url_formats}},
//	  parse_state, review_state, upload_status, progress, src
//
// attachmentBlock 就是 attachment_block 的值本身。
type attachmentBlock struct {
	Attachments []attachmentItem `json:"attachments"`
}

type attachmentItem struct {
	Type        int         `json:"type"`
	Identifier  string      `json:"identifier"`
	Image       uploadImage `json:"image"`
	ParseState  int         `json:"parse_state"`
	ReviewState int         `json:"review_state"`
	UploadState int         `json:"upload_status"`
	Progress    int         `json:"progress"`
	Src         string      `json:"src"`
}

type uploadImage struct {
	Name     string    `json:"name"`
	URI      string    `json:"uri"`
	ImageOri imageMeta `json:"image_ori"`
}

type imageMeta struct {
	URL        string         `json:"url"`
	Width      int            `json:"width"`
	Height     int            `json:"height"`
	Format     string         `json:"format"`
	URLFormats map[string]any `json:"url_formats"`
}

// checkImagesSupported 在发请求前确认每张图都能用。
//
// 提前拒绝比发出去再让模型说"没收到"更清楚：用户能立刻知道是图片通道的问题，
// 而不是怀疑模型理解能力。
func checkImagesSupported(images []imagePart) error {
	for _, img := range images {
		if img.URI != "" {
			continue
		}
		if img.DataURL {
			return errImageNeedsUpload("base64 内联图片")
		}
		return errImageNeedsUpload("外部图片链接")
	}
	return nil
}

// buildAttachmentBlocks 把图片转成附件块。
//
// 只能使用已经存在于豆包资源里的图（有 uri）。外链与 base64 需要先上传，
// 上传能力尚未实现，这里返回错误让调用方如实告知用户，而不是丢图后
// 让模型说"没收到图片"。
func buildAttachmentBlocks(images []imagePart) ([]requestBlock, error) {
	if len(images) == 0 {
		return nil, nil
	}
	out := make([]requestBlock, 0, len(images))
	for _, img := range images {
		if img.URI == "" {
			if img.DataURL {
				return nil, errImageNeedsUpload("base64 内联图片")
			}
			return nil, errImageNeedsUpload("外部图片链接")
		}
		item := attachmentItem{
			Type:       1,
			Identifier: "attachment-image-" + img.URI,
			Image: uploadImage{
				Name: "Image",
				URI:  img.URI,
				ImageOri: imageMeta{
					Format:     imageFormatFromURI(img.URI),
					URLFormats: map[string]any{},
				},
			},
			ReviewState: 1,
			UploadState: 1,
			Progress:    100,
		}
		out = append(out, requestBlock{
			BlockType:    blockTypeAttachment,
			Content:      requestBlockBody{Attachment: &attachmentBlock{Attachments: []attachmentItem{item}}},
			BlockID:      randomUUID(),
			ParentID:     "",
			MetaInfo:     []any{},
			AppendFields: []any{},
		})
	}
	return out, nil
}

func imageFormatFromURI(uri string) string {
	lower := strings.ToLower(uri)
	switch {
	case strings.HasSuffix(lower, ".png"):
		return "png"
	case strings.HasSuffix(lower, ".webp"):
		return "webp"
	case strings.HasSuffix(lower, ".gif"):
		return "gif"
	default:
		return "jpeg"
	}
}

// errImageNeedsUpload 说明图片必须先上传到豆包才能发送。
type errImageNeedsUpload string

func (e errImageNeedsUpload) Error() string {
	return string(e) + " 需要先上传到豆包资源，当前版本尚未支持；" +
		"请改用豆包已有的图片链接，或先用文字描述图片内容。"
}

// md5Hex 计算内容摘要，附件块里有一个 md5 字段（可选）。
func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// decodeDataURL 解开 data: 内联图片，upload 实现就绪后使用。
func decodeDataURL(raw string) ([]byte, string, bool) {
	const marker = ";base64,"
	idx := strings.Index(raw, marker)
	if idx < 0 {
		return nil, "", false
	}
	meta := strings.TrimPrefix(raw[:idx], "data:")
	mime := meta
	if semi := strings.IndexByte(mime, ';'); semi >= 0 {
		mime = mime[:semi]
	}
	decoded, errDecode := base64.StdEncoding.DecodeString(raw[idx+len(marker):])
	if errDecode != nil {
		return nil, "", false
	}
	return decoded, mime, true
}
