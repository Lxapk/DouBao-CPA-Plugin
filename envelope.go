package main

import (
	"encoding/json"
	"fmt"
)

// envelope is the CPA RPC envelope:
//
//	{"ok":true,"result":{...}}  |  {"ok":false,"error":{"code","message","http_status"}}
type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

// errorEnvelope builds a failure envelope. httpStatus defaults to 500 the same
// way CPA's pluginabi.NewErrorEnvelope treats a zero status.
func errorEnvelope(code, message string, httpStatus int) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
	}})
	return raw
}

// okEnvelope wraps a value in the CPA success envelope.
func okEnvelope(v any) ([]byte, error) {
	raw, errMarshal := json.Marshal(v)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(envelope{OK: true, Result: json.RawMessage(raw)})
}

// identifierResponse answers every *.identifier method.
type identifierResponse struct {
	Identifier string `json:"identifier"`
}

// guardRPC runs fn, converting a panic into an ordinary error.
//
// CPA recovers plugin panics, but it also marks the plugin fused when one
// escapes, which silently disables every later capability. Containing the panic
// here keeps a single bad request from taking the whole plugin down.
func guardRPC(method string, fn func() ([]byte, error)) (raw []byte, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic in %s: %v", method, rec)
			raw = nil
		}
	}()
	return fn()
}
