package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 手机号 + 短信验证码登录。
//
// 豆包没有 OAuth 回调，网页端登录后凭据就是一串 Cookie。早先的做法是让用户
// 自己开浏览器、翻开发者工具、复制 Cookie 粘进来——步骤多、容易漏字段，而且
// 「复制得够不够全」只能等第一次调用失败才知道。
//
// 这里改成插件直接走 passport 的短信登录接口，登录成功后从响应的 Set-Cookie
// 里取出那一串 Cookie，用户只需要输入手机号和收到的验证码。
//
// 两个接口都来自 APK 的 passport 客户端（com/bytedance/sdk/account/impl/d.smali）：
//
//	POST /passport/mobile/sms_login_only/        发送验证码
//	POST /passport/mobile/validate_code/v1/      用验证码换登录态
//
// 参数里 mobile 与 code 都不是明文：客户端用 StringUtils.encryptWithXor 处理过，
// 即每个字节异或 5 之后转十六进制。这是固定算法，不依赖设备，照做即可。

// passportEndpoints 是登录用到的两个路径。
const (
	passportSendCodePath = "/passport/mobile/sms_login_only/"
	passportLoginPath    = "/passport/mobile/validate_code/v1/"
	// smsLoginType 是 validate_code 的 type 参数。客户端固定传 28，对应
	// 「短信验证码登录」这一种校验方式。
	smsLoginType = "28"
)

// phoneLoginError 把上游的拒绝翻译成人能处理的一句话。
type phoneLoginError struct {
	Code    string
	Message string
	Hint    string
}

func (e *phoneLoginError) Error() string {
	if e.Hint != "" {
		return e.Message + "（" + e.Hint + "）"
	}
	return e.Message
}

// xorHex 复现客户端的 encryptWithXor：逐字节异或 5，再转十六进制。
func xorHex(value string) string {
	src := []byte(value)
	for i := range src {
		src[i] ^= 5
	}
	return hex.EncodeToString(src)
}

// passportRequest 组装一次 passport 调用。
//
// 这些接口不像 IM 那套需要特殊的 Content-Type；它们就是普通表单提交，
// 响应是 JSON，登录态在响应头里。
func passportRequest(ctx context.Context, host string, path string, form url.Values) (*http.Response, []byte, error) {
	body := form.Encode()
	req, errNew := http.NewRequestWithContext(ctx, http.MethodPost, host+path, strings.NewReader(body))
	if errNew != nil {
		return nil, nil, errNew
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Referer", host+"/")

	resp, errDo := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if errDo != nil {
		return nil, nil, fmt.Errorf("请求上游失败: %w", errDo)
	}
	defer resp.Body.Close()

	payload, errRead := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if errRead != nil {
		return resp, nil, fmt.Errorf("读取上游响应失败: %w", errRead)
	}
	return resp, payload, nil
}

// passportError 判断返回里是不是错误，并给出可操作的解释。
//
// passport 用 message 字段报错，但不同错误的信息量差别很大：有的直接说手机号
// 格式不对，有的只给一个错误码。能归类的那几种补一句怎么办，其余原样透出。
func passportError(payload []byte) *phoneLoginError {
	var out struct {
		Message string `json:"message"`
		Data    struct {
			Description string `json:"description"`
		} `json:"data"`
	}
	_ = json.Unmarshal(payload, &out)

	msg := strings.TrimSpace(out.Message)
	if msg == "" {
		msg = strings.TrimSpace(out.Data.Description)
	}
	if msg == "" {
		return nil
	}

	// 成功的响应不会带 message，所以走到这里一定是失败。
	lower := strings.ToLower(msg)
	err := &phoneLoginError{Message: msg}
	switch {
	case strings.Contains(msg, "频繁") || strings.Contains(lower, "too many"):
		err.Hint = "发送太频繁，等一分钟再试"
	case strings.Contains(msg, "验证码") || strings.Contains(lower, "code"):
		err.Hint = "验证码不对或已过期，重新获取一次"
	case strings.Contains(msg, "手机号") || strings.Contains(lower, "mobile"):
		err.Hint = "手机号需要是 11 位国内号码"
	case strings.Contains(lower, "risk") || strings.Contains(msg, "风控"):
		err.Hint = "该号码触发了风控，需要先在官方客户端登录一次"
	}
	return err
}

// sendLoginCode 发送短信验证码。
func sendLoginCode(ctx context.Context, r realm, phone string) error {
	profile := profileFor(r)
	form := url.Values{}
	form.Set("mobile", xorHex(url.QueryEscape(phone)))
	form.Set("mix_mode", "1")
	form.Set("type", xorHex(smsLoginType))
	// 客户端还会带这些固定参数；缺了会被当成非官方客户端。
	form.Set("aid", profile.AID)
	form.Set("service", profile.AID)
	form.Set("device_platform", "android")

	resp, payload, errDo := passportRequest(ctx, profile.Host, passportSendCodePath, form)
	if errDo != nil {
		return errDo
	}
	if err := passportError(payload); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("发送验证码失败：上游返回 HTTP %d", resp.StatusCode)
	}
	return nil
}

// loginWithCode 用验证码换登录态，并返回登录后的 Cookie。
//
// 登录态只在响应头里，不在 body：passport 通过 Set-Cookie 下发 sessionid 等
// 一组 Cookie，这正是网页端登录后浏览器持有的东西。
func loginWithCode(ctx context.Context, r realm, phone string, code string) (string, error) {
	profile := profileFor(r)
	form := url.Values{}
	form.Set("mobile", xorHex(url.QueryEscape(phone)))
	form.Set("code", xorHex(code))
	form.Set("mix_mode", "1")
	form.Set("fixed_mix_mode", "1")
	form.Set("type", xorHex(smsLoginType))
	form.Set("need_ticket", "1")
	form.Set("aid", profile.AID)
	form.Set("service", profile.AID)
	form.Set("device_platform", "android")

	resp, payload, errDo := passportRequest(ctx, profile.Host, passportLoginPath, form)
	if errDo != nil {
		return "", errDo
	}
	if err := passportError(payload); err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("登录失败：上游返回 HTTP %d", resp.StatusCode)
	}

	cookies := cookiesFromResponse(resp)
	if !hasLoginCookie(cookies) {
		return "", errors.New("登录接口没有下发 sessionid：号码或验证码可能不对，也可能是上游改了接口")
	}
	return cookies, nil
}

// cookiesFromResponse 把 Set-Cookie 拼成请求头用的形式。
//
// 只取 name=value，属性（Path、Domain、Expires）一条都不要：这是要放进
// Cookie 请求头的值，属性留在里面会让上游解析出错。
func cookiesFromResponse(resp *http.Response) string {
	var parts []string
	for _, raw := range resp.Header.Values("Set-Cookie") {
		pair, _, _ := strings.Cut(raw, ";")
		pair = strings.TrimSpace(pair)
		if pair == "" || !strings.Contains(pair, "=") {
			continue
		}
		parts = append(parts, pair)
	}
	return normalizeCookieSeparators(strings.Join(parts, "; "))
}

// hasLoginCookie 判断这串 Cookie 够不够用。
//
// 与凭据校验用的是同一组必要条件：sessionid 说明登录成立。
func hasLoginCookie(cookies string) bool {
	jar := (&credentials{Cookies: cookies}).cookieMap()
	return jar["sessionid"] != "" || jar["sessionid_ss"] != "" || jar["sid_tt"] != ""
}

// accountIdentity 从已登录的 Cookie 里问出账号标识，用于给账号起名字。
//
// 失败不算错误：登录已经成功，只是名字拿不到，退回用手机号后四位命名。
func accountIdentity(ctx context.Context, r realm, cookies string) (nickname string, secUserID string, uid string) {
	creds := (&credentials{Realm: r, Cookies: cookies}).withDefaults()
	launch, errLaunch := probeCredential(creds)
	if errLaunch != nil {
		logInfo("doubao: 登录后探测账号失败：" + errLaunch.Error())
		return "", "", ""
	}
	return launch.Nickname, launch.SecUserID, launch.UID
}

// maskPhone 只保留手机号前三位与后四位，用于面板展示与日志。
func maskPhone(phone string) string {
	digits := strings.TrimPrefix(strings.TrimSpace(phone), "+86")
	digits = strings.TrimPrefix(digits, "86")
	if len(digits) < 8 {
		return digits
	}
	return digits[:3] + "****" + digits[len(digits)-4:]
}
