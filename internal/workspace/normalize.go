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

// NormalizeSQL collapses whitespace and strips a trailing semicolon for stable
// comparison. Whitespace inside SQL string literals ('...'), quoted identifiers
// ("..."), and dollar-quoted bodies ($tag$...$tag$) is preserved verbatim so
// literals like 'foo  bar' can never collapse-match 'foo bar'.
func NormalizeSQL(s string) string {
	s = strings.TrimSpace(s)
	// Strip one trailing semicolon, tolerating surrounding whitespace.
	if strings.HasSuffix(s, ";") {
		s = strings.TrimSuffix(s, ";")
	}
	s = strings.TrimSpace(s)

	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	i := 0
	for i < len(s) {
		c := s[i]

		// String literal or quoted identifier: copy verbatim until the
		// closing quote, honoring doubled quotes ('' or "") as escapes.
		if c == '\'' || c == '"' {
			quote := c
			b.WriteByte(c)
			i++
			for i < len(s) {
				if s[i] == quote {
					// Doubled quote is an escaped literal quote.
					if i+1 < len(s) && s[i+1] == quote {
						b.WriteByte(quote)
						b.WriteByte(quote)
						i += 2
						continue
					}
					b.WriteByte(quote)
					i++
					break
				}
				b.WriteByte(s[i])
				i++
			}
			inSpace = false
			continue
		}

		// Dollar-quoted literal: $tag$ ... $tag$, tag may be empty ($$...$$).
		if c == '$' {
			if tag, ok := dollarQuoteTag(s, i); ok {
				b.WriteString(s[i : i+len(tag)+2])
				i += len(tag) + 2
				closing := "$" + tag + "$"
				end := strings.Index(s[i:], closing)
				if end < 0 {
					// Unterminated: copy the rest verbatim.
					b.WriteString(s[i:])
					i = len(s)
				} else {
					b.WriteString(s[i : i+end+len(closing)])
					i += end + len(closing)
				}
				inSpace = false
				continue
			}
		}

		// Whitespace outside literals collapses to a single space.
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			i++
			continue
		}

		b.WriteByte(c)
		inSpace = false
		i++
	}
	return strings.TrimRight(b.String(), " ")
}

// dollarQuoteTag returns the tag of a dollar-quote opener starting at s[i]
// when s[i:] begins with $tag$. ok is false when there is no valid opener.
func dollarQuoteTag(s string, i int) (tag string, ok bool) {
	// s[i] == '$'; find the closing '$'. A valid tag contains only letters,
	// digits, and underscores (possibly empty).
	j := i + 1
	for j < len(s) && (isTagByte(s[j])) {
		j++
	}
	if j < len(s) && s[j] == '$' {
		return s[i+1 : j], true
	}
	return "", false
}

func isTagByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
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
