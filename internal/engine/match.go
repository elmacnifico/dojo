package engine

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"dojo/internal/workspace"
	"dojo/pkg/dojo"
)

// resolvePostgresAPI returns the name of the single Postgres API in the suite.
// Names are sorted so behavior is deterministic when multiple Postgres APIs exist.
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
	na := workspace.NormalizePayloadForMatch(proto, actual)
	ne := workspace.NormalizePayloadForMatch(proto, expected)
	return na == ne
}

// ProcessRequest correlates the intercepted request to an active test by
// comparing normalized actual and expected request payloads. No separate
// correlation config is used; suite load enforces unique expectations per API.
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

	var matched []*ActiveTest
	e.Registry.ForEach(func(_ string, at *ActiveTest) bool {
		if _, has := at.Expectations[apiName]; !has {
			return true
		}
		eff := effectiveAPIConfig(suite, at, apiName)
		if eff.ExpectedRequest == nil || len(eff.ExpectedRequest.Payload) == 0 {
			return true
		}
		if payloadsMatch(eff, reqPayload, eff.ExpectedRequest.Payload) {
			matched = append(matched, at)
		}
		return true
	})

	var activeTest *ActiveTest
	switch len(matched) {
	case 0:
		activeTest = nil
	case 1:
		activeTest = matched[0]
		result.MatchedID = activeTest.ID
	default:
		return dojo.MatchResult{
			Err: fmt.Errorf("ambiguous request match for API %q: %d active tests share the same normalized expectation (suite load validation should have prevented this)", apiName, len(matched)),
		}
	}

	if activeTest != nil {
		apiConfig = effectiveAPIConfig(suite, activeTest, apiName)

		if exp, ok := activeTest.Expectations[apiName]; ok && exp.RequiresEval {
			if evalErr := e.Evaluate(activeTest, reqPayload); evalErr != nil {
				result.Err = fmt.Errorf("AI Evaluation failed: %w", evalErr)
			}
		}

		deferToHTTPResponse := protocol == "http" && apiConfig.Mode == "live" &&
			apiConfig.ExpectedResponse != nil && len(apiConfig.ExpectedResponse.Payload) > 0
		if result.Err != nil || !deferToHTTPResponse {
			activeTest.MarkFulfilled(apiName, result.Err)
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
				if apiConfig.ExpectedResponse != nil && len(apiConfig.ExpectedResponse.Payload) > 0 {
					expectedStr := string(apiConfig.ExpectedResponse.Payload)
					actualStr := string(respPayload)

					if strings.Contains(actualStr, expectedStr) {
						e.evalAndMark(activeTest, pgName, respPayload)
					}
				} else {
					e.evalAndMark(activeTest, pgName, respPayload)
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
			if httpPayloadContains(respPayload, apiConfig.ExpectedResponse.Payload) {
				e.evalAndMark(activeTest, apiName, respPayload)
			}
		}
	}
}

// evalAndMark runs AI evaluation (if required) and marks the expectation fulfilled.
func (e *Engine) evalAndMark(activeTest *ActiveTest, apiName string, payload []byte) {
	var checkErr error
	if exp, ok := activeTest.Expectations[apiName]; ok && exp.RequiresEval {
		if err := e.Evaluate(activeTest, payload); err != nil {
			checkErr = fmt.Errorf("AI Evaluation failed: %w", err)
		}
	}
	activeTest.MarkFulfilled(apiName, checkErr)
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
