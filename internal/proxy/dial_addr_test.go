package proxy

import "testing"

func TestExtractPostgresDialAddr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want string
	}{
		{"", ""},
		{"postgres://user:pass@host.internal:5432/db", "host.internal:5432"},
		{"postgres://localhost:5432/db", "localhost:5432"},
	}
	for _, tc := range cases {
		if got := ExtractPostgresDialAddr(tc.raw); got != tc.want {
			t.Errorf("ExtractPostgresDialAddr(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
