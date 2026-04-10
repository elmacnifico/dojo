package engine

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"dojo/internal/workspace"
	"dojo/pkg/dojo"

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

func payloadsMatch(cfg workspace.APIConfig, actual, expected []byte) bool {
	proto := workspace.APIProtocolForMatch(cfg)
	if proto == "postgres" {
		na := workspace.NormalizeSQL(string(actual))
		ne := workspace.NormalizeSQL(string(expected))
		return strings.Contains(na, ne)
	}
	return workspace.JSONSubsetMatch(actual, expected)
}

// matchHit records a matched active test and the expectation index within it.
type matchHit struct {
	test *ActiveTest
	idx  int
}

// ProcessRequest correlates the intercepted request to an active test by
// comparing actual and expected request payloads using subset matching. For
// APIs with ordered expectations, the first unfulfilled expectation whose
// payload matches is used.
func (e *Engine) ProcessRequest(protocol, apiName string, reqPayload []byte) dojo.MatchResult {
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
				if spec.ExpectedRequest == nil || len(spec.ExpectedRequest.Payload) == 0 {
					continue
				}
				if payloadsMatch(eff, reqPayload, spec.ExpectedRequest.Payload) {
					hits = append(hits, matchHit{test: at, idx: i})
					return true
				}
			}
			return true
		}

		// Fallback: single ExpectedRequest (backward compat).
		if eff.ExpectedRequest == nil || len(eff.ExpectedRequest.Payload) == 0 {
			return true
		}
		if exps[0].Fulfilled {
			return true
		}
		if payloadsMatch(eff, reqPayload, eff.ExpectedRequest.Payload) {
			hits = append(hits, matchHit{test: at, idx: 0})
		}
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

		exps := activeTest.Expectations[apiName]
		exp := exps[matchedIdx]

		if exp.RequiresEval && apiConfig.Mode != "live" {
			if evalErr := e.Evaluate(activeTest, reqPayload); evalErr != nil {
				result.Err = fmt.Errorf("AI Evaluation failed: %w", evalErr)
			}
		}

		hasExpResp := apiConfig.ExpectedResponse != nil && len(apiConfig.ExpectedResponse.Payload) > 0
		deferToResponse := (protocol == "http" && apiConfig.Mode == "live" &&
			(hasExpResp || exp.RequiresEval)) ||
			(protocol == "postgres" && apiConfig.Mode == "live")
		if result.Err != nil || !deferToResponse {
			activeTest.MarkFulfilled(apiName, matchedIdx, result.Err)
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
		result.MockResponse = apiConfig.DefaultResponse.Payload
	}

	return result
}

// ProcessResponse validates the intercepted live response payload for a specific API.
func (e *Engine) ProcessResponse(protocol, matchedID, apiName string, reqPayload []byte, respPayload []byte) {
	if e.ActiveSuite == nil || matchedID == "" {
		return
	}

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
						if !payloadsMatch(eff, reqPayload, spec.ExpectedRequest.Payload) {
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
			e.evalAndMark(activeTest, apiName, unfulfilled.Index, respPayload)
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
