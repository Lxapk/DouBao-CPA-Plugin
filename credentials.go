package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"net/url"
	"strings"
	"time"
)

// credentials is one upstream account.
//
// Both realms authenticate with an ordinary web session: a set of cookies that
// the browser holds after signing in. There is no OAuth token and no API key,
// and the session cannot be reconstructed from a single value — the gateway
// binds the session server-side and rejects partial cookie sets, which is why
// this struct stores the whole cookie jar rather than one chosen field.
//
// The load-bearing cookies, all under the realm's domain, are:
//
//	sessionid / sessionid_ss / sid_tt   the session id itself
//	sid_guard                           expiry + rotation metadata
//	uid_tt / uid_tt_ss                  the stable user id
//	odin_tt                             device token (anti-abuse binding)
//	flow_cur_user_sec_id                the account handle the IM API reads
//	d_ticket                            passport ticket
//	ttwid                               traffic fingerprint
//	passport_csrf_token                 CSRF for state-changing calls
//
// flow_cur_user_sec_id is the one the IM endpoints actually key on. A jar
// missing it looks authenticated to a human but fails every chat call, so it is
// treated as required.
type credentials struct {
	// Realm selects the upstream. Empty means doubao.
	Realm realm `json:"realm"`
	// UID is the numeric account id, when known.
	UID string `json:"uid,omitempty"`
	// SecUserID is the account handle returned by /alice/user/launch.
	SecUserID string `json:"sec_user_id,omitempty"`
	// Nickname is the display name, when known.
	Nickname string `json:"nickname,omitempty"`
	// Cookies is the raw Cookie header value. It is the primary secret.
	Cookies string `json:"cookies"`
	// DeviceID is the client device identifier echoed in every query string.
	DeviceID string `json:"device_id,omitempty"`
	// WebID / TeaUUID are the browser identity pair used by the web client.
	WebID string `json:"web_id,omitempty"`
	// WebTabID is a per-session tab identifier.
	WebTabID string `json:"web_tab_id,omitempty"`
	// BotID overrides the default assistant for this realm.
	BotID string `json:"bot_id,omitempty"`
	// ExpiresAt is when the session is believed to end.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	// SavedAt records when the credential was stored.
	SavedAt time.Time `json:"saved_at,omitempty"`
}

// profile returns the realm profile this credential belongs to.
func (c *credentials) profile() realmProfile {
	return profileFor(c.Realm)
}

// providerKey is the CPA auth provider key for this credential's realm.
func (c *credentials) providerKey() string {
	return string(c.profile().ID)
}

// cookieMap parses the raw Cookie header into name/value pairs.
func (c *credentials) cookieMap() map[string]string {
	out := make(map[string]string, 16)
	for _, part := range strings.Split(c.Cookies, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		out[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	return out
}

// sanitizedCookies returns the Cookie header value with whitespace normalised.
//
// A Cookie pasted from a browser or a clipboard often arrives wrapped across
// lines. Go's HTTP client rejects a header value containing a newline with
// "invalid header field value", so the value is collapsed to a single line
// before it is sent. Any character below 0x20 is replaced by a space and runs of
// whitespace are squeezed, which preserves the "; "-separated structure while
// removing the bytes that make the header illegal.
func (c *credentials) sanitizedCookies() string {
	if c.Cookies == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(c.Cookies))
	prevSpace := false
	for _, r := range c.Cookies {
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		if r == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	// Re-join the pairs so a value that wrapped mid-cookie still parses.
	return normalizeCookieSeparators(out)
}

// normalizeCookieSeparators rewrites separators into the canonical "; " form.
func normalizeCookieSeparators(raw string) string {
	parts := strings.Split(raw, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}

// cookie returns one cookie value, or "" when absent.
func (c *credentials) cookie(name string) string {
	return c.cookieMap()[name]
}

// validate reports whether the credential carries enough material to attempt a
// call. It checks shape only; whether the session is still alive is decided by
// the upstream.
func (c *credentials) validate() error {
	if strings.TrimSpace(c.Cookies) == "" {
		return errors.New("未提供 Cookie")
	}
	if c.Realm == "" {
		return errors.New("未指定上游（doubao 或 dola）")
	}
	if _, ok := realmProfiles[c.Realm]; !ok {
		return fmt.Errorf("未知上游 %q", c.Realm)
	}
	if c.cookie("flow_cur_user_sec_id") == "" {
		return errors.New("Cookie 缺少 flow_cur_user_sec_id，请确认复制的是登录后的完整 Cookie")
	}
	if !hasAnyCookie(c, "sessionid", "sessionid_ss", "sid_tt") {
		return errors.New("Cookie 缺少 sessionid，请确认复制的是登录后的完整 Cookie")
	}
	return nil
}

func hasAnyCookie(c *credentials, names ...string) bool {
	jar := c.cookieMap()
	for _, n := range names {
		if jar[n] != "" {
			return true
		}
	}
	return false
}

// withDefaults fills in the fields the web client normally generates, so a
// credential captured from a phone or a different browser still produces a
// well-formed query string.
func (c *credentials) withDefaults() *credentials {
	out := *c
	if out.DeviceID == "" {
		out.DeviceID = randomNumericID()
	}
	if out.WebID == "" {
		out.WebID = randomNumericID()
	}
	if out.WebTabID == "" {
		out.WebTabID = randomUUID()
	}
	if out.BotID == "" {
		out.BotID = defaultBotIDFor(out.Realm)
	}
	if out.ExpiresAt.IsZero() {
		out.ExpiresAt = sessionExpiryFromCookies(out.Cookies)
	}
	return &out
}

// sessionExpiryFromCookies reads the expiry out of sid_guard.
//
// sid_guard is shaped "<session>|<unix>|<max-age>|<http-date>" but arrives fully
// percent-encoded, so the separators appear as %7C and the date's comma and
// spaces as %2C and +. It is decoded first, then split; splitting the encoded
// form would find a single field.
func sessionExpiryFromCookies(raw string) time.Time {
	jar := (&credentials{Cookies: raw}).cookieMap()
	guard := jar["sid_guard"]
	if guard == "" {
		return time.Time{}
	}

	decoded, errUnescape := url.QueryUnescape(guard)
	if errUnescape != nil {
		decoded = guard
	}
	parts := strings.Split(decoded, "|")
	if len(parts) < 4 {
		return time.Time{}
	}
	dateStr := strings.TrimSpace(parts[3])
	// The date is "Sat, 31-Oct-2026 15:48:57 GMT" — day-month-year, which no
	// stdlib layout covers, so the layouts are tried in order.
	dateStr = strings.TrimPrefix(dateStr, "GMT")
	dateStr = strings.TrimSuffix(dateStr, "GMT")
	dateStr = strings.Trim(strings.TrimSpace(dateStr), ",")
	if idx := strings.Index(dateStr, ","); idx >= 0 {
		dateStr = strings.TrimSpace(dateStr[idx+1:])
	}
	for _, layout := range []string{
		"02-Jan-2006 15:04:05",
		"02-Jan-2006 15:04:05 GMT",
		"Mon, 02-Jan-2006 15:04:05",
	} {
		if t, err := time.Parse(layout, dateStr); err == nil {
			return t
		}
	}
	return time.Time{}
}

// label renders a short human-readable name for the account.
func (c *credentials) label() string {
	if c.Nickname != "" {
		return c.Nickname
	}
	if c.SecUserID != "" {
		return c.SecUserID
	}
	if c.UID != "" {
		return c.UID
	}
	return string(c.profile().ID)
}

// storageJSON is what the plugin persists inside the CPA auth record.
type storageJSON struct {
	Realm     realm     `json:"realm"`
	UID       string    `json:"uid,omitempty"`
	SecUserID string    `json:"sec_user_id,omitempty"`
	Nickname  string    `json:"nickname,omitempty"`
	Cookies   string    `json:"cookies"`
	DeviceID  string    `json:"device_id,omitempty"`
	WebID     string    `json:"web_id,omitempty"`
	WebTabID  string    `json:"web_tab_id,omitempty"`
	BotID     string    `json:"bot_id,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	SavedAt   time.Time `json:"saved_at,omitempty"`
}

func (c *credentials) toStorage() ([]byte, error) {
	return json.Marshal(storageJSON{
		Realm:     c.Realm,
		UID:       c.UID,
		SecUserID: c.SecUserID,
		Nickname:  c.Nickname,
		Cookies:   c.Cookies,
		DeviceID:  c.DeviceID,
		WebID:     c.WebID,
		WebTabID:  c.WebTabID,
		BotID:     c.BotID,
		ExpiresAt: c.ExpiresAt,
		SavedAt:   c.SavedAt,
	})
}

// credentialsFromStorage decodes a stored credential.
//
// Two shapes arrive here:
//
//  1. the JSON this plugin writes (storageJSON, all lower_snake_case)
//  2. the raw auth file CPA persists, which is the same document with a "type"
//     field added
//
// They overlap, so one struct reads both.
func credentialsFromStorage(raw []byte) (*credentials, error) {
	var s storageJSON
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &credentials{
		Realm:     s.Realm,
		UID:       s.UID,
		SecUserID: s.SecUserID,
		Nickname:  s.Nickname,
		Cookies:   s.Cookies,
		DeviceID:  s.DeviceID,
		WebID:     s.WebID,
		WebTabID:  s.WebTabID,
		BotID:     s.BotID,
		ExpiresAt: s.ExpiresAt,
		SavedAt:   s.SavedAt,
	}, nil
}
