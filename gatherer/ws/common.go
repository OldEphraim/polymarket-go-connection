package ws

import (
	"encoding/json"
	"strings"
)

func firstPresent(a, b json.RawMessage) json.RawMessage {
	if len(a) > 0 {
		return a
	}
	return b
}

func readString(m map[string]json.RawMessage, key string) string {
	if raw, ok := m[key]; ok && len(raw) > 0 {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func f64s(s string) float64 {
	f, _ := strconvParseFloat(strings.TrimSpace(s), 64)
	return f
}

// tiny indirection to allow reuse without importing strconv everywhere
func strconvParseFloat(s string, bitSize int) (float64, error) {
	return strconvParseFloatStd(s, bitSize)
}
