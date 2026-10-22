package main

import (
	"fmt"
	"strconv"
	"strings"
)

// 图片比例、视频比例与时长的落地方式。
//
// 豆包的生成参数不在请求体里，而是由 agent 在规划阶段决定：响应中每张图的
// gen_params 里都带着 {"prompt":…,"ratio":"1:1"}，这个 ratio 是服务端自己填的，
// 与请求里的任何字段都无关。实测把 aspect_ratio / ratio / size / width+height
// 放进 model_extra_params，返回的仍是 2048x2048，部分写法还会被网关判为
// 参数错误。
//
// 所以这些参数唯一可靠的落地方式是写进提示词，由 agent 在规划时采用。这也是
// 网页端的行为：用户在对话框里说"要横版"，规划阶段就把 ratio 定成 16:9。
//
// 下面把 OpenAI 风格的 size / aspect_ratio / seconds 归一化后拼进提示词。

// 常见尺寸到比例的映射，用于把 1024x1024 这类写法转成 1:1。
func ratioFromSize(size string) string {
	s := strings.ToLower(strings.TrimSpace(size))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "×", "x")
	s = strings.ReplaceAll(s, "：", ":")
	if s == "" {
		return ""
	}
	// 空值以外的非数字写法原样当比例用（"16:9"）。
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		if strings.Contains(s, ":") {
			return s
		}
		return ""
	}
	w, errW := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(parts[0], "px"), ""))
	h, errH := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(parts[1], "px"), ""))
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return ""
	}
	g := gcd(w, h)
	rw, rh := w/g, h/g
	ratio := fmt.Sprintf("%d:%d", rw, rh)
	// 只接受常见比例，避免把 1024x1000 算成 128:125 这种没意义的提示。
	switch ratio {
	case "1:1", "4:3", "3:4", "16:9", "9:16", "3:2", "2:3", "21:9":
		return ratio
	}
	return ""
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}

// 归一化比例写法：16x9、16：9、16 / 9 都变成 16:9。
func normalizeRatio(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "×", "x")
	s = strings.ReplaceAll(s, "：", ":")
	if s == "" {
		return ""
	}
	if strings.Contains(s, ":") {
		parts := strings.SplitN(s, ":", 2)
		return strings.TrimSpace(parts[0]) + ":" + strings.TrimSpace(parts[1])
	}
	if ratio := ratioFromSize(s); ratio != "" {
		return ratio
	}
	return ""
}

// 常见视频时长，超出范围的值截到模型支持的区间。
func normalizeSeconds(n int) int {
	switch {
	case n <= 0:
		return 0
	case n < 4:
		return 4
	case n > 12:
		return 12
	}
	return n
}

// generationHints 把请求里的比例与时长拼成提示词后缀。
//
// 图片只要比例，视频要比例加时长。没有可用的参数时返回空串，提示词保持原样。
func generationHints(kind string, req *chatRequest) string {
	if req == nil {
		return ""
	}
	ratio := normalizeRatio(req.AspectRatio)
	if ratio == "" {
		ratio = ratioFromSize(req.Size)
	}

	var parts []string
	switch kind {
	case kindVideo:
		if ratio != "" {
			parts = append(parts, "画面比例 "+ratio)
		}
		if secs := normalizeSeconds(req.Seconds); secs > 0 {
			parts = append(parts, "时长 "+strconv.Itoa(secs)+" 秒")
		}
	case kindImage:
		if ratio != "" {
			parts = append(parts, "画面比例 "+ratio)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n\n（生成要求：" + strings.Join(parts, "，") + "）"
}

// withGenerationHints 在提示词末尾附上生成参数。
func withGenerationHints(prompt string, kind string, req *chatRequest) string {
	hints := generationHints(kind, req)
	if hints == "" {
		return prompt
	}
	return strings.TrimRight(prompt, "\n") + hints
}
