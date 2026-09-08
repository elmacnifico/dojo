package engine

import (
	"os"
	"strings"
)

// expandMockEnv expands $VAR references in a mock response body.
// API_<NAME>_URL variables resolve against the engine's own proxy addresses
// (snapshotted in StartProxies) so concurrent engines in one process cannot
// corrupt each other's mock responses; every other variable falls back to the
// process environment (e.g. $GEMINI_API_KEY).
func (e *Engine) expandMockEnv(body string) string {
	return os.Expand(body, func(name string) string {
		if apiName, ok := apiNameFromURLVar(name); ok {
			if v, ok := e.apiURLs[apiName]; ok {
				return v
			}
		}
		return os.Getenv(name)
	})
}

// apiNameFromURLVar maps an API_<NAME>_URL variable name back to its API name
// ("MEDIA_DOWNLOAD" -> "media_download"). ok is false for names that are not
// in that shape. Note that API names may themselves contain underscores, so
// any name with the API_ prefix and _URL suffix maps by lowercasing the rest.
func apiNameFromURLVar(varName string) (apiName string, ok bool) {
	const prefix, suffix = "API_", "_URL"
	if !strings.HasPrefix(varName, prefix) || !strings.HasSuffix(varName, suffix) {
		return "", false
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(varName, prefix), suffix)
	if rest == "" {
		return "", false
	}
	return strings.ToLower(rest), true
}
