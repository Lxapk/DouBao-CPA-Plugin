package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// 图片上传。
//
// 豆包的 /alice/bot/upload_file 接收 base64 图片数据，服务端自己存到 ImageX 并
// 返回资源地址。整条链路只需要登录 Cookie，不需要任何客户端签名 —— 这也是
// 唯一现实可行的上传方式：ImageX 的 ApplyImageUpload 需要 AWS V4 签名，而
// 签名密钥由服务端加密下发，插件侧算不出来。
//
// 请求与响应都来自 APK 的 BotImageUploadViewModel（com/larus/bot/impl/
// feature/edit/viewmodel），参数是 file_head{file_type,biz_type} + data。
//
// 实测：
//
//	file_type 必须是 MIME 类型（image/png），写 "png" 会被判参数错误
//	biz_type 传 1
//	响应 data.upload_uri 就是发送消息时要用的资源标识

// botUploadPath 是服务端代收图片的接口。
const botUploadPath = "/alice/bot/upload_file"

// 图片上传的结果。
type uploadedImage struct {
	// URI 是资源标识（tos-cn-i-…），发送消息时放进附件块。
	URI string
	// URL 是带签名的直链，仅用于展示。
	URL string
}

// uploadResult 是接口返回的结构。
type uploadResult struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		UploadURL string `json:"upload_url"`
		UploadURI string `json:"upload_uri"`
	} `json:"data"`
}

// uploadImagePart 把一张本地或外链图片上传到豆包。
//
// 外链图片先下载再上传：接口只接受二进制内容，不接受远程 URL，由插件代取。
func uploadImagePart(ctx context.Context, creds *credentials, part imagePart) (*uploadedImage, error) {
	raw, mime, errData := loadImageBytes(ctx, part)
	if errData != nil {
		return nil, errData
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("图片内容为空")
	}

	body, errMarshal := json.Marshal(map[string]any{
		"file_head": map[string]any{
			"file_type": mime,
			"biz_type":  1,
		},
		"data": base64.StdEncoding.EncodeToString(raw),
	})
	if errMarshal != nil {
		return nil, errMarshal
	}

	profile := creds.profile()
	endpoint := strings.TrimRight(profile.Host, "/") + botUploadPath + "?" + uploadQuery(creds)

	req, errNew := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if errNew != nil {
		return nil, errNew
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", creds.sanitizedCookies())
	req.Header.Set("Origin", profile.Host)
	req.Header.Set("Referer", profile.Host+"/chat/")
	req.Header.Set("User-Agent", webUserAgent)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, errDo := client.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("上传图片失败: %w", errDo)
	}
	defer resp.Body.Close()

	payload, errRead := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if errRead != nil {
		return nil, fmt.Errorf("读取上传响应失败: %w", errRead)
	}

	var out uploadResult
	if errUnmarshal := json.Unmarshal(payload, &out); errUnmarshal != nil {
		return nil, fmt.Errorf("上传响应不是 JSON：%s", truncate(string(payload), 200))
	}
	if out.Code != 0 {
		return nil, &upstreamError{
			Code:    out.Code,
			Message: statusCodeHint(out.Code, out.Msg),
		}
	}
	// upload_uri 缺失时退回从 upload_url 里解析。
	uri := strings.TrimSpace(out.Data.UploadURI)
	if uri == "" {
		uri = resourceURIFromURL(out.Data.UploadURL)
	}
	if uri == "" {
		return nil, fmt.Errorf("上传成功但没有返回资源地址：%s", truncate(string(payload), 200))
	}
	return &uploadedImage{URI: uri, URL: out.Data.UploadURL}, nil
}

// uploadQuery 是上传接口需要的 query，与 IM 请求共用同一组设备标识。
func uploadQuery(creds *credentials) string {
	profile := creds.profile()
	q := url.Values{}
	q.Set("version_code", doubaoVersionCode)
	q.Set("language", profile.Locale)
	q.Set("device_platform", "web")
	q.Set("doubao_device_platform", "web")
	q.Set("aid", profile.AID)
	q.Set("real_aid", profile.AID)
	q.Set("pkg_type", "release_version")
	q.Set("device_id", creds.DeviceID)
	q.Set("pc_version", profile.PCCVersion)
	q.Set("doubao_pc_version", profile.PCCVersion)
	q.Set("web_id", creds.WebID)
	q.Set("tea_uuid", creds.WebID)
	q.Set("region", profile.Region)
	q.Set("sys_region", profile.SysRegion)
	q.Set("samantha_web", "1")
	q.Set("web_platform", "browser")
	q.Set("use-olympus-account", "1")
	q.Set("web_tab_id", creds.WebTabID)
	return q.Encode()
}

// loadImageBytes 取出图片的二进制内容与 MIME 类型。
//
// 三种来源：
//
//	data URL    直接 base64 解码
//	http(s) 链接 下载（插件代为取回，因为接口只收二进制）
//	本地路径     读文件
func loadImageBytes(ctx context.Context, part imagePart) ([]byte, string, error) {
	if part.DataURL {
		raw, mime, ok := decodeDataURL(part.URL)
		if !ok {
			return nil, "", fmt.Errorf("无法解析内联图片")
		}
		if mime == "" {
			mime = detectMime(raw, "")
		}
		return raw, mime, nil
	}

	src := strings.TrimSpace(part.LocalPath)
	if src == "" {
		src = strings.TrimSpace(part.URL)
	}
	if src == "" {
		return nil, "", fmt.Errorf("图片没有可用的内容")
	}

	// 本地文件
	if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
		raw, errRead := readLocalFile(src)
		if errRead != nil {
			return nil, "", errRead
		}
		return raw, detectMime(raw, src), nil
	}

	// 远程地址：下载后上传
	req, errNew := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if errNew != nil {
		return nil, "", errNew
	}
	req.Header.Set("User-Agent", webUserAgent)
	resp, errDo := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if errDo != nil {
		return nil, "", fmt.Errorf("下载图片失败: %w", errDo)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("下载图片失败：HTTP %d", resp.StatusCode)
	}
	raw, errRead := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if errRead != nil {
		return nil, "", fmt.Errorf("读取图片失败: %w", errRead)
	}
	return raw, detectMime(raw, resp.Header.Get("Content-Type")), nil
}

// detectMime 依据文件头判断图片类型；接口要求 file_type 是 MIME。
func detectMime(raw []byte, hint string) string {
	if len(raw) >= 3 {
		switch {
		case raw[0] == 0x89 && raw[1] == 'P' && raw[2] == 'N':
			return "image/png"
		case raw[0] == 0xFF && raw[1] == 0xD8:
			return "image/jpeg"
		case string(raw[0:3]) == "GIF":
			return "image/gif"
		case len(raw) >= 12 && string(raw[0:4]) == "RIFF" && string(raw[8:12]) == "WEBP":
			return "image/webp"
		}
	}
	if strings.HasPrefix(strings.ToLower(hint), "image/") {
		return hint
	}
	lower := strings.ToLower(hint)
	switch {
	case strings.Contains(lower, "png"):
		return "image/png"
	case strings.Contains(lower, "webp"):
		return "image/webp"
	case strings.Contains(lower, "gif"):
		return "image/gif"
	}
	return "image/jpeg"
}

// readLocalFile 读取本地图片文件。
func readLocalFile(path string) ([]byte, error) {
	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		return nil, fmt.Errorf("读取本地图片失败: %w", errRead)
	}
	return raw, nil
}

// ensureImagesUploaded 把还没有资源标识的图片上传到豆包。
//
// 已经有 uri 的图片（用户直接给了豆包资源地址）跳过，不重复上传。
func ensureImagesUploaded(ctx context.Context, creds *credentials, images []imagePart) ([]imagePart, error) {
	if len(images) == 0 {
		return images, nil
	}
	out := make([]imagePart, 0, len(images))
	// 同一张图在一次请求里出现多次时只上传一次。
	cache := map[string]string{}
	for _, img := range images {
		if img.URI != "" {
			out = append(out, img)
			continue
		}
		key := firstNonEmpty(img.LocalPath, img.URL)
		if key == "" && img.DataURL {
			key = "data:" + img.URL
		}
		if uri, ok := cache[key]; ok {
			img.URI = uri
			out = append(out, img)
			continue
		}

		uploaded, errUpload := uploadImagePart(ctx, creds, img)
		if errUpload != nil {
			return nil, fmt.Errorf("图片上传失败: %w", errUpload)
		}
		img.URI = uploaded.URI
		if key != "" {
			cache[key] = uploaded.URI
		}
		logInfo("doubao: 图片已上传 " + uploaded.URI)
		out = append(out, img)
	}
	return out, nil
}
