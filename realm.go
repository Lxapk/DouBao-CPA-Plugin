package main

import "strings"

// realm identifies which upstream an account belongs to.
//
// Doubao (国内版) and Dola (国际版) run the same IM protocol. They differ in
// exactly four things, so the difference is data rather than code:
//
//	                     doubao                dola
//	host                 www.doubao.com        www.dola.com
//	aid / real_aid       497858                495671
//	region / sys_region  CN                    JP
//	cookie tld           .doubao.com           .dola.com
//
// The query string, the envelope, the command ids and the content types are
// byte-for-byte identical, which is what lets one executor serve both.
type realm string

const (
	realmDoubao realm = "doubao"
	realmDola   realm = "dola"
)

// realmProfile is the per-realm data table.
type realmProfile struct {
	// ID is the realm key, also the auth provider key prefix.
	ID realm
	// DisplayName is what a person reads in the panel.
	DisplayName string
	// Host is the web origin that serves the IM endpoints.
	Host string
	// AID and RealAID identify the client application to the gateway.
	AID string
	// Region and SysRegion are echoed back in the query string.
	Region    string
	SysRegion string
	// CookieDomain is the suffix that scopes the session cookies.
	CookieDomain string
	// PCCVersion is the desktop client version the web build reports.
	PCCVersion string
	// Locale is the web UI language.
	Locale string
	// PassportHost is the account centre that serves the passport endpoints.
	//
	// 豆包把它放在 accounts.doubao.com，Dola 没有独立账号中心，passport 就挂在
	// 站点本身。写死成一个域名会让另一侧的上游永远登录不了。
	PassportHost string
	// PassportAID is the aid the passport endpoints expect.
	//
	// 它和 IM 那套的 AID 不是一回事：短信模板、登录后的站点归属都看这个值，
	// 用错会得到「应用无权限」或登录到另一侧。
	PassportAID string
}

const (
	doubaoPCVersion   = "3.39.2"
	doubaoVersionCode = "20800"
)

var realmProfiles = map[realm]realmProfile{
	realmDoubao: {
		ID:           realmDoubao,
		DisplayName:  "豆包",
		Host:         "https://www.doubao.com",
		AID:          "497858",
		Region:       "CN",
		SysRegion:    "CN",
		CookieDomain: ".doubao.com",
		PCCVersion:   doubaoPCVersion,
		Locale:       "zh",
		PassportHost: "https://accounts.doubao.com",
		PassportAID:  "497858",
	},
	realmDola: {
		ID:           realmDola,
		DisplayName:  "Dola",
		Host:         "https://www.dola.com",
		AID:          "495671",
		Region:       "JP",
		SysRegion:    "JP",
		CookieDomain: ".dola.com",
		PCCVersion:   doubaoPCVersion,
		Locale:       "zh",
		// Dola 没有独立账号中心，passport 挂在站点本身。
		PassportHost: "https://www.dola.com",
		PassportAID:  "495671",
	},
}

// allRealms is the stable iteration order used whenever every realm is walked.
// Doubao leads because it is the one most users have an account for.
var allRealms = []realm{realmDoubao, realmDola}

// profileFor returns the profile for a realm, falling back to doubao.
func profileFor(r realm) realmProfile {
	if p, ok := realmProfiles[r]; ok {
		return p
	}
	return realmProfiles[realmDoubao]
}

// normalizeRealm maps the many spellings a user or config might use onto a
// canonical realm. Empty input yields the empty realm, which callers treat as
// "not specified" rather than "doubao".
func normalizeRealm(s string) realm {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "doubao", "豆包", "cn", "china", "mainland", "中国":
		return realmDoubao
	case "dola", "国际", "intl", "international", "oversea", "overseas", "global":
		return realmDola
	}
	return ""
}

// realmFromHost infers the realm from a host or URL. It returns the empty realm
// when the host matches neither upstream.
func realmFromHost(host string) realm {
	h := strings.ToLower(host)
	switch {
	case strings.Contains(h, "doubao.com"):
		return realmDoubao
	case strings.Contains(h, "dola.com"):
		return realmDola
	}
	return ""
}
