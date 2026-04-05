package workspace_test

import (
	"strings"
	"testing"

	"dojo/internal/workspace"
)

func TestExtractCorrelation(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *workspace.CorrelationConfig
		payload     []byte
		expectedID  string
		expectedErr bool
	}{
		{
			name: "jsonpath match",
			cfg: &workspace.CorrelationConfig{
				Type:   "jsonpath",
				Target: "payload.test_id",
			},
			payload:    []byte(`{"payload": {"test_id": "test_001"}}`),
			expectedID: "test_001",
		},
		{
			name: "regex match",
			cfg: &workspace.CorrelationConfig{
				Type:   "regex",
				Target: `(test_[0-9]+)`,
			},
			payload:    []byte("INSERT INTO users VALUES ('test_002_alice')"),
			expectedID: "test_002",
		},
		{
			name: "exact value override",
			cfg: &workspace.CorrelationConfig{
				Value: "test_003",
			},
			payload:    []byte("this payload contains test_003 here"),
			expectedID: "test_003",
		},
		{
			name: "invalid jsonpath",
			cfg: &workspace.CorrelationConfig{
				Type:   "jsonpath",
				Target: "missing.key",
			},
			payload:     []byte(`{"other": "value"}`),
			expectedErr: true,
		},
		{
			name: "invalid regex",
			cfg: &workspace.CorrelationConfig{
				Type:   "regex",
				Target: `(test_[0-9]+)`,
			},
			payload:     []byte("no match here"),
			expectedErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := workspace.ExtractCorrelation(tt.cfg, tt.payload)
			if tt.expectedErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if id != tt.expectedID {
					t.Errorf("Expected ID %s, got %s", tt.expectedID, id)
				}
			}
		})
	}
}

func TestExtractCorrelation_EdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		cfg        *workspace.CorrelationConfig
		payload    []byte
		wantID     string
		wantErr    bool
		errContain string
	}{
		{"nil config", nil, []byte("x"), "", true, "missing correlation"},
		{"jsonpath empty target", &workspace.CorrelationConfig{Type: "jsonpath", Target: ""}, []byte(`{}`), "", true, "missing target"},
		{"jsonpath invalid json", &workspace.CorrelationConfig{Type: "jsonpath", Target: "id"}, []byte("not json"), "", true, "not valid JSON"},
		{"jsonpath invalid regex", &workspace.CorrelationConfig{Type: "jsonpath", Target: "id", Regex: "[bad"}, []byte(`{"id":"abc"}`), "", true, "invalid regex"},
		{"jsonpath regex no capture group", &workspace.CorrelationConfig{Type: "jsonpath", Target: "id", Regex: "test_[0-9]+"}, []byte(`{"id":"test_99"}`), "test_99", false, ""},
		{"jsonpath regex no match", &workspace.CorrelationConfig{Type: "jsonpath", Target: "id", Regex: "^zzz"}, []byte(`{"id":"abc"}`), "", true, "did not match"},
		{"regex empty target", &workspace.CorrelationConfig{Type: "regex", Target: ""}, []byte("x"), "", true, "missing target"},
		{"regex invalid pattern", &workspace.CorrelationConfig{Type: "regex", Target: "[bad"}, []byte("x"), "", true, "invalid regex"},
		{"regex no capture returns full match", &workspace.CorrelationConfig{Type: "regex", Target: "test_[0-9]+"}, []byte("some test_42 data"), "test_42", false, ""},
		{"regex no match", &workspace.CorrelationConfig{Type: "regex", Target: "^zzz"}, []byte("abc"), "", true, "did not match"},
		{"value not in payload", &workspace.CorrelationConfig{Value: "missing"}, []byte("other"), "", true, "does not contain"},
		{"unknown type", &workspace.CorrelationConfig{Type: "xpath", Target: "//id"}, []byte("<id/>"), "", true, "unknown correlation type"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id, err := workspace.ExtractCorrelation(tc.cfg, tc.payload)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tc.errContain != "" && !strings.Contains(err.Error(), tc.errContain) {
					t.Fatalf("error %q should contain %q", err.Error(), tc.errContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tc.wantID {
				t.Errorf("got %q, want %q", id, tc.wantID)
			}
		})
	}
}

func TestFindCorrelationByValue(t *testing.T) {
	if !workspace.FindCorrelationByValue([]byte("hello ord_123 world"), "ord_123") {
		t.Errorf("Expected true")
	}
}
