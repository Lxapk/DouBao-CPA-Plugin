package main

import (
	"context"
	"fmt"
	"strings"
)

// Resolving a generated image to a downloadable URL.
//
// A generated image is identified by a resource key, not a URL:
//
//	tos-cn-i-<bucket>/rc_gen_image/<hash>.jpeg
//
// That key is not fetchable on its own. The upstream exposes two ways to turn it
// into a link, and they differ in quality:
//
//	image_thumb.url    already present in the stream, watermarked, low-res
//	/creativity/resource/get_without_watermark   a fresh signed URL, full size
//
// The stream's own URL is used when present, because it needs no extra call. The
// no-watermark call is made when the caller wants the full-quality asset, which
// is what an OpenAI image response implies.

// watermarkRequest is the body of the no-watermark call.
type watermarkRequest struct {
	URI []string `json:"uri"`
}

// watermarkResponse is the reply.
type watermarkResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		WithoutWatermark bool                        `json:"without_watermark"`
		DownloadImage    map[string]watermarkVariant `json:"download_image"`
		PreviewImage     map[string]watermarkVariant `json:"preview_image"`
	} `json:"data"`
}

type watermarkVariant struct {
	URL    string `json:"url"`
	URI    string `json:"uri"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// resolveNoWatermark turns resource keys into full-size signed URLs.
//
// It returns a map of key to URL. A key that the upstream does not resolve is
// absent from the map rather than an error, so one bad key does not discard the
// results for the others.
func (c *client) resolveNoWatermark(ctx context.Context, keys []string) (map[string]watermarkVariant, error) {
	out := make(map[string]watermarkVariant)
	if len(keys) == 0 {
		return out, nil
	}

	var resp watermarkResponse
	err := c.postJSONInto(ctx, "/creativity/resource/get_without_watermark",
		watermarkRequest{URI: keys}, &resp)
	if err != nil {
		return out, err
	}
	if resp.Code != 0 {
		return out, &upstreamError{
			Code:    resp.Code,
			Message: fmt.Sprintf("获取无水印图片失败：%s", resp.Msg),
		}
	}

	for key, v := range resp.Data.DownloadImage {
		if v.URL != "" {
			out[key] = v
		}
	}
	for key, v := range resp.Data.PreviewImage {
		if _, exists := out[key]; !exists && v.URL != "" {
			out[key] = v
		}
	}
	return out, nil
}

// hydrateAssets upgrades generated assets to full-size no-watermark URLs.
//
// Assets that already carry a usable URL and are not images are left alone: a
// video's URL is already direct, and a text-only reply has nothing to resolve.
func (c *client) hydrateAssets(ctx context.Context, assets []creationAsset) []creationAsset {
	var keys []string
	seen := map[string]bool{}
	for _, a := range assets {
		if a.Key == "" || a.isVideo() {
			continue
		}
		if seen[a.Key] {
			continue
		}
		seen[a.Key] = true
		keys = append(keys, a.Key)
	}
	if len(keys) == 0 {
		return assets
	}

	resolved, errResolve := c.resolveNoWatermark(ctx, keys)
	if errResolve != nil {
		// The stream's own thumbnail URL is still usable, so a failure here
		// degrades quality rather than failing the request.
		c.logf("无水印解析失败（回退到缩略图）：%v", errResolve)
		return assets
	}

	for i := range assets {
		v, ok := resolved[assets[i].Key]
		if !ok || v.URL == "" {
			continue
		}
		assets[i].URL = v.URL
		if v.Width > 0 {
			assets[i].Width = v.Width
		}
		if v.Height > 0 {
			assets[i].Height = v.Height
		}
	}
	return assets
}

// normalizeResourceKey strips a scheme or host prefix that a caller may have
// included, leaving the bare resource key the API expects.
func normalizeResourceKey(raw string) string {
	s := strings.TrimSpace(raw)
	if idx := strings.Index(s, "rc_gen_image/"); idx >= 0 {
		return s[idx:]
	}
	s = strings.TrimPrefix(s, "tos-cn-i-")
	if idx := strings.Index(s, "/"); idx >= 0 {
		// Re-add the bucket prefix the API expects.
		return "tos-cn-i-" + s
	}
	return s
}
