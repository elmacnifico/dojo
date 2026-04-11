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

// JSONSubsetMatch returns true when every field in expected exists in actual
// with a matching value. Extra fields in actual are ignored at every nesting
// level. For non-JSON payloads it falls back to whitespace-normalized contains.
func JSONSubsetMatch(actual, expected []byte) bool {
	actual = bytes.TrimSpace(actual)
	expected = bytes.TrimSpace(expected)
	if len(expected) == 0 {
		return true
	}

	var av, ev any
	aOK := json.Unmarshal(actual, &av) == nil && json.Valid(actual)
	eOK := json.Unmarshal(expected, &ev) == nil && json.Valid(expected)

	if aOK && eOK {
		return jsonValueContains(av, ev)
	}
	// Non-JSON fallback: whitespace-collapsed contains.
	na := strings.Join(strings.Fields(string(actual)), " ")
	ne := strings.Join(strings.Fields(string(expected)), " ")
	return strings.Contains(na, ne)
}

// jsonValueContains checks that every field in expected is present and matching
// in actual. Objects allow extra keys in actual; arrays compare index-by-index
// up to len(expected).
func jsonValueContains(actual, expected any) bool {
	switch ev := expected.(type) {
	case map[string]any:
		am, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for k, evv := range ev {
			avv, exists := am[k]
			if !exists || !jsonValueContains(avv, evv) {
				return false
			}
		}
		return true
	case []any:
		aa, ok := actual.([]any)
		if !ok {
			return false
		}
		if len(aa) < len(ev) {
			return false
		}
		for i, evv := range ev {
			if !jsonValueContains(aa[i], evv) {
				return false
			}
		}
		return true
	default:
		if evStr, ok := expected.(string); ok {
			if avStr, ok := actual.(string); ok {
				if strings.HasPrefix(evStr, "*") && strings.HasSuffix(evStr, "*") && len(evStr) >= 2 {
					return strings.Contains(avStr, evStr[1:len(evStr)-1])
				}
				return avStr == evStr
			}
		}
		return actual == expected
	}
}

// SplitEnvelope detects whether data is an envelope fixture containing both
// "headers" (object) and "body" keys. If so it returns the extracted body bytes
// and raw headers JSON separately. Non-envelope data is returned unchanged.
func SplitEnvelope(data []byte) (body []byte, headers []byte, isEnvelope bool) {
	var envelope struct {
		Headers json.RawMessage `json:"headers"`
		Body    json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return data, nil, false
	}
	if envelope.Headers == nil || envelope.Body == nil {
		return data, nil, false
	}
	var hm map[string]any
	if json.Unmarshal(envelope.Headers, &hm) != nil {
		return data, nil, false
	}
	// body: string → raw bytes; object/array → keep as JSON
	var bodyStr string
	if json.Unmarshal(envelope.Body, &bodyStr) == nil {
		return []byte(bodyStr), envelope.Headers, true
	}
	return envelope.Body, envelope.Headers, true
}
