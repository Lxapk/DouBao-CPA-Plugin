package main

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// 上游同一字段的类型并不统一：豆包把尺寸写成字符串（"384"），Dola 写成数字。
// 直接声明成 int 会让 json.Unmarshal 整个失败，调用方只看到「解析出 0 个资产」，
// 东西其实都在。这两个类型把两种写法都收下。

// flexibleInt 接受数字、数字字符串与空串。
type flexibleInt int

func (f *flexibleInt) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		*f = 0
		return nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*f = 0
			return nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			*f = 0
			return nil
		}
		*f = flexibleInt(v)
		return nil
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		*f = 0
		return nil
	}
	*f = flexibleInt(v)
	return nil
}

// flexibleFloat 同上，用于时长（豆包可能给 10.08，也可能是 "10.08"）。
type flexibleFloat float64

func (f *flexibleFloat) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		*f = 0
		return nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*f = 0
			return nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			*f = 0
			return nil
		}
		*f = flexibleFloat(v)
		return nil
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		*f = 0
		return nil
	}
	*f = flexibleFloat(v)
	return nil
}
