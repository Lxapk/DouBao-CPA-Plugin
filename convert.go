package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Small coercion helpers.
//
// YAML decodes into any, and the concrete types differ between a value written
// as a bare scalar and one quoted in the config. These keep the callers free of
// type switches.

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

func toBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "yes", "on", "1", "是", "开":
			return true, true
		case "false", "no", "off", "0", "否", "关":
			return false, true
		}
	case int:
		return t != 0, true
	case int64:
		return t != 0, true
	case float64:
		return t != 0, true
	}
	return false, false
}

func toInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case string:
		if n, errAtoi := strconv.Atoi(strings.TrimSpace(t)); errAtoi == nil {
			return n, true
		}
	}
	return 0, false
}
