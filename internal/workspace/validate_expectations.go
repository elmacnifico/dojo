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
		api         string
		path        string
		norm        string
		normHeaders string
	}
	seen := make(map[expectKey][]string)

	for testName, test := range suite.Tests {
		for apiName, cfg := range test.APIs {
			proto := APIProtocolForMatch(cfg)

			// Collect all (request payload, header payload) pairs for this API.
			type payloadPair struct {
				body    []byte
				headers []byte
				path    string
			}
			var pairs []payloadPair
			for _, spec := range cfg.OrderedExpectations {
				if spec.ExpectedRequest != nil && len(spec.ExpectedRequest.Payload) > 0 {
					var hdrs []byte
					if spec.ExpectedHeaders != nil {
						hdrs = spec.ExpectedHeaders.Payload
					}
					pairs = append(pairs, payloadPair{body: spec.ExpectedRequest.Payload, headers: hdrs, path: spec.Path})
				} else if spec.Path != "" {
					pairs = append(pairs, payloadPair{path: spec.Path})
				}
			}
			if len(pairs) == 0 && cfg.ExpectedRequest != nil && len(cfg.ExpectedRequest.Payload) > 0 {
				var hdrs []byte
				if cfg.ExpectedHeaders != nil {
					hdrs = cfg.ExpectedHeaders.Payload
				}
				pairs = append(pairs, payloadPair{body: cfg.ExpectedRequest.Payload, headers: hdrs})
			}

			for _, p := range pairs {
				norm := NormalizePayloadForMatch(proto, p.body)
				if norm == "" && p.path == "" {
					continue
				}
				nh := NormalizeHTTPBody(p.headers)
				k := expectKey{api: apiName, path: p.path, norm: norm, normHeaders: nh}
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
