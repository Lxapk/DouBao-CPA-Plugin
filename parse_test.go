package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// TestAuthParseProducesStorage pins the contract CPA's account page depends on.
//
// CPA calls auth.parse with the stored file and expects AuthData back. The credential
// has to travel in StorageJSON — AuthData has no Tokens field, and a parse that returns
// an empty StorageJSON makes CPA treat the file as holding nothing, which the account
// page renders as a blank entry.
func TestAuthParseProducesStorage(t *testing.T) {
	stored := map[string]any{
		"type":        pluginName,
		"realm":       string(realmDoubao),
		"cookies":     "sessionid=abc; flow_cur_user_sec_id=xyz",
		"sec_user_id": "MS4wLjABAAAA_test",
		"bot_id":      "7338286299411103781",
		"device_id":   "1",
		"web_id":      "2",
		"web_tab_id":  "3",
	}
	raw, _ := json.Marshal(stored)

	req, _ := json.Marshal(pluginapi.AuthParseRequest{Provider: pluginName, RawJSON: raw})
	resp, err := authParse(req)
	if err != nil {
		t.Fatal(err)
	}

	var env struct {
		Result struct {
			Handled bool               `json:"Handled"`
			Auth    pluginapi.AuthData `json:"Auth"`
			Error   string             `json:"Error"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatal(err)
	}
	if env.Result.Error != "" {
		t.Fatalf("解析报错: %s", env.Result.Error)
	}
	if !env.Result.Handled {
		t.Fatal("未认领该凭据，CPA 会当作异物")
	}
	if len(env.Result.Auth.StorageJSON) == 0 {
		t.Fatal("StorageJSON 为空 —— CPA 会认为文件里没有凭证")
	}

	var back map[string]any
	if err := json.Unmarshal(env.Result.Auth.StorageJSON, &back); err != nil {
		t.Fatalf("StorageJSON 不是合法 JSON: %v", err)
	}
	if back["cookies"] == nil || back["cookies"] == "" {
		t.Error("StorageJSON 里没有 cookies")
	}
	if env.Result.Auth.Provider != pluginName {
		t.Errorf("Provider = %q, want %q", env.Result.Auth.Provider, pluginName)
	}
	if env.Result.Auth.ID == "" {
		t.Error("ID 为空，宿主无法稳定索引该凭据")
	}
}

// TestAuthDirTrustGate pins the guard against writing into a guessed directory.
//
// The fallback path never fails on its own: it produces a valid credential file that
// CPA does not read, so the user sees "authorised" and an account that never appears.
// Refusing the write turns that into a message.
func TestAuthDirTrustGate(t *testing.T) {
	// A directory derived from the host is trusted; one derived from cwd is not.
	hostConfig.mu.Lock()
	prev := hostConfig.dir
	hostConfig.dir = ""
	hostConfig.mu.Unlock()
	defer func() {
		hostConfig.mu.Lock()
		hostConfig.dir = prev
		hostConfig.mu.Unlock()
	}()

	if authDirIsTrusted() {
		t.Error("缓存为空时不应被视为可信")
	}
	rememberAuthDir("/srv/cpa/auths")
	if !authDirIsTrusted() {
		t.Error("宿主下发的目录应被视为可信")
	}
	if got := authDirFromHost(); got != "/srv/cpa/auths" {
		t.Errorf("authDirFromHost = %q, want /srv/cpa/auths", got)
	}
}
