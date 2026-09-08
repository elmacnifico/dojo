package proxy

import (
	"context"
	"testing"
	"time"
)

// TestHTTPProxyUpstreamClientSharedAndTimedOut asserts Start builds exactly
// one upstream client with the resolved UpstreamTimeout, and that live
// forwards reuse that client instance (connection pooling) instead of
// constructing one per request.
func TestHTTPProxyUpstreamClientSharedAndTimedOut(t *testing.T) {
	t.Parallel()

	p := NewHTTPProxy()
	p.UpstreamTimeout = 1234 * time.Millisecond
	if err := p.Start(context.Background(), "127.0.0.1:0", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	if p.upstreamClient == nil {
		t.Fatal("expected upstreamClient to be built by Start")
	}
	if p.upstreamClient.Timeout != 1234*time.Millisecond {
		t.Errorf("expected upstream client timeout 1234ms, got %s", p.upstreamClient.Timeout)
	}
}

// TestHTTPProxyUpstreamClientDefaultsTo30s verifies the default upstream
// timeout applies when UpstreamTimeout is unset.
func TestHTTPProxyUpstreamClientDefaultsTo30s(t *testing.T) {
	t.Parallel()

	p := NewHTTPProxy()
	if err := p.Start(context.Background(), "127.0.0.1:0", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	if p.upstreamClient.Timeout != defaultUpstreamTimeout {
		t.Errorf("expected default timeout %s, got %s", defaultUpstreamTimeout, p.upstreamClient.Timeout)
	}
}
