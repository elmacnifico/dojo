package engine

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/elmacnifico/dojo/internal/workspace"
)

func TestInferSUTListenTCPAddr(t *testing.T) {
	t.Parallel()

	t.Run("empty URL defaults to 127.0.0.1:8080", func(t *testing.T) {
		s := &workspace.Suite{
			Entrypoints: map[string]workspace.EntrypointConfig{
				"w": {Type: "http", Path: "/trigger"},
			},
		}
		if got := inferSUTListenTCPAddr(s); got != "127.0.0.1:8080" {
			t.Fatalf("got %q, want 127.0.0.1:8080", got)
		}
	})

	t.Run("parses entrypoint URL host and port", func(t *testing.T) {
		s := &workspace.Suite{
			Entrypoints: map[string]workspace.EntrypointConfig{
				"w": {Type: "http", URL: "http://127.0.0.1:9191", Path: "/x"},
			},
		}
		if got := inferSUTListenTCPAddr(s); got != "127.0.0.1:9191" {
			t.Fatalf("got %q, want 127.0.0.1:9191", got)
		}
	})

	t.Run("skips non-http entrypoints", func(t *testing.T) {
		s := &workspace.Suite{
			Entrypoints: map[string]workspace.EntrypointConfig{
				"q": {Type: "queue", URL: "amqp://x"},
			},
		}
		if got := inferSUTListenTCPAddr(s); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})
}

func TestPollTCPDialReady(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := pollTCPDialReady(ctx, ln.Addr().String(), 50*time.Millisecond, 300*time.Millisecond); err != nil {
		t.Fatalf("pollTCPDialReady: %v", err)
	}
}
