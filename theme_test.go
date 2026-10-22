package main

import (
	"strings"
	"testing"
)

// 三套主题都必须有完整的一套 token，否则某个主题下会出现读不清的控件。
func TestThemesCoverAllHostValues(t *testing.T) {
	for _, theme := range []string{"dark", "black", "white", "light", "kraft", "paper"} {
		selector := `:root[data-theme="` + theme + `"]`
		if !strings.Contains(uiCSS, selector) {
			t.Errorf("缺少主题 %s 的规则", theme)
		}
	}
}

// 每个主题块都要给出全部关键 token —— 少一个就会继承到别的主题的颜色。
func TestThemeBlocksAreComplete(t *testing.T) {
	required := []string{
		"--bg-secondary", "--bg-primary", "--bg-tertiary", "--bg-hover",
		"--text-primary", "--text-secondary", "--text-tertiary",
		"--border-color", "--primary-color",
		"--success-color", "--warning-color", "--error-color",
	}
	// 按主题块切分：从选择器起到下一个顶层 "}" 的规则开始处。
	blocks := map[string]string{}
	for _, theme := range []string{"dark", "black", "white", "kraft"} {
		start := strings.Index(uiCSS, `:root[data-theme="`+theme+`"]`)
		if start < 0 {
			t.Fatalf("缺少主题 %s", theme)
		}
		end := strings.Index(uiCSS[start:], "\n}")
		if end < 0 {
			t.Fatalf("主题 %s 的块没有结束", theme)
		}
		blocks[theme] = uiCSS[start : start+end]
	}
	for theme, block := range blocks {
		for _, token := range required {
			if !strings.Contains(block, token+":") {
				t.Errorf("主题 %s 缺少 %s", theme, token)
			}
		}
	}
}

// 跟随系统时的纸色必须和 kraft 一致，否则切换主题会看到两套纸色。
func TestPaperMatchesKraft(t *testing.T) {
	kraft := uiCSS[strings.Index(uiCSS, `:root[data-theme="kraft"]`):]
	kraft = kraft[:strings.Index(kraft, "\n}")]
	media := uiCSS[strings.Index(uiCSS, "@media (prefers-color-scheme: light)"):]
	media = media[:strings.Index(media, "\n}")]

	for _, token := range []string{"--bg-secondary", "--bg-primary", "--text-primary"} {
		kv := func(s string) string {
			i := strings.Index(s, token+":")
			if i < 0 {
				return ""
			}
			rest := s[i+len(token)+1:]
			if j := strings.Index(rest, ";"); j >= 0 {
				rest = rest[:j]
			}
			return strings.TrimSpace(rest)
		}
		if got, want := kv(media), kv(kraft); got != want {
			t.Errorf("%s 在跟随系统与 kraft 下不一致: %q vs %q", token, got, want)
		}
	}
}

// 脚本不能再用白名单过滤主题值，否则 host 新增主题时会退回暗色。
func TestThemePassthrough(t *testing.T) {
	script := panelScript()
	if strings.Contains(script, `theme === "dark" || theme === "white"`) {
		t.Error("仍在白名单过滤主题值")
	}
	if !strings.Contains(script, "root.setAttribute(\"data-theme\", theme)") {
		t.Error("主题值未透传")
	}
	if !strings.Contains(script, "root.removeAttribute(\"data-theme\")") {
		t.Error("跟随系统时未移除属性")
	}
}
