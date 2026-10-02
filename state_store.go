package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Persisting panel-only settings.
//
// CPA owns config.yaml and exposes no host callback that rewrites it — the callback
// set is limited to HTTP, model execution, streaming, logging and the auth store.
// Settings changed from the panel therefore cannot be written back through the host,
// and are stored in the plugin's own file instead.
//
// The file is an *overlay*, not a snapshot: it records only the keys the panel has
// actually changed. Storing a full snapshot would mean that a value the operator
// writes by hand in config.yaml is silently reverted by whatever the panel happened
// to hold when it last saved — including fields the operator never touched there.
//
// Precedence on load: config.yaml first, then the overlay on top. So the panel wins
// for keys it has edited, and config.yaml supplies the rest, including any key the
// operator adds later.
//
// Path choice mirrors CPA's own use of os.UserConfigDir() so the file survives a
// restart; the working directory is the fallback for environments without a writable
// home (PRoot sandboxes, containers).

// panelState is the persisted overlay. Absent keys mean "not changed here".
type panelState struct {
	RealmDefault          string `json:"realm_default,omitempty"`
	ExposeModels          *bool  `json:"expose_models,omitempty"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds,omitempty"`
	ReplyPollSeconds      int    `json:"reply_poll_seconds,omitempty"`
	MediaPollSeconds      int    `json:"media_poll_seconds,omitempty"`
	ReplyPollIntervalMS   int    `json:"reply_poll_interval_ms,omitempty"`
	Debug                 *bool  `json:"debug,omitempty"`
}

// pluginStateDir returns the directory used for the state file.
//
// It is a variable so tests can redirect the file; production never reassigns it.
var pluginStateDir = func() string {
	if dir, errConfig := os.UserConfigDir(); errConfig == nil && dir != "" {
		return filepath.Join(dir, pluginName)
	}
	if wd, errWd := os.Getwd(); errWd == nil && wd != "" {
		return wd
	}
	return "."
}

// panelStatePath is the state file location.
func panelStatePath() string {
	return filepath.Join(pluginStateDir(), "settings.json")
}

// loadPanelState reads the persisted overlay.
//
// A missing or unreadable file yields an empty overlay rather than an error: the
// plugin must register and serve even when its state directory is not writable, and
// the config-derived values are already in place by then.
func loadPanelState() panelState {
	var out panelState
	raw, errRead := os.ReadFile(panelStatePath())
	if errRead != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

// overlayPanelState folds the persisted overlay into the settings.
//
// Applied after the config YAML, so a change made in the panel wins for the keys it
// touched while every other key keeps its configured value.
func overlayPanelState(s *pluginSettings) {
	p := loadPanelState()
	if r := normalizeRealm(p.RealmDefault); r != "" {
		s.RealmDefault = r
	}
	if p.ExposeModels != nil {
		s.ExposeModels = p.ExposeModels
	}
	if p.Debug != nil {
		s.Debug = *p.Debug
	}
	if p.RequestTimeoutSeconds > 0 {
		s.RequestTimeoutSeconds = p.RequestTimeoutSeconds
	}
	if p.ReplyPollSeconds > 0 {
		s.ReplyPollSeconds = p.ReplyPollSeconds
	}
	if p.MediaPollSeconds > 0 {
		s.MediaPollSeconds = p.MediaPollSeconds
	}
	if p.ReplyPollIntervalMS > 0 {
		s.ReplyPollIntervalMS = p.ReplyPollIntervalMS
	}
}

// mergePanelState updates the overlay with just the keys a patch contained.
//
// The patch is applied key by key rather than by writing the whole settings object,
// because the object also holds values that came from config.yaml. Writing it whole
// would move those into the overlay and make the config file stop mattering.
func mergePanelState(patch map[string]json.RawMessage) error {
	current := loadPanelState()

	for key, raw := range patch {
		switch key {
		case "realm_default":
			var v string
			if errUnmarshal := json.Unmarshal(raw, &v); errUnmarshal == nil {
				current.RealmDefault = v
			}
		case "expose_models":
			var v bool
			if errUnmarshal := json.Unmarshal(raw, &v); errUnmarshal == nil {
				current.ExposeModels = boolPtr(v)
			}
		case "debug":
			var v bool
			if errUnmarshal := json.Unmarshal(raw, &v); errUnmarshal == nil {
				current.Debug = boolPtr(v)
			}
		case "request_timeout_seconds":
			if n, errNum := positiveInt(raw); errNum == nil {
				current.RequestTimeoutSeconds = n
			}
		case "reply_poll_seconds":
			if n, errNum := positiveInt(raw); errNum == nil {
				current.ReplyPollSeconds = n
			}
		case "media_poll_seconds":
			if n, errNum := positiveInt(raw); errNum == nil {
				current.MediaPollSeconds = n
			}
		case "reply_poll_interval_ms":
			if n, errNum := positiveInt(raw); errNum == nil {
				current.ReplyPollIntervalMS = n
			}
		default:
			// An unknown key is ignored: the page and the plugin can be deployed
			// out of step, and failing the whole patch would stop a newer page
			// from saving anything against an older plugin.
		}
	}
	return writePanelState(current)
}

// writePanelState serialises the overlay.
//
// The write goes through a temp file and a rename so a crash mid-write cannot leave
// a truncated file behind — a truncated file parses to an empty overlay and would
// silently discard the user's choices.
func writePanelState(state panelState) error {
	raw, errMarshal := json.MarshalIndent(state, "", "  ")
	if errMarshal != nil {
		return errMarshal
	}

	dir := pluginStateDir()
	if errMkdir := os.MkdirAll(dir, 0o755); errMkdir != nil {
		return errMkdir
	}

	path := panelStatePath()
	tmp := path + ".tmp"
	if errWrite := os.WriteFile(tmp, raw, 0o644); errWrite != nil {
		return errWrite
	}
	return os.Rename(tmp, path)
}
