package proxy_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/elmacnifico/dojo/internal/proxy"
	"github.com/elmacnifico/dojo/pkg/dojo"
)

// stubMatchTable is a no-op MatchTable for tests that don't need matching.
type stubMatchTable struct{}

func (stubMatchTable) ProcessRequest(string, string, []byte, map[string][]string, string) dojo.MatchResult {
	return dojo.MatchResult{IsMock: true, MockCode: 200}
}
func (stubMatchTable) ProcessResponse(string, string, string, []byte, []byte) {}

func waitForConnCount(t *testing.T, p *proxy.PostgresProxy, want int, timeout time.Duration) {
	t.Helper()
	if p.ConnCount() == want {
		return
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ticker.C:
			if p.ConnCount() == want {
				return
			}
		case <-timer.C:
			t.Errorf("ConnCount: got %d, want %d (after %v)", p.ConnCount(), want, timeout)
			return
		}
	}
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
