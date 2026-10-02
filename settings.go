package main

import (
	"sync"

	"gopkg.in/yaml.v3"
)

// pluginSettings is the resolved configuration.
type pluginSettings struct {
	// RealmDefault is used when a credential does not name a realm.
	RealmDefault realm `yaml:"realm_default"`
	// ExposeModels controls whether both realms' models appear in /v1/models.
	ExposeModels *bool `yaml:"expose_models"`
	// RequestTimeoutSeconds bounds one upstream call.
	RequestTimeoutSeconds int `yaml:"request_timeout_seconds"`
	// ReplyPollSeconds bounds how long to wait for a reply on the fallback path.
	ReplyPollSeconds int `yaml:"reply_poll_seconds"`
	// MediaPollSeconds bounds how long to wait for an image or video. Generation
	// is far slower than text, so it gets its own budget.
	MediaPollSeconds int `yaml:"media_poll_seconds"`
	// ReplyPollIntervalMS is the poll cadence.
	ReplyPollIntervalMS int `yaml:"reply_poll_interval_ms"`
	// Debug turns on verbose logging.
	Debug bool `yaml:"debug"`
}

// defaultSettings returns the values used before any config arrives.
func defaultSettings() pluginSettings {
	return pluginSettings{
		RealmDefault:          realmDoubao,
		ExposeModels:          boolPtr(true),
		RequestTimeoutSeconds: 90,
		ReplyPollSeconds:      120,
		MediaPollSeconds:      600,
		ReplyPollIntervalMS:   700,
		Debug:                 false,
	}
}

func boolPtr(v bool) *bool { return &v }

// exposesModels reports whether both realms should be advertised.
func (s pluginSettings) exposesModels() bool {
	if s.ExposeModels == nil {
		return true
	}
	return *s.ExposeModels
}

// settingsHolder guards the live settings.
type settingsHolder struct {
	mu   sync.RWMutex
	when pluginSettings
}

// decodeLifecycleConfig parses the config_yaml CPA passes on register and
// reconfigure.
//
// The host hands over the full plugin config document, which nests this
// plugin's own settings under a top-level key named after the plugin. Anything
// that does not decode is ignored rather than fatal: a malformed optional field
// should not leave the plugin unable to register, because a plugin that does not
// register disappears from the UI entirely and the operator has no way to fix
// the config from inside the product.
func (h *settingsHolder) decodeLifecycleConfig(configYAML string) error {
	next := defaultSettings()

	if configYAML != "" {
		var root map[string]any
		if errUnmarshal := yaml.Unmarshal([]byte(configYAML), &root); errUnmarshal == nil && root != nil {
			// Prefer the plugin-named sub-document; fall back to the whole
			// document so a bare settings block also works.
			scope := root
			if nested, ok := root[pluginName].(map[string]any); ok {
				scope = nested
			}
			applyScope(scope, &next)
		}
	}

	if next.RealmDefault == "" {
		next.RealmDefault = realmDoubao
	}
	h.mu.Lock()
	h.when = next
	h.mu.Unlock()
	return nil
}

// applyScope copies recognised keys out of a decoded YAML map.
func applyScope(scope map[string]any, out *pluginSettings) {
	if scope == nil {
		return
	}
	if v, ok := scope["realm_default"]; ok {
		if r := normalizeRealm(toString(v)); r != "" {
			out.RealmDefault = r
		}
	}
	if v, ok := scope["expose_models"]; ok {
		if b, isBool := toBool(v); isBool {
			out.ExposeModels = &b
		}
	}
	if v, ok := scope["request_timeout_seconds"]; ok {
		if n, isNum := toInt(v); isNum && n > 0 {
			out.RequestTimeoutSeconds = n
		}
	}
	if v, ok := scope["reply_poll_seconds"]; ok {
		if n, isNum := toInt(v); isNum && n > 0 {
			out.ReplyPollSeconds = n
		}
	}
	if v, ok := scope["media_poll_seconds"]; ok {
		if n, isNum := toInt(v); isNum && n > 0 {
			out.MediaPollSeconds = n
		}
	}
	if v, ok := scope["reply_poll_interval_ms"]; ok {
		if n, isNum := toInt(v); isNum && n > 0 {
			out.ReplyPollIntervalMS = n
		}
	}
	if v, ok := scope["debug"]; ok {
		if b, isBool := toBool(v); isBool {
			out.Debug = b
		}
	}
}

func (h *settingsHolder) get() pluginSettings {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.when
}

// pluginState is the process-wide state.
type pluginState struct {
	settings settingsHolder
}

var state = func() *pluginState {
	s := &pluginState{}
	s.settings.when = defaultSettings()
	return s
}()

// lifecycleRequest mirrors pluginhost.rpcLifecycleRequest.
type lifecycleRequest struct {
	ConfigYAML string `json:"config_yaml"`
}

// shutdownPlugin is called on plugin.shutdown and process teardown.
func shutdownPlugin() {}
