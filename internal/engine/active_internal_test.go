package engine

// Tests for the live multi-round flow accessors on ActiveTest: FinalLiveResponse
// (the deadline-handler payload source) and ClaimFinalEvaluation (quiet-window
// claim arbitration). These are the safety-net paths that grade a live flow's
// final payload when the model ends with fewer rounds than the MaxCalls budget.

import (
	"bytes"
	"testing"
	"time"

	"github.com/elmacnifico/dojo/internal/workspace"
)

// newLiveActiveTest builds an ActiveTest with one live eval expectation on the
// given API, mirroring how buildExpectations wires live multi-round flows.
func newLiveActiveTest(apiName string, exp *Expectation) *ActiveTest {
	return &ActiveTest{
		ID: "live-test",
		Test: &workspace.Test{APIs: map[string]workspace.APIConfig{
			apiName: {Mode: "live", Protocol: "http"},
		}},
		Suite: &workspace.Suite{},
		Expectations: map[string][]*Expectation{
			apiName: {exp},
		},
		done: make(chan struct{}),
	}
}

func TestFinalLiveResponse(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"candidates":[{"content":{"parts":[{"text":"final answer"}]}}]}`)

	tests := []struct {
		name        string
		exp         *Expectation
		wantPayload []byte
		wantOK      bool
	}{
		{
			name: "happy path returns stored response",
			exp: &Expectation{
				Target:           "gemini",
				Index:            0,
				Deadline:         time.Minute,
				LastLiveResponse: payload,
			},
			wantPayload: payload,
			wantOK:      true,
		},
		{
			name: "in-flight request defers the final response",
			exp: &Expectation{
				Target:           "gemini",
				Index:            0,
				Deadline:         time.Minute,
				LastLiveResponse: payload,
				InFlight:         1,
			},
			wantOK: false,
		},
		{
			name: "no recorded response",
			exp: &Expectation{
				Target:   "gemini",
				Index:    0,
				Deadline: time.Minute,
			},
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			at := newLiveActiveTest("gemini", tc.exp)
			got, ok := at.FinalLiveResponse("gemini", 0)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if tc.wantOK && !bytes.Equal(got, tc.wantPayload) {
				t.Errorf("payload = %s, want %s", got, tc.wantPayload)
			}
			if !tc.wantOK && got != nil {
				t.Errorf("payload = %s, want nil", got)
			}
		})
	}
}

func TestFinalLiveResponse_OutOfRangeIndex(t *testing.T) {
	t.Parallel()
	at := newLiveActiveTest("gemini", &Expectation{
		Target:           "gemini",
		Index:            0,
		Deadline:         time.Minute,
		LastLiveResponse: []byte("x"),
	})

	if got, ok := at.FinalLiveResponse("gemini", 1); ok || got != nil {
		t.Errorf("idx 1: got (%s, %v), want (nil, false)", got, ok)
	}
	if got, ok := at.FinalLiveResponse("gemini", -1); ok || got != nil {
		t.Errorf("idx -1: got (%s, %v), want (nil, false)", got, ok)
	}
	if got, ok := at.FinalLiveResponse("unknown", 0); ok || got != nil {
		t.Errorf("unknown api: got (%s, %v), want (nil, false)", got, ok)
	}
}

// TestFinalLiveResponse_ReturnsCopy proves the caller may mutate the returned
// slice (the deadline goroutine passes it to the evaluator) without corrupting
// the stored LastLiveResponse a concurrent quiet-window read still sees.
func TestFinalLiveResponse_ReturnsCopy(t *testing.T) {
	t.Parallel()
	payload := []byte("stored final payload")
	at := newLiveActiveTest("gemini", &Expectation{
		Target:           "gemini",
		Index:            0,
		Deadline:         time.Minute,
		LastLiveResponse: payload,
	})

	got, ok := at.FinalLiveResponse("gemini", 0)
	if !ok {
		t.Fatal("expected ok")
	}
	got[0] = 'X'

	stored, ok := at.FinalLiveResponse("gemini", 0)
	if !ok {
		t.Fatal("expected ok on second read")
	}
	if !bytes.Equal(stored, payload) {
		t.Errorf("stored payload was mutated through the returned slice: %s", stored)
	}
}

func TestClaimFinalEvaluation(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"candidates":[{"content":{"parts":[{"text":"final"}]}}]}`)

	tests := []struct {
		name   string
		exp    *Expectation
		gen    int
		wantOK bool
	}{
		{
			name: "happy path claims matching generation",
			exp: &Expectation{
				Target:           "gemini",
				Index:            0,
				Deadline:         time.Minute,
				LastLiveResponse: payload,
				LiveResponseGen:  3,
			},
			gen:    3,
			wantOK: true,
		},
		{
			name: "stale generation stands down",
			exp: &Expectation{
				Target:           "gemini",
				Index:            0,
				Deadline:         time.Minute,
				LastLiveResponse: payload,
				LiveResponseGen:  5,
			},
			gen:    3, // a newer response (gen 5) already re-armed its own window
			wantOK: false,
		},
		{
			name: "in-flight request refuses the claim",
			exp: &Expectation{
				Target:           "gemini",
				Index:            0,
				Deadline:         time.Minute,
				LastLiveResponse: payload,
				LiveResponseGen:  3,
				InFlight:         1,
			},
			gen:    3,
			wantOK: false,
		},
		{
			name: "fulfilled expectation cannot be claimed",
			exp: &Expectation{
				Target:           "gemini",
				Index:            0,
				Deadline:         time.Minute,
				Fulfilled:        true,
				LastLiveResponse: payload,
				LiveResponseGen:  3,
			},
			gen:    3,
			wantOK: false,
		},
		{
			name: "no recorded response cannot be claimed",
			exp: &Expectation{
				Target:          "gemini",
				Index:           0,
				Deadline:        time.Minute,
				LiveResponseGen: 3,
			},
			gen:    3,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			at := newLiveActiveTest("gemini", tc.exp)

			got, ok := at.ClaimFinalEvaluation("gemini", 0, tc.gen)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if tc.wantOK && !bytes.Equal(got, payload) {
				t.Errorf("payload = %s, want %s", got, payload)
			}
			if !tc.wantOK && got != nil {
				t.Errorf("payload = %s, want nil", got)
			}
		})
	}
}

// TestClaimFinalEvaluation_OutOfRangeIndex covers the guard bounds.
func TestClaimFinalEvaluation_OutOfRangeIndex(t *testing.T) {
	t.Parallel()
	at := newLiveActiveTest("gemini", &Expectation{
		Target:           "gemini",
		Index:            0,
		Deadline:         time.Minute,
		LastLiveResponse: []byte("x"),
		LiveResponseGen:  1,
	})

	if got, ok := at.ClaimFinalEvaluation("gemini", 1, 1); ok || got != nil {
		t.Errorf("idx 1: got (%s, %v), want (nil, false)", got, ok)
	}
	if got, ok := at.ClaimFinalEvaluation("gemini", -1, 1); ok || got != nil {
		t.Errorf("idx -1: got (%s, %v), want (nil, false)", got, ok)
	}
	if got, ok := at.ClaimFinalEvaluation("unknown", 0, 1); ok || got != nil {
		t.Errorf("unknown api: got (%s, %v), want (nil, false)", got, ok)
	}
}
