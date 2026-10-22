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
	// Kind is the OpenAI surface this alias answers on: "chat", "image" or
	// "video". It decides which endpoint may route to it.
	Kind string
	// ModelKey 是上游 model_item_key，用来在创作场景里指定具体模型。
	//
	// 模型清单本身由服务端下发（App 的图片/视频选择器就是读它），所以这里是
	// 按已知版本做的映射；键名与 APK 资源里的 larus_imageGen_model_menu_* /
	// larus_aiVideo_tag_seedance_* 一一对应。留空表示交给上游选默认模型。
	ModelKey string
	// DeepThink asks the upstream to reason before answering. It is a flag on the
	// request rather than a skill, so the two are modelled separately.
	DeepThink bool
}

// The OpenAI surfaces this plugin answers on.
//
// A model is bound to exactly one: CPA routes /v1/images/generations only to a
// model registered with Type "openai-image", and the chat endpoint only to a
// chat model. Registering the same alias everywhere would let a client ask the
// chat endpoint for a picture and get a text description instead.
const (
	kindChat  = "chat"
	kindImage = "image"
	kindVideo = "video"
)

// kindForModel returns the surface an alias belongs to, defaulting to chat.
func kindForModel(requested string) string {
	name, _ := stripModelPrefix(normalizeModelName(requested))
	for _, alias := range capabilityAliases {
		if strings.EqualFold(name, alias.ID) {
			return alias.Kind
		}
	}
	return kindChat
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

// deepThinkForModel reports whether a model name asks for deep thinking.
func deepThinkForModel(requested string) bool {
	name, _ := stripModelPrefix(normalizeModelName(requested))
	for _, alias := range capabilityAliases {
		if strings.EqualFold(name, alias.ID) {
			return alias.DeepThink
		}
	}
	return false
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
		Kind:          kindChat,
	},
	{
		ID:            "pro",
		DisplayName:   "深度思考",
		Description:   "开启深度思考的对话模型，回复更慢但推理更充分。",
		UpstreamModel: "",
		DeepThink:     true,
		Kind:          kindChat,
	},

	// ---- 图片：Seedream 系列（APK 资源 larus_imageGen_model_menu_*）----
	{
		ID:          "image",
		DisplayName: "图像生成（默认）",
		Description: "按账号默认的出图模型生成，等价于网页端直接出图。",
		Skill:       skillImageGen,
		Kind:        kindImage,
	},
	{
		ID:          "seedream-5.0-pro",
		DisplayName: "Seedream 5.0 Pro",
		Description: "专业出图，4 倍消耗。",
		Skill:       skillImageGen,
		Kind:        kindImage,
		ModelKey:    "seedream_5.0_pro",
	},
	{
		ID:          "seedream-5.0-flash",
		DisplayName: "Seedream 5.0 Flash",
		Description: "专业快速。",
		Skill:       skillImageGen,
		Kind:        kindImage,
		ModelKey:    "seedream_5.0",
	},
	{
		ID:          "seedream-4.5",
		DisplayName: "Seedream 4.5",
		Description: "日常生成。",
		Skill:       skillImageGen,
		Kind:        kindImage,
		ModelKey:    "seedream_4.5",
	},
	{
		ID:          "seedream-4.0",
		DisplayName: "Seedream 4.0",
		Description: "基础生图。",
		Skill:       skillImageGen,
		Kind:        kindImage,
		ModelKey:    "seedream_4.0",
	},

	// ---- 视频：Seedance 系列（APK 资源 larus_aiVideo_tag_seedance_*）----
	{
		ID:          "video",
		DisplayName: "视频生成（默认）",
		Description: "按账号默认的视频模型生成。",
		Skill:       skillVideoGen,
		Kind:        kindVideo,
	},
	{
		ID:          "seedance-2.5",
		DisplayName: "Seedance 2.5",
		Description: "旗舰视频标杆，5 倍消耗。",
		Skill:       skillVideoGen,
		Kind:        kindVideo,
		ModelKey:    "seedance_2.5",
	},
	{
		ID:          "seedance-2.0",
		DisplayName: "Seedance 2.0",
		Description: "进阶画面表现，2 倍消耗。",
		Skill:       skillVideoGen,
		Kind:        kindVideo,
		ModelKey:    "seedance_2.0",
	},
	{
		ID:          "seedance-2.0-fast",
		DisplayName: "Seedance 2.0 Fast",
		Description: "快速出片选择。",
		Skill:       skillVideoGen,
		Kind:        kindVideo,
		ModelKey:    "seedance_2.0_fast",
	},
	{
		ID:          "seedance-2.0-mini",
		DisplayName: "Seedance 2.0 Mini",
		Description: "日常生成使用。",
		Skill:       skillVideoGen,
		Kind:        kindVideo,
		ModelKey:    "seedance_2.0_mini",
	},
}

// modelKeyFor 返回一个别名对应的上游 model_item_key。
func modelKeyFor(requested string) string {
	name, _ := stripModelPrefix(normalizeModelName(requested))
	for _, alias := range capabilityAliases {
		if strings.EqualFold(name, alias.ID) {
			return alias.ModelKey
		}
	}
	return ""
}

// aliasForModel 按客户端模型名找到别名。
func aliasForModel(requested string) *modelAlias {
	name, _ := stripModelPrefix(normalizeModelName(requested))
	for i := range capabilityAliases {
		if strings.EqualFold(name, capabilityAliases[i].ID) {
			return &capabilityAliases[i]
		}
	}
	return nil
}

// Upstream skill identifiers, from the client's ChatAbilityType enum
// (com/larus/im/bean/message/ChatAbilityType, doubao 15.4.0). They are what makes
// an image request actually produce a picture: sending "画一只猫" as plain text
// makes the model *describe* the picture it would draw rather than draw it.
//
// The values come from the enum's own constructor arguments, not from its ordinal:
// SKILL_IMAGE_GENERATION is 2001 and SKILL_VIDEO_GENERATION is 2002, which is a
// different numbering from the pre-15.x client. An ordinal would be silently wrong
// here — it would name a different skill and produce text instead of media.
const (
	skillImageGen = 2001
	skillVideoGen = 2002
)

// registryTypeFor maps an alias to the model Type CPA stores in its registry.
//
// The image type is not cosmetic: OpenAI's /v1/images/generations accepts a model
// only when the registry reports Type "openai-image", so an alias registered
// without it is rejected by the endpoint before the executor is ever called.
func registryTypeFor(kind string) string {
	if kind == kindImage {
		return "openai-image"
	}
	return ""
}

// catalogModel is the wire shape for /v1/models.
type catalogModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	// Type tells CPA which surface may route to this model. "openai-image" is
	// what admits it to /v1/images/generations; empty means a chat model.
	Type string `json:"type,omitempty"`
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
			Type:        registryTypeFor(alias.Kind),
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
// 只返回该账号所属上游的模型，而且只返回带前缀的形式。
//
// CPA 会为每个账号调用一次并把结果并进 registry，所以这里返回什么，账号
// 名下就有什么。之前每个账号都返回「两个上游 + 裸名」的完整清单，结果是同
// 一批模型被注册了三遍：doubao/x、dola/x、x，列表里全是重复项；没登录 Dola
// 也会看到 dola/* 的模型，选中后必然失败。
//
// realm 优先从凭据里读，读不到才退回默认上游。
func modelForAuth(request []byte) ([]byte, error) {
	var req pluginapi.AuthModelRequest
	if len(request) > 0 {
		_ = json.Unmarshal(request, &req)
	}
	// Same host context as model.static, but this one runs on every credential
	// reload, so it also repairs the cached value if the directory moved.
	rememberAuthDir(req.Host.AuthDir)

	r := normalizeRealm(firstNonEmpty(
		req.Attributes["realm"],
		req.Attributes["provider"],
		realmFromStorageBlob(req.StorageJSON),
	))
	if r == "" {
		r = state.settings.get().RealmDefault
	}
	if r == "" {
		r = realmDoubao
	}

	return okEnvelope(map[string]any{"models": catalogueFor(r)})
}

// realmFromStorageBlob reads the realm out of a credential's storage JSON.
func realmFromStorageBlob(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var probe struct {
		Realm string `json:"realm"`
		Type  string `json:"type"`
	}
	if errUnmarshal := json.Unmarshal(raw, &probe); errUnmarshal != nil {
		return ""
	}
	if probe.Realm != "" {
		return probe.Realm
	}
	return probe.Type
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
	// 不再额外列出裸名（default / image / …）。
	//
	// 裸名仍然可用——请求时由 resolveUpstreamModel 按默认上游解析——但不再
	// 出现在模型列表里。否则同一个模型会有三个 ID（doubao/x、dola/x、x），
	// 客户端下拉框里就变成一长串重复项。
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
