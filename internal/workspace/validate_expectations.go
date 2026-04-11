package workspace

import (
	"fmt"
	"sort"
	"strings"
)

// ValidateUniqueExpectedRequests returns an error if two or more tests in the suite
// share the same normalized expected request payload for the same API name.
// This guarantees implicit correlation by full normalized match cannot be ambiguous.
// Within a single test, duplicate expectations for the same API are allowed
// (ordered multi-expectations).
func ValidateUniqueExpectedRequests(suite *Suite) error {
	type expectKey struct {
		api  string
		norm string
	}
	seen := make(map[expectKey][]string)

	for testName, test := range suite.Tests {
		for apiName, cfg := range test.APIs {
			proto := APIProtocolForMatch(cfg)

			// Collect all request payloads for this API (ordered + single).
			var payloads [][]byte
			for _, spec := range cfg.OrderedExpectations {
				if spec.ExpectedRequest != nil && len(spec.ExpectedRequest.Payload) > 0 {
					payloads = append(payloads, spec.ExpectedRequest.Payload)
				}
			}
			if len(payloads) == 0 && cfg.ExpectedRequest != nil && len(cfg.ExpectedRequest.Payload) > 0 {
				payloads = append(payloads, cfg.ExpectedRequest.Payload)
			}

			for _, p := range payloads {
				norm := NormalizePayloadForMatch(proto, p)
				if norm == "" {
					continue
				}
				k := expectKey{api: apiName, norm: norm}
				seen[k] = append(seen[k], testName)
			}
		}
	}

	for k, names := range seen {
		
		uniqueNames := make(map[string]bool)
		for _, n := range names {
			uniqueNames[n] = true
		}
		if len(uniqueNames) < 2 {
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
