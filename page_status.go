package main

import (
	"encoding/json"
	"strings"
)

// Status document and small rendering helpers shared by the pages.

// statusSnapshot is the plugin status document served at /status.
type statusSnapshot struct {
	Plugin       string           `json:"plugin"`
	Version      string           `json:"version"`
	DefaultRealm string           `json:"default_realm"`
	Realms       []realmStatus    `json:"realms"`
	Models       []map[string]any `json:"models"`
	Settings     map[string]any   `json:"settings"`
}

type realmStatus struct {
	Realm       string `json:"realm"`
	DisplayName string `json:"display_name"`
	Host        string `json:"host"`
	AID         string `json:"aid"`
	Region      string `json:"region"`
	BotID       string `json:"bot_id"`
	ModelCount  int    `json:"model_count"`
}

// statusJSON renders the status document.
//
// It is the machine-readable counterpart of the panel: automation and the browser
// both read it, so the shape is stable and the model list is not truncated.
func statusJSON() []byte {
	settings := state.settings.get()
	out := statusSnapshot{
		Plugin:       pluginName,
		Version:      pluginVersion,
		DefaultRealm: string(settings.RealmDefault),
		Settings:     settingsJSON(settings),
		Models:       modelDisplayList(),
	}
	for _, r := range allRealms {
		p := profileFor(r)
		out.Realms = append(out.Realms, realmStatus{
			Realm:       string(r),
			DisplayName: p.DisplayName,
			Host:        p.Host,
			AID:         p.AID,
			Region:      p.Region,
			BotID:       defaultBotIDFor(r),
			ModelCount:  len(catalogueFor(r)),
		})
	}
	raw, _ := json.MarshalIndent(out, "", "  ")
	return raw
}

// htmlEscape escapes the characters that matter inside element content and inside
// a double-quoted attribute.
//
// Every value interpolated into the page passes through here: model ids and
// descriptions come from configuration, so they are not trusted markup.
func htmlEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
