package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// This file publishes the model catalogue.
//
// The upstream reports 67 model entries, but they are internal code names
// ("Aoe", "ances s2", "func: ogt（4k）") rather than product names, and the
// mapping between them and what the UI shows is not published. Rather than
// invent a mapping, the catalogue is declared explicitly here: a small set of
// stable, documented aliases that route to the assistant, plus a pass-through
// rule that accepts any upstream model id so a user who knows the internal
// name can still call it.
//
// The aliases are realm-prefixed ("doubao/…", "dola/…"). Both upstreams expose
// the same names, so the prefix is what keeps them apart and lets a request
// choose a side. An unprefixed name resolves to the configured default realm.

// modelAlias is one advertised model.
type modelAlias struct {
	// ID is the client-facing name, including the realm prefix.
	ID string
	// DisplayName is the human-readable label for /v1/models.
	DisplayName string
	// Created is the advertised creation timestamp.
	Created int64
	// Description explains what the alias does.
	Description string
	// UpstreamModel optionally pins the request to an upstream model id.
	// Empty means "let the upstream use the account's default assistant".
	UpstreamModel string
	// Skill is the upstream capability to activate, or 0 for plain chat.
	Skill int
}

// skillForModel returns the upstream skill a model name activates.
func skillForModel(requested string) int {
	name, _ := stripModelPrefix(normalizeModelName(requested))
	for _, alias := range capabilityAliases {
		if strings.EqualFold(name, alias.ID) {
			return alias.Skill
		}
	}
	return 0
}

// capabilityAliases are the stable, realm-independent model names.
//
// They describe behaviours, not vendor SKUs, because the vendor renames its
// internal models frequently and these names have to keep working across those
// changes.
var capabilityAliases = []modelAlias{
	{
		ID:            "default",
		DisplayName:   "默认助手",
		Description:   "账号默认的对话模型，等同于网页端直接提问。",
		UpstreamModel: "",
	},
	{
		ID:            "pro",
		DisplayName:   "深度思考",
		Description:   "开启深度思考的对话模型，回复更慢但推理更充分。",
		UpstreamModel: "",
	},
	{
		ID:            "image",
		DisplayName:   "图像生成",
		Description:   "生成图片。回复以 OpenAI 多模态 content 返回，图片地址在 image_url 中。",
		UpstreamModel: "",
		// Skill is the upstream capability to activate for this alias.
		Skill: skillImageGen,
	},
	{
		ID:            "video",
		DisplayName:   "视频生成",
		Description:   "生成视频。视频地址以文本与 media 扩展字段返回。",
		UpstreamModel: "",
		Skill:         skillVideoGen,
	},
}

// Upstream skill identifiers, from the client's skill table
// (/samantha/skill/recommend). They are what makes an image request actually
// produce a picture: sending "画一只猫" as plain text makes the model *describe*
// the picture it would draw rather than draw it.
const (
	skillImageGen = 3
	skillVideoGen = 5
)

// catalogModel is the wire shape for /v1/models.
type catalogModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	// Root/Description are extensions the CPA catalogue carries through when
	// present; clients that do not know them ignore them.
	Root        string `json:"root,omitempty"`
	Description string `json:"description,omitempty"`
}

// catalogueFor builds the model list for one realm.
func catalogueFor(r realm) []catalogModel {
	profile := profileFor(r)
	out := make([]catalogModel, 0, len(capabilityAliases))
	for _, alias := range capabilityAliases {
		out = append(out, catalogModel{
			ID:          modelPrefixFor(r) + alias.ID,
			Object:      "model",
			Created:     referenceCreated,
			OwnedBy:     string(r),
			Description: fmt.Sprintf("%s · %s", profile.DisplayName, alias.Description),
		})
	}
	return out
}

// modelStatic answers model.register and model.static.
//
// It returns the full catalogue, across both realms when both are exposed. CPA
// calls this at startup and on demand, so the answer must not depend on any
// credential being present — a plugin with no account yet still has to publish
// its models, or the user has nothing to select while authorising.
func modelStatic(request []byte) ([]byte, error) {
	var req pluginapi.StaticModelRequest
	if len(request) > 0 {
		_ = json.Unmarshal(request, &req)
	}
	// model.static arrives during registration, before any credential exists, and
	// carries the host's resolved auth directory. Capturing it here is what makes
	// the authorisation form able to write to the right place on first use — the
	// value is otherwise only sent during auth parsing, which a panel-driven
	// authorisation never reaches.
	rememberAuthDir(req.Host.AuthDir)

	models := buildCatalogue()
	return okEnvelope(map[string]any{"models": models})
}

// modelForAuth answers model.for_auth: the catalogue for one credential.
//
// CPA calls this once per credential and merges the results across credentials
// of the same provider. Because one plugin serves two realms under a single
// provider key, the per-auth view returns *both* realms' models: returning only
// the credential's own realm would leave the other realm's models absent from
// /v1/models whenever no account of that realm is authorised, and returning both
// is harmless — they resolve through the same executor.
func modelForAuth(request []byte) ([]byte, error) {
	var req pluginapi.AuthModelRequest
	if len(request) > 0 {
		_ = json.Unmarshal(request, &req)
	}
	// Same host context as model.static, but this one runs on every credential
	// reload, so it also repairs the cached value if the directory moved.
	rememberAuthDir(req.Host.AuthDir)

	models := buildCatalogue()
	return okEnvelope(map[string]any{"models": models})
}

// buildCatalogue assembles the advertised models.
func buildCatalogue() []catalogModel {
	settings := state.settings.get()
	realms := allRealms
	if !settings.exposesModels() {
		realms = []realm{settings.RealmDefault}
	}
	var out []catalogModel
	for _, r := range realms {
		out = append(out, catalogueFor(r)...)
	}
	// The bare aliases are advertised too, resolving to the default realm, so a
	// client that does not know about realms can just ask for "default".
	for _, alias := range capabilityAliases {
		out = append(out, catalogModel{
			ID:          alias.ID,
			Object:      "model",
			Created:     referenceCreated,
			OwnedBy:     string(settings.RealmDefault),
			Description: fmt.Sprintf("%s（默认上游：%s）", alias.Description, profileFor(settings.RealmDefault).DisplayName),
		})
	}
	return out
}

// realmForAuthRequest resolves the realm an auth-scoped model request refers to.
func realmForAuthRequest(request []byte) realm {
	if len(request) > 0 {
		var probe struct {
			Auth *pluginapi.AuthData `json:"auth"`
		}
		if errUnmarshal := json.Unmarshal(request, &probe); errUnmarshal == nil && probe.Auth != nil {
			if r := normalizeRealm(probe.Auth.Attributes["realm"]); r != "" {
				return r
			}
			if r := normalizeRealm(probe.Auth.Attributes["provider"]); r != "" {
				return r
			}
			if len(probe.Auth.StorageJSON) > 0 {
				if creds, errCreds := credentialsFromStorage(probe.Auth.StorageJSON); errCreds == nil {
					if r := normalizeRealm(string(creds.Realm)); r != "" {
						return r
					}
				}
			}
		}
	}
	if r := state.settings.get().RealmDefault; r != "" {
		return r
	}
	return realmDoubao
}

// referenceCreated is the timestamp reported for catalogue entries.
//
// It is a fixed value rather than time.Now(): the field feeds client caches and
// a changing value would make every poll look like a new release.
const referenceCreated = 1767225600 // 2026-01-01T00:00:00Z

// resolveUpstreamModel maps a client-facing model name to the realm and upstream
// model id a request should use.
//
// Resolution order:
//
//  1. an explicit realm prefix ("dola/default") picks that realm
//  2. a bare alias ("default") uses the default realm
//  3. anything else is passed through as an upstream model id, so an operator
//     who knows an internal name is not blocked by this list
func resolveUpstreamModel(requested string) (realm, string) {
	settings := state.settings.get()
	name, r := stripModelPrefix(strings.TrimSpace(requested))
	if r == "" {
		r = settings.RealmDefault
		if r == "" {
			r = realmDoubao
		}
	}
	bare := strings.TrimSuffix(name, ":latest")
	for _, alias := range capabilityAliases {
		if strings.EqualFold(bare, alias.ID) {
			return r, alias.UpstreamModel
		}
	}
	// Not an alias: hand the caller's name to the upstream unchanged. The
	// upstream ignores an unknown model id and answers with the account default,
	// which is the least surprising outcome.
	return r, bare
}
