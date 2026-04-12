package workspace

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// ResolveHTTPPerform validates the first-phase Perform line: resolves the HTTP
// entrypoint (named or inline method), payload files, With merge, and status.
// It mirrors the resolution logic used at execution time so preflight and the
// engine stay aligned.
func ResolveHTTPPerform(perform ParsedLine, test *Test, suite *Suite, testDir, suiteDir string) (EntrypointConfig, []byte, int, error) {
	var ep EntrypointConfig
	target := perform.Target

	parts := strings.SplitN(target, " ", 2)
	isHTTPMethod := false
	if len(parts) == 2 {
		method := strings.ToUpper(parts[0])
		switch method {
		case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
			isHTTPMethod = true
			ep = EntrypointConfig{
				Type:   "http",
				Method: method,
				Path:   parts[1],
			}
		}
	}

	if !isHTTPMethod {
		epName := strings.TrimPrefix(target, "entrypoints/")
		var ok bool
		ep, ok = suite.Entrypoints[epName]
		if !ok {
			return ep, nil, 0, fmt.Errorf("entrypoint '%s' not found", epName)
		}
		if testEP, ok := test.Entrypoints[epName]; ok {
			ep = testEP
		}
	}

	var payload []byte
	var expectStatus int
	var withJSON []byte
	var hasPayloadClause bool

	for _, clause := range perform.Clauses {
		switch strings.ToLower(clause.Key) {
		case "payload":
			hasPayloadClause = true
			if clause.Value != nil {
				if filepath.Ext(*clause.Value) != "" {
					b, err := ResolveFile(*clause.Value, testDir, suiteDir)
					if err != nil {
						return ep, nil, 0, fmt.Errorf("failed to read payload %s: %w", *clause.Value, err)
					}
					payload = b
				} else {
					payload = []byte(*clause.Value)
				}
			}
		case "with":
			if clause.Value != nil {
				withJSON = []byte(*clause.Value)
			}
		case "status", "expectstatus":
			if clause.Value != nil {
				n, err := strconv.Atoi(*clause.Value)
				if err != nil {
					return ep, nil, 0, fmt.Errorf("invalid Status value %q: must be an integer", *clause.Value)
				}
				expectStatus = n
			}
		case "header":
			if clause.Value != nil {
				if ep.Headers == nil {
					ep.Headers = make(map[string]string)
				}
				hdrParts := strings.SplitN(*clause.Value, ":", 2)
				if len(hdrParts) == 2 {
					ep.Headers[strings.TrimSpace(hdrParts[0])] = strings.TrimSpace(hdrParts[1])
				}
			}
		}
	}

	if !hasPayloadClause && !isHTTPMethod {
		epName := strings.TrimPrefix(target, "entrypoints/")
		implicitFile := epName + ".json"
		b, err := ResolveFile(implicitFile, testDir, suiteDir)
		if err == nil {
			payload = b
		} else if len(withJSON) > 0 {
			return ep, nil, 0, fmt.Errorf("With: clause requires a base payload, but %s was not found", implicitFile)
		}
	}

	if len(withJSON) > 0 {
		if len(payload) == 0 {
			return ep, nil, 0, fmt.Errorf("With: clause requires a base payload")
		}
		merged, err := DeepMergeJSON(payload, withJSON)
		if err != nil {
			return ep, nil, 0, fmt.Errorf("failed to merge With: JSON: %w", err)
		}
		payload = merged
	}

	return ep, payload, expectStatus, nil
}
