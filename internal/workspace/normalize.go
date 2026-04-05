package workspace

import (
	"bytes"
	"encoding/json"
	"strings"
)

// NormalizePayloadForMatch returns a canonical string form of a request payload.
// for equality comparison. Postgres traffic uses SQL normalization; HTTP and
// other JSON bodies use canonical JSON when valid, otherwise whitespace-normalized raw text.
func NormalizePayloadForMatch(protocol string, payload []byte) string {
	if protocol == "postgres" {
		return NormalizeSQL(string(payload))
	}
	return NormalizeHTTPBody(payload)
}

// NormalizeSQL collapses whitespace and strips a trailing semicolon for stable comparison.
func NormalizeSQL(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ";")
	return strings.Join(strings.Fields(s), " ")
}

// NormalizeHTTPBody returns canonical JSON when the body is valid JSON; otherwise
// a single-space-normalized raw string.
func NormalizeHTTPBody(payload []byte) string {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return ""
	}
	if !json.Valid(payload) {
		return strings.Join(strings.Fields(string(payload)), " ")
	}
	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		return strings.Join(strings.Fields(string(payload)), " ")
	}
	v = canonicalizeJSONValue(v)
	out, err := json.Marshal(v)
	if err != nil {
		return strings.Join(strings.Fields(string(payload)), " ")
	}
	return string(out)
}

func canonicalizeJSONValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = canonicalizeJSONValue(val)
		}
		return x
	case []any:
		for i := range x {
			x[i] = canonicalizeJSONValue(x[i])
		}
		return x
	default:
		return v
	}
}
