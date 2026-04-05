package proxy

import (
	"fmt"
	"io"
	"net"
	"testing"
)

func TestIsConnClosed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"io.ErrClosedPipe", io.ErrClosedPipe, true},
		{"net.ErrClosed", net.ErrClosed, true},
		{"string contains closed", fmt.Errorf("connection closed by peer"), true},
		{"io.EOF", io.EOF, false},
		{"timeout error", fmt.Errorf("timeout"), false},
		{"generic error", fmt.Errorf("something went wrong"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isConnClosed(tc.err)
			if got != tc.want {
				t.Errorf("isConnClosed(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
