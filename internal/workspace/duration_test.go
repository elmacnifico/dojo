package workspace

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDurationUnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{name: "seconds", input: `"5s"`, want: 5 * time.Second},
		{name: "milliseconds", input: `"300ms"`, want: 300 * time.Millisecond},
		{name: "minutes", input: `"2m"`, want: 2 * time.Minute},
		{name: "empty string", input: `""`, want: 0},
		{name: "invalid", input: `"bogus"`, wantErr: true},
		{name: "number", input: `123`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var d Duration
			err := json.Unmarshal([]byte(tc.input), &d)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %s, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if d.Duration != tc.want {
				t.Errorf("got %v, want %v", d.Duration, tc.want)
			}
		})
	}
}

func TestDurationMarshalJSON(t *testing.T) {
	t.Parallel()

	d := Duration{Duration: 5 * time.Second}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `"5s"` {
		t.Errorf("got %s, want %q", string(b), "5s")
	}

	zero := Duration{}
	b, err = json.Marshal(zero)
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	if string(b) != `""` {
		t.Errorf("got %s, want empty string", string(b))
	}
}

func TestTimeoutConfigResolveDefaults(t *testing.T) {
	t.Parallel()

	var tc TimeoutConfig
	tc.ResolveDefaults()

	if tc.SUTStartup.Duration != DefaultSUTStartup {
		t.Errorf("SUTStartup: got %v, want %v", tc.SUTStartup.Duration, DefaultSUTStartup)
	}
	if tc.TCPPollInterval.Duration != DefaultTCPPollInterval {
		t.Errorf("TCPPollInterval: got %v, want %v", tc.TCPPollInterval.Duration, DefaultTCPPollInterval)
	}
	if tc.TCPDialTimeout.Duration != DefaultTCPDialTimeout {
		t.Errorf("TCPDialTimeout: got %v, want %v", tc.TCPDialTimeout.Duration, DefaultTCPDialTimeout)
	}
	if tc.HTTPClient.Duration != DefaultHTTPClient {
		t.Errorf("HTTPClient: got %v, want %v", tc.HTTPClient.Duration, DefaultHTTPClient)
	}
	if tc.AIEvaluator.Duration != DefaultAIEvaluator {
		t.Errorf("AIEvaluator: got %v, want %v", tc.AIEvaluator.Duration, DefaultAIEvaluator)
	}
}

func TestTimeoutConfigCustomValues(t *testing.T) {
	t.Parallel()

	tc := TimeoutConfig{
		SUTStartup:      Duration{Duration: 10 * time.Second},
		TCPPollInterval: Duration{Duration: 100 * time.Millisecond},
		TCPDialTimeout:  Duration{Duration: 500 * time.Millisecond},
		HTTPClient:      Duration{Duration: 15 * time.Second},
		AIEvaluator:     Duration{Duration: 60 * time.Second},
	}
	tc.ResolveDefaults()

	if tc.SUTStartup.Duration != 10*time.Second {
		t.Errorf("SUTStartup: got %v, want 10s", tc.SUTStartup.Duration)
	}
	if tc.TCPPollInterval.Duration != 100*time.Millisecond {
		t.Errorf("TCPPollInterval: got %v, want 100ms", tc.TCPPollInterval.Duration)
	}
	if tc.TCPDialTimeout.Duration != 500*time.Millisecond {
		t.Errorf("TCPDialTimeout: got %v, want 500ms", tc.TCPDialTimeout.Duration)
	}
	if tc.HTTPClient.Duration != 15*time.Second {
		t.Errorf("HTTPClient: got %v, want 15s", tc.HTTPClient.Duration)
	}
	if tc.AIEvaluator.Duration != 60*time.Second {
		t.Errorf("AIEvaluator: got %v, want 60s", tc.AIEvaluator.Duration)
	}
}

func TestTimeoutConfigFromJSON(t *testing.T) {
	t.Parallel()

	input := `{
		"concurrency": 5,
		"timeouts": {
			"sut_startup": "10s",
			"http_client": "15s"
		}
	}`

	var cfg DojoConfig
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if cfg.Timeouts.SUTStartup.Duration != 10*time.Second {
		t.Errorf("SUTStartup: got %v, want 10s", cfg.Timeouts.SUTStartup.Duration)
	}
	if cfg.Timeouts.HTTPClient.Duration != 15*time.Second {
		t.Errorf("HTTPClient: got %v, want 15s", cfg.Timeouts.HTTPClient.Duration)
	}
	// Zero-valued fields should be zero before ResolveDefaults
	if cfg.Timeouts.TCPPollInterval.Duration != 0 {
		t.Errorf("TCPPollInterval: got %v, want 0", cfg.Timeouts.TCPPollInterval.Duration)
	}
}
