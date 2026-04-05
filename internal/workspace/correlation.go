package workspace

import (
	"bytes"
	"fmt"
	"regexp"

	"github.com/tidwall/gjson"
)

// ExtractCorrelation uses the CorrelationConfig to extract an ID from a raw payload.
func ExtractCorrelation(cfg *CorrelationConfig, payload []byte) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("missing correlation config")
	}

	// If the test explicitly overrode the correlation with a static value
	if cfg.Value != "" {
		if !bytes.Contains(payload, []byte(cfg.Value)) {
			return "", fmt.Errorf("payload does not contain literal value '%s'", cfg.Value)
		}
		return cfg.Value, nil
	}

	switch cfg.Type {
	case "jsonpath":
		if cfg.Target == "" {
			return "", fmt.Errorf("jsonpath correlation missing target")
		}
		if !gjson.ValidBytes(payload) {
			return "", fmt.Errorf("payload is not valid JSON")
		}
		res := gjson.GetBytes(payload, cfg.Target)
		if !res.Exists() {
			return "", fmt.Errorf("jsonpath '%s' not found in payload", cfg.Target)
		}
		extracted := res.String()
		if cfg.Regex != "" {
			re, err := regexp.Compile(cfg.Regex)
			if err != nil {
				return "", fmt.Errorf("invalid regex '%s': %w", cfg.Regex, err)
			}
			matches := re.FindStringSubmatch(extracted)
			if len(matches) > 1 {
				return matches[1], nil
			} else if len(matches) == 1 {
				return matches[0], nil
			}
			return "", fmt.Errorf("regex '%s' did not match extracted jsonpath string", cfg.Regex)
		}
		return extracted, nil

	case "regex":
		if cfg.Target == "" {
			return "", fmt.Errorf("regex correlation missing target")
		}
		re, err := regexp.Compile(cfg.Target)
		if err != nil {
			return "", fmt.Errorf("invalid regex '%s': %w", cfg.Target, err)
		}
		matches := re.FindSubmatch(payload)
		if len(matches) > 1 {
			// Submatch found (capture group 1)
			return string(matches[1]), nil
		} else if len(matches) == 1 {
			// Full match
			return string(matches[0]), nil
		}
		return "", fmt.Errorf("regex '%s' did not match payload", cfg.Target)

	default:
		return "", fmt.Errorf("unknown correlation type: %s", cfg.Type)
	}
}

// FindCorrelationByValue scans a payload for a known correlation ID string.
// This is useful if the proxy knows the ID and just wants to confirm if it exists.
func FindCorrelationByValue(payload []byte, id string) bool {
	return bytes.Contains(payload, []byte(id))
}
