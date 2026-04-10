// Package dojo provides the core interfaces for the Dojo testing orchestrator.
package dojo

import "context"

// MatchResult holds the outcome of a [MatchTable.ProcessRequest] call.
type MatchResult struct {
	MatchedID       string
	IsMock          bool
	MockCode        int
	MockResponse    []byte
	MockContentType string
	DestURL         string
	Headers         map[string]string
	Err             error
}

// MatchTable is used by the Observer to intercept and match SUT traffic against expectations.
// It bridges the proxy layer and the engine's global registry.
type MatchTable interface {
	// ProcessRequest matches the intercepted request to an active test using normalized
	// full equality on expected vs actual payloads, then returns mock response details if the API is mocked.
	ProcessRequest(protocol, apiName string, reqPayload []byte) MatchResult

	// ProcessResponse validates the intercepted live response payload for a specific API against the test plan.
	// apiName is the logical API key (e.g. HTTP path prefix). For Postgres it may be empty when the engine
	// matches against all postgres APIs in the suite.
	ProcessResponse(protocol, matchedID, apiName string, reqPayload []byte, respPayload []byte)
}

// Adapter represents an external protocol (HTTP, Postgres, AMQP).
// It acts as the bridge between Dojo's engine and the wire.
type Adapter interface {
	// Trigger is used by the Initiator to start a test.
	Trigger(ctx context.Context, payload []byte, endpointConfig map[string]any) error

	// Listen is used by the Observer to intercept and match SUT traffic.
	Listen(ctx context.Context, matchTable MatchTable) error
}

// EvaluatorResult contains the outcome of an evaluation.
// It provides a strictly-typed response indicating success or failure along with an AI-generated reason.
type EvaluatorResult struct {
	Pass   bool   `json:"pass"`
	Reason string `json:"reason"`
}

// Evaluator evaluates a payload against an expected state or AI rule.
type Evaluator interface {
	// Evaluate compares actual data against an expected rule.
	Evaluate(ctx context.Context, actual []byte, expectedRule string) (EvaluatorResult, error)
}
