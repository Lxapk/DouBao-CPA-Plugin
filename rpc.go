package main

import (
	"encoding/json"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Plugin identity.
//
// pluginName is not a label: CPA derives the management routes
// (/v0/management/<pluginName>) and the resource routes
// (/v0/resource/plugins/<pluginName>) from it, and the plugin uses it to locate
// its data directory. It also becomes the .so file name, since CPA takes the
// plugin id from the file. Changing it breaks every stored route and orphans the
// saved configuration, so it is deliberately lower-case and free of the display
// spelling.
const (
	pluginName        = "doubao"
	pluginDisplayName = "豆包 / Dola"

	// pluginVersion is the version reported to the host and shown in the panel.
	//
	// It must match registry.json and the release tag. Keeping it in sync was
	// previously manual and it silently drifted: the published releases reached
	// 0.2.2 while this constant still said 0.1.0, so the host logged every build
	// as 0.1.0 and the panel showed a version that did not exist. The release
	// script now rewrites this line before building, and a test pins it to
	// registry.json so a mismatch fails the suite rather than shipping.
	pluginVersion = "0.2.4"

	pluginAuthor = "Lxapk"
	pluginRepo   = "https://github.com/Lxapk/doubao-cpa-plugin"
)

// registration mirrors pluginhost.rpcRegistration.
type registration struct {
	SchemaVersion uint32             `json:"schema_version"`
	Metadata      pluginapi.Metadata `json:"metadata"`
	Capabilities  registrationCaps   `json:"capabilities"`
}

// registrationCaps mirrors pluginhost.rpcCapabilities.
//
// Only the fields this plugin sets are declared; the rest default to
// false/empty. The JSON tags are snake_case here even though pluginapi.Metadata
// uses PascalCase — the host decodes these from the capability object, and
// getting the casing wrong silently disables the capability.
type registrationCaps struct {
	// AuthProvider lets the account page run this plugin's credential parser.
	AuthProvider bool `json:"auth_provider"`

	// Both model routes are declared. model.register lets the host pull the
	// catalogue once at startup, model.static answers on demand, and
	// model.for_auth serves the per-credential view the auth page needs.
	ModelRegistrar bool `json:"model_registrar"`
	ModelProvider  bool `json:"model_provider"`

	// Execution: this is what makes /v1/chat/completions route here.
	Executor              bool                         `json:"executor"`
	ExecutorModelScope    pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats  []string                     `json:"executor_input_formats"`
	ExecutorOutputFormats []string                     `json:"executor_output_formats"`

	// ManagementAPI serves the plugin's own panel under /v0/management/<name>.
	ManagementAPI bool `json:"management_api"`
}

// managementRegistrationResponse mirrors pluginhost.rpcManagementRegistrationResponse.
type managementRegistrationResponse struct {
	Routes    []pluginapi.ManagementRoute `json:"routes,omitempty"`
	Resources []pluginapi.ResourceRoute   `json:"resources,omitempty"`
}

// handleMethod dispatches one CPA RPC call.
func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {

	// ---- lifecycle ----------------------------------------------------
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		var req lifecycleRequest
		if len(request) > 0 {
			if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
				return nil, errUnmarshal
			}
		}
		if errDecode := state.settings.decodeLifecycleConfig(req.ConfigYAML); errDecode != nil {
			return nil, errDecode
		}
		return okEnvelope(buildRegistration())

	case pluginabi.MethodPluginQuiesce:
		// Nothing to drain: CPA owns the request lifecycle.
		return okEnvelope(map[string]any{})

	case pluginabi.MethodPluginShutdown:
		shutdownPlugin()
		return okEnvelope(map[string]any{})

	// ---- credentials --------------------------------------------------
	case pluginabi.MethodAuthIdentifier:
		return authIdentifier()

	case pluginabi.MethodAuthParse:
		return authParse(request)

	case pluginabi.MethodAuthLoginStart:
		return authLoginStart(request)

	case pluginabi.MethodAuthLoginPoll:
		return authLoginPoll(request)

	case pluginabi.MethodAuthRefresh:
		return authRefresh(request)

	// ---- model catalogue ----------------------------------------------
	//
	// model.register and model.static answer with the same catalogue through two
	// host-driven paths. They share an implementation because the catalogue is
	// realm-independent: doubao and dola expose the same model names.
	case pluginabi.MethodModelRegister:
		return modelStatic(request)

	case pluginabi.MethodModelStatic:
		return modelStatic(request)

	case pluginabi.MethodModelForAuth:
		return modelForAuth(request)

	// ---- execution -----------------------------------------------------
	case pluginabi.MethodExecutorIdentifier:
		return executorIdentifier()

	case pluginabi.MethodExecutorExecute:
		return executorExecute(request)

	case pluginabi.MethodExecutorExecuteStream:
		return executorExecuteStream(request)

	case pluginabi.MethodExecutorCountTokens:
		return executorCountTokens(request)

	// ---- management panel ----------------------------------------------
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegistration())

	case pluginabi.MethodManagementHandle:
		return handleManagement(request)

	default:
		return errorEnvelope("unknown_method", "unknown method: "+method, http.StatusNotImplemented), nil
	}
}

// buildRegistration answers plugin.register / plugin.reconfigure.
func buildRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             pluginDisplayName,
			Version:          pluginVersion,
			Author:           pluginAuthor,
			GitHubRepository: pluginRepo,
			ConfigFields: []pluginapi.ConfigField{
				{
					Name:        "realm_default",
					Type:        pluginapi.ConfigFieldTypeEnum,
					EnumValues:  []string{"doubao", "dola"},
					Description: "默认上游：doubao=国内版豆包（www.doubao.com），dola=国际版 Dola（www.dola.com）。未在凭据中指定时使用。",
				},
				{
					Name:        "expose_models",
					Type:        pluginapi.ConfigFieldTypeBoolean,
					Description: "是否在 /v1/models 中同时暴露豆包与 Dola 的模型。关闭时只暴露 default_realm 的模型。",
				},
				{
					Name:        "request_timeout_seconds",
					Type:        pluginapi.ConfigFieldTypeInteger,
					Description: "单次上游请求的超时秒数。",
				},
				{
					Name:        "reply_poll_seconds",
					Type:        pluginapi.ConfigFieldTypeInteger,
					Description: "等待模型回复的最长秒数（轮询兜底路径）。",
				},
				{
					Name:        "reply_poll_interval_ms",
					Type:        pluginapi.ConfigFieldTypeInteger,
					Description: "轮询回复的间隔毫秒数。",
				},
				{
					Name:        "debug",
					Type:        pluginapi.ConfigFieldTypeBoolean,
					Description: "输出详细日志（含上游请求/响应摘要）。",
				},
			},
		},
		Capabilities: registrationCaps{
			AuthProvider:   true,
			ModelRegistrar: true,
			ModelProvider:  true,
			Executor:       true,
			// The credentials are auth-bound, so the host may bind models to a
			// specific credential as well as serving them statically.
			ExecutorModelScope:    pluginapi.ExecutorModelScopeBoth,
			ExecutorInputFormats:  []string{"chat-completions"},
			ExecutorOutputFormats: []string{"chat-completions"},
			ManagementAPI:         true,
		},
	}
}
