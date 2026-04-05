package proxy_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"dojo/internal/proxy"
	"dojo/pkg/dojo"
)

// stubMatchTable is a no-op MatchTable for tests that don't need matching.
type stubMatchTable struct{}

func (stubMatchTable) ProcessRequest(string, string, []byte) dojo.MatchResult {
	return dojo.MatchResult{IsMock: true, MockCode: 200}
}
func (stubMatchTable) ProcessResponse(string, string, string, []byte, []byte) {}

func waitForConnCount(t *testing.T, p *proxy.PostgresProxy, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.ConnCount() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("ConnCount: got %d, want %d (after %v)", p.ConnCount(), want, timeout)
}

func TestPostgresProxyConnIDCleanup(t *testing.T) {
	t.Parallel()

	p := proxy.NewPostgresProxy("")
	if err := p.Start(context.Background(), "127.0.0.1:0", stubMatchTable{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer p.Stop()

	conn, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close()

	waitForConnCount(t, p, 0, 2*time.Second)
}

func TestPostgresProxyConcurrentConnections(t *testing.T) {
	t.Parallel()

	p := proxy.NewPostgresProxy("")
	var mt stubMatchTable
	if err := p.Start(context.Background(), "127.0.0.1:0", mt); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer p.Stop()

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)

	for range n {
		go func() {
			defer wg.Done()
			conn, err := net.Dial("tcp", p.Addr())
			if err != nil {
				t.Errorf("dial: %v", err)
				return
			}
			conn.Close()
		}()
	}

	wg.Wait()
	waitForConnCount(t, p, 0, 2*time.Second)
}
