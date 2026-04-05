package workspace

import (
	"fmt"
	"sort"
	"strings"
)

// ValidateUniqueExpectedRequests returns an error if two or more tests in the suite
// share the same normalized expected request payload for the same API name.
// This guarantees implicit correlation by full normalized match cannot be ambiguous.
func ValidateUniqueExpectedRequests(suite *Suite) error {
	type expectKey struct {
		api  string
		norm string
	}
	seen := make(map[expectKey][]string)

	for testName, test := range suite.Tests {
		for apiName, cfg := range test.APIs {
			if cfg.ExpectedRequest == nil || len(cfg.ExpectedRequest.Payload) == 0 {
				continue
			}
			proto := APIProtocolForMatch(cfg)
			norm := NormalizePayloadForMatch(proto, cfg.ExpectedRequest.Payload)
			if norm == "" {
				continue
			}
			k := expectKey{api: apiName, norm: norm}
			seen[k] = append(seen[k], testName)
		}
	}

	for k, names := range seen {
		if len(names) < 2 {
			continue
		}
		sort.Strings(names)
		return fmt.Errorf("duplicate normalized expected request for API %q across tests %v (implicit correlation requires unique expectations per API)", k.api, names)
	}
	return nil
}

// APIProtocolForMatch returns "postgres" or "http" for request payload normalization.
func APIProtocolForMatch(cfg APIConfig) string {
	if cfg.Protocol == "postgres" || strings.HasPrefix(cfg.URL, "postgres://") {
		return "postgres"
	}
	return "http"
}
