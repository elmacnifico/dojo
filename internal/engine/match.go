package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"reflect"
	"text/template"
	"strings"

	"github.com/elmacnifico/dojo/internal/workspace"
	"github.com/elmacnifico/dojo/pkg/dojo"

	"github.com/jackc/pgproto3/v2"
)

// resolvePostgresAPI returns the first Postgres API name (sorted) from the suite.
// Sorting ensures deterministic behavior when multiple Postgres APIs exist.
func (e *Engine) resolvePostgresAPI(suite *workspace.Suite) string {
	names := make([]string, 0, len(suite.APIs))
	for name, cfg := range suite.APIs {
		if cfg.Protocol == "postgres" || strings.HasPrefix(cfg.URL, "postgres://") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

func effectiveAPIConfig(suite *workspace.Suite, at *ActiveTest, apiName string) workspace.APIConfig {
	cfg := suite.APIs[apiName]
	if at != nil {
		if tcfg, ok := at.Test.APIs[apiName]; ok {
			return tcfg
		}
	}
	return cfg
}

func payloadsMatch(cfg workspace.APIConfig, actual, expected []byte, vars map[string]any) bool {
	proto := workspace.APIProtocolForMatch(cfg)
	if proto == "postgres" {
		expectedStr := string(expected)
		if len(vars) > 0 {
			tmpl, err := template.New("sql").Parse(expectedStr)
			if err == nil {
				var buf bytes.Buffer
				if err := tmpl.Execute(&buf, vars); err == nil {
					expectedStr = buf.String()
				}
			}
		}
		na := workspace.NormalizeSQL(string(actual))
		ne := workspace.NormalizeSQL(expectedStr)
		return strings.Contains(na, ne)
	}
	return workspace.JSONSubsetMatch(actual, expected)
}

// matchHit records a matched active test and the expectation index within it.
type matchHit struct {
	test *ActiveTest
	idx  int
}

// headersMatch flattens actual HTTP headers to {"Header-Name": "first-value", ...}
// and checks that every key/value in expectedJSON is present using JSONSubsetMatch.
func headersMatch(actual map[string][]string, expectedJSON []byte) bool {
	flat := make(map[string]string, len(actual))
	for k, vv := range actual {
		if len(vv) > 0 {
			flat[k] = vv[0]
		}
	}
	actualJSON, err := json.Marshal(flat)
	if err != nil {
		return false
	}
	return workspace.JSONSubsetMatch(actualJSON, expectedJSON)
}

// expectationSpecMatches reports whether an intercepted request satisfies the
// given ordered expectation (body subset, optional headers, optional path).
func expectationSpecMatches(cfg workspace.APIConfig, spec workspace.ExpectationSpec, reqPayload []byte, reqHeaders map[string][]string, reqURL string, vars map[string]any) bool {
	bodyMatch := spec.ExpectedRequest == nil || len(spec.ExpectedRequest.Payload) == 0 ||
		payloadsMatch(cfg, reqPayload, spec.ExpectedRequest.Payload, vars)
	if !bodyMatch {
		return false
	}
	if spec.ExpectedHeaders != nil && len(spec.ExpectedHeaders.Payload) > 0 {
		if reqHeaders == nil || !headersMatch(reqHeaders, spec.ExpectedHeaders.Payload) {
			return false
		}
	}
	if spec.Path != "" && spec.Path != reqURL {
		return false
	}
	return true
}

// maxCallsLookaheadAllowed is true when the expectation at idx may still accept
// more identical matches, so a request that also matches the next spec should
// advance (greedy MaxCalls semantics). When MaxCalls is 0 or 1 and no match
// has been counted yet, we do not lookahead so duplicate ordered expectations
// with the same fixture still consume slots one at a time.
func maxCallsLookaheadAllowed(exp *Expectation) bool {
	return exp.MaxCalls > 1 || exp.MatchCount > 0
}

// orderedExpectRequestPayloadEqual returns true when two ordered expectation
// specs use the same expected request bytes (after trim). When equal, we do
// not apply MaxCalls lookahead so multiple Expect lines with the same Request
// fixture still match one outbound call per slot.
func orderedExpectRequestPayloadEqual(a, b workspace.ExpectationSpec) bool {
	var ap, bp []byte
	if a.ExpectedRequest != nil {
		ap = bytes.TrimSpace(a.ExpectedRequest.Payload)
	}
	if b.ExpectedRequest != nil {
		bp = bytes.TrimSpace(b.ExpectedRequest.Payload)
	}
	return bytes.Equal(ap, bp)
}

// ProcessRequest correlates the intercepted request to an active test by
// comparing actual and expected request payloads using subset matching. For
// APIs with ordered expectations, the first unfulfilled expectation whose
// payload matches is used, with MaxCalls lookahead when a repeat slot also
// matches the next ordered spec. reqHeaders carries HTTP headers (nil for non-HTTP).
func (e *Engine) ProcessRequest(protocol, apiName string, reqPayload []byte, reqHeaders map[string][]string, reqURL string) dojo.MatchResult {
	e.processRequestMu.Lock()
	defer e.processRequestMu.Unlock()

	if e.ActiveSuite == nil {
		return dojo.MatchResult{Err: fmt.Errorf("no active suite")}
	}

	suite := e.ActiveSuite

	if protocol == "postgres" {
		apiName = e.resolvePostgresAPI(suite)
	}

	if apiName == "" {
		return dojo.MatchResult{Err: fmt.Errorf("api not found for protocol %s", protocol)}
	}

	apiConfig, ok := suite.APIs[apiName]
	if !ok {
		return dojo.MatchResult{Err: fmt.Errorf("api %s not found in suite", apiName)}
	}

	var result dojo.MatchResult

	var hits []matchHit
	e.Registry.ForEach(func(_ string, at *ActiveTest) bool {
		exps := at.Expectations[apiName]
		if len(exps) == 0 {
			return true
		}
		eff := effectiveAPIConfig(suite, at, apiName)

		// Try ordered expectations first.
		if len(eff.OrderedExpectations) > 0 {
			for i, spec := range eff.OrderedExpectations {
				if i >= len(exps) || exps[i].Fulfilled {
					continue
				}
				if !expectationSpecMatches(eff, spec, reqPayload, reqHeaders, reqURL, at.Variables) {
					continue
				}
				j := i
				for j+1 < len(eff.OrderedExpectations) && j+1 < len(exps) && !exps[j+1].Fulfilled {
					nextSpec := eff.OrderedExpectations[j+1]
					if !expectationSpecMatches(eff, nextSpec, reqPayload, reqHeaders, reqURL, at.Variables) {
						break
					}
					if !maxCallsLookaheadAllowed(exps[j]) {
						break
					}
					if orderedExpectRequestPayloadEqual(eff.OrderedExpectations[j], eff.OrderedExpectations[j+1]) {
						break
					}
					at.ForceFulfillEarly(apiName, j)
					j++
				}
				hits = append(hits, matchHit{test: at, idx: j})
				return true
			}
			return true
		}

		// Fallback: single ExpectedRequest (backward compat).
		if exps[0].Fulfilled {
			return true
		}
		bodyMatch := eff.ExpectedRequest == nil || len(eff.ExpectedRequest.Payload) == 0 ||
			payloadsMatch(eff, reqPayload, eff.ExpectedRequest.Payload, at.Variables)
		if !bodyMatch {
			return true
		}
		if eff.ExpectedHeaders != nil && len(eff.ExpectedHeaders.Payload) > 0 {
			if reqHeaders == nil || !headersMatch(reqHeaders, eff.ExpectedHeaders.Payload) {
				return true
			}
		}
		hits = append(hits, matchHit{test: at, idx: 0})
		return true
	})

	var activeTest *ActiveTest
	var matchedIdx int
	switch len(hits) {
	case 0:
		activeTest = nil
	case 1:
		activeTest = hits[0].test
		matchedIdx = hits[0].idx
		result.MatchedID = activeTest.ID
	default:
		return dojo.MatchResult{
			Err: fmt.Errorf("ambiguous request match for API %q: %d active tests matched", apiName, len(hits)),
		}
	}

	if activeTest != nil {
		apiConfig = effectiveAPIConfig(suite, activeTest, apiName)
	} else {
		// No expectation matched, but if exactly one active test carries a
		// test-level API override for this API, use that config for the mock
		// response. This lets tests override default_response (e.g. serve a
		// binary file) without needing an Expect clause.
		// Skip overrides from tests that have expectations for this API — their
		// override is for matched-response workflows, not general defaults.
		var override *workspace.APIConfig
		e.Registry.ForEach(func(_ string, at *ActiveTest) bool {
			if len(at.Expectations[apiName]) > 0 {
				return true
			}
			if tcfg, ok := at.Test.APIs[apiName]; ok {
				suiteCfg := suite.APIs[apiName]
				// Check if this test actually overrides the suite config
				if !reflect.DeepEqual(tcfg, suiteCfg) {
					if override != nil {
						override = nil // ambiguous — more than one test overrides
						return false
					}
					override = &tcfg
				}
			}
			return true
		})
		if override != nil {
			apiConfig = *override
		}
	}

	if activeTest != nil {
		apiConfig = effectiveAPIConfig(suite, activeTest, apiName)

		exps := activeTest.Expectations[apiName]
		exp := exps[matchedIdx]

		if exp.RequiresEval && apiConfig.Mode != "live" {
			go func(at *ActiveTest, api string, idx int, payload []byte) {
				var evalErr error
				if err := e.Evaluate(at, payload); err != nil {
					evalErr = fmt.Errorf("AI Evaluation failed: %w", err)
				}
				at.MarkFulfilled(api, idx, evalErr)
			}(activeTest, apiName, matchedIdx, reqPayload)
		} else {
			hasExpResp := apiConfig.ExpectedResponse != nil && len(apiConfig.ExpectedResponse.Payload) > 0
			deferToResponse := (protocol == "http" && apiConfig.Mode == "live" &&
				(hasExpResp || exp.RequiresEval)) ||
				(protocol == "postgres" && apiConfig.Mode == "live")
			if !deferToResponse {
				activeTest.MarkFulfilled(apiName, matchedIdx, nil)
			}
		}

		// Use the matched expectation's specific response if available.
		if len(apiConfig.OrderedExpectations) > matchedIdx {
			if resp := apiConfig.OrderedExpectations[matchedIdx].Response; resp != nil {
				apiConfig.DefaultResponse = resp
			}
		}
	}

	result.IsMock = apiConfig.Mode == "mock"
	result.DestURL = apiConfig.URL
	result.Headers = apiConfig.Headers

	if result.IsMock && apiConfig.DefaultResponse != nil {
		result.MockCode = apiConfig.DefaultResponse.Code
		if result.MockCode == 0 {
			result.MockCode = 200
		}
		result.MockContentType = apiConfig.DefaultResponse.ContentType
		payload := apiConfig.DefaultResponse.Payload
		if apiConfig.DefaultResponse.File == "" || strings.HasSuffix(apiConfig.DefaultResponse.File, ".json") {
			payload = []byte(os.ExpandEnv(string(payload)))
		}
		result.MockResponse = payload

		// Parse usage from mock response for tracking/reporting
		if activeTest != nil {
			if usage, ok := parseUsage(payload); ok {
				addUsageToActiveTest(activeTest, apiName, usage)
			}
		}
	}

	return result
}

// ProcessResponse validates the intercepted live response payload for a specific API.
func (e *Engine) ProcessResponse(protocol, matchedID, apiName string, reqPayload []byte, respPayload []byte) {
	if e.ActiveSuite == nil || matchedID == "" {
		return
	}

	e.processRequestMu.Lock()
	defer e.processRequestMu.Unlock()

	suite := e.ActiveSuite
	activeTest, ok := e.Registry.Lookup(matchedID)
	if !ok {
		return
	}

	if protocol == "postgres" {
		for pgName, apiConfig := range suite.APIs {
			if apiConfig.Protocol == "postgres" || strings.HasPrefix(apiConfig.URL, "postgres://") {
				if apiName != "" && pgName != apiName {
					continue
				}
				if testAPI, ok := activeTest.Test.APIs[pgName]; ok {
					apiConfig = testAPI
				}
				unfulfilled := activeTest.FirstUnfulfilled(pgName)
				if unfulfilled == nil {
					continue
				}
				idx := unfulfilled.Index

				// Verify the response's query matches the unfulfilled expectation
				// to prevent stale responses from fulfilling the wrong ordered expectation.
				eff := effectiveAPIConfig(suite, activeTest, pgName)
				if len(eff.OrderedExpectations) > idx {
					spec := eff.OrderedExpectations[idx]
					if spec.ExpectedRequest != nil && len(spec.ExpectedRequest.Payload) > 0 {
						if !payloadsMatch(eff, reqPayload, spec.ExpectedRequest.Payload, activeTest.Variables) {
							continue
						}
					}
				}

				if apiConfig.ExpectedResponse != nil && len(apiConfig.ExpectedResponse.Payload) > 0 {
					expectedStr := string(apiConfig.ExpectedResponse.Payload)
					actualStr := string(respPayload)

					if strings.Contains(actualStr, expectedStr) {
						e.evalAndMark(activeTest, pgName, idx, respPayload)
					}
				} else {
					complete, errMsg := pgResponseCheck(respPayload)
					if !complete {
						continue
					}
					if errMsg != "" {
						activeTest.MarkFulfilled(pgName, idx, fmt.Errorf("postgres query failed: %s", errMsg))
					} else {
						e.evalAndMark(activeTest, pgName, idx, respPayload)
					}
				}
			}
		}
	} else if protocol == "http" && apiName != "" {
		apiConfig, ok := suite.APIs[apiName]
		if !ok {
			return
		}
		if testAPI, ok := activeTest.Test.APIs[apiName]; ok {
			apiConfig = testAPI
		}
		if apiConfig.Mode != "live" {
			return
		}

		if usage, ok := parseUsage(respPayload); ok {
			addUsageToActiveTest(activeTest, apiName, usage)
		}

		if apiConfig.ExpectedResponse != nil && len(apiConfig.ExpectedResponse.Payload) > 0 {
			unfulfilled := activeTest.FirstUnfulfilled(apiName)
			idx := 0
			if unfulfilled != nil {
				idx = unfulfilled.Index
			}
			if httpPayloadContains(respPayload, apiConfig.ExpectedResponse.Payload) {
				e.evalAndMark(activeTest, apiName, idx, respPayload)
			} else {
				exp := truncate(string(apiConfig.ExpectedResponse.Payload), 500)
				act := truncate(string(respPayload), 500)
				activeTest.MarkFulfilled(apiName, idx, &MismatchError{
					Reason:   fmt.Sprintf("live response mismatch for API %s\n  expected (substring): %s\n  actual:              %s", apiName, exp, act),
					Expected: exp,
					Actual:   act,
				})
			}
		} else if unfulfilled := activeTest.FirstUnfulfilled(apiName); unfulfilled != nil && unfulfilled.RequiresEval {
			payloadCopy := make([]byte, len(respPayload))
			copy(payloadCopy, respPayload)
			go e.evalAndMark(activeTest, apiName, unfulfilled.Index, payloadCopy)
		}
	}
}

// evalAndMark runs AI evaluation (if required) and marks the expectation fulfilled.
func (e *Engine) evalAndMark(activeTest *ActiveTest, apiName string, idx int, payload []byte) {
	var checkErr error
	exps := activeTest.Expectations[apiName]
	if idx < len(exps) && exps[idx].RequiresEval {
		if err := e.Evaluate(activeTest, payload); err != nil {
			checkErr = fmt.Errorf("AI Evaluation failed: %w", err)
		}
	}
	activeTest.MarkFulfilled(apiName, idx, checkErr)
}

// pgResponseCheck parses raw pgproto3 backend response bytes and returns
// whether the query completed and any error message from Postgres.
//   - ErrorResponse found: (true, errorMessage)
//   - ReadyForQuery found: (true, "")   — success
//   - Neither yet:         (false, "")   — incomplete, caller should wait
func pgResponseCheck(data []byte) (complete bool, errMsg string) {
	r := bytes.NewReader(data)
	frontend := pgproto3.NewFrontend(pgproto3.NewChunkReader(r), nil)
	for {
		msg, err := frontend.Receive()
		if err != nil {
			return false, ""
		}
		switch m := msg.(type) {
		case *pgproto3.ErrorResponse:
			return true, m.Message
		case *pgproto3.ReadyForQuery:
			return true, ""
		}
	}
}

func httpPayloadContains(actual, expected []byte) bool {
	strippedA := bytes.ReplaceAll(actual, []byte(" "), []byte(""))
	strippedA = bytes.ReplaceAll(strippedA, []byte("\n"), []byte(""))
	strippedE := bytes.ReplaceAll(expected, []byte(" "), []byte(""))
	strippedE = bytes.ReplaceAll(strippedE, []byte("\n"), []byte(""))
	if bytes.Contains(strippedA, strippedE) {
		return true
	}
	return bytes.Contains(actual, expected)
}
