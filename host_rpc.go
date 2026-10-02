package main

import (
	"encoding/json"
	"errors"
)

// errHostUnavailable reports that the plugin has no host API to call. It only
// happens when a method is exercised outside CPA (unit tests, a standalone
// build), never in production.
var errHostUnavailable = errors.New("host API 不可用")

// errHostRPC carries a structured failure returned by the host.
type errHostRPC struct {
	Code    string
	Message string
}

func (e errHostRPC) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// hostCallFunc is the indirection that lets the package build with
// CGO_ENABLED=0. cabi.go swaps in the cgo implementation during
// cliproxy_plugin_init; this default keeps unit tests host-free.
var hostCallFunc = func(string, any) (json.RawMessage, error) {
	return nil, errHostUnavailable
}

// callHost performs one host RPC and returns the unwrapped result.
func callHost(method string, payload any) (json.RawMessage, error) {
	return hostCallFunc(method, payload)
}

// callHostInto performs a host RPC and decodes the result into out.
func callHostInto(method string, payload any, out any) error {
	raw, errCall := callHost(method, payload)
	if errCall != nil {
		return errCall
	}
	if len(raw) == 0 || out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
