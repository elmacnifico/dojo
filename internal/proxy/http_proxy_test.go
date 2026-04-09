package proxy_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dojo/internal/engine"
	"dojo/internal/proxy"
	"dojo/internal/workspace"
	"dojo/pkg/dojo"
)

type failingMatchTable struct{}

func (f *failingMatchTable) ProcessRequest(protocol, apiName string, reqPayload []byte) dojo.MatchResult {
	return dojo.MatchResult{Err: fmt.Errorf("injected match failure")}
}

func (f *failingMatchTable) ProcessResponse(protocol, matchedID, apiName string, reqPayload []byte, respPayload []byte) {}

func TestHTTPProxy(t *testing.T) {
	realAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/live-endpoint" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"live": true}`))
			return
		}
		http.Error(w, "Not found on real API", http.StatusNotFound)
	}))
	defer realAPI.Close()

	ws := &workspace.Workspace{
		Suites: map[string]*workspace.Suite{
			"test": {
				APIs: map[string]workspace.APIConfig{
					"realAPI": {
						Mode: "live",
						URL:  realAPI.URL,
					},
					"mockAPI": {
						Mode: "mock",
						ExpectedRequest: &workspace.PayloadSpec{
							Payload: []byte(`{"id": "test_123"}`),
						},
						DefaultResponse: &workspace.DefaultResponse{
							Code:    200,
							Payload: []byte(`{"mocked": true}`),
						},
					},
				},
				Tests: map[string]*workspace.Test{
					"test_123": {
						APIs: map[string]workspace.APIConfig{},
						Plan: "Perform -> entrypoints/webhook\nExpect -> mockAPI",
					},
				},
			},
		},
	}

	eng := engine.NewEngine(ws)
	eng.ActiveSuite = ws.Suites["test"]
	
	activeTest := &engine.ActiveTest{
		ID:    "test_123",
		Test:  ws.Suites["test"].Tests["test_123"],
		Suite: ws.Suites["test"],
		Expectations: map[string]*engine.Expectation{
			"mockAPI": {Target: "mockAPI"},
		},
	}
	eng.Registry.Register("test_123", activeTest)

	// Start proxies for the suite
	eng.HTTPProxy.Start(context.Background(), "127.0.0.1:0", eng)
	defer eng.HTTPProxy.Stop()

	proxyURL := "http://" + eng.HTTPProxy.Addr()

	reqMock, _ := http.NewRequest(http.MethodPost, proxyURL+"/mockAPI/mocked-endpoint", bytes.NewReader([]byte(`{"id": "test_123"}`)))
	reqLive, _ := http.NewRequest(http.MethodGet, proxyURL+"/realAPI/live-endpoint", nil)

	client := &http.Client{}

	respMock, err := client.Do(reqMock)
	if err != nil {
		t.Fatalf("Failed to request mocked endpoint: %v", err)
	}
	defer respMock.Body.Close()

	mockBody, _ := io.ReadAll(respMock.Body)
	if string(mockBody) != `{"mocked": true}` {
		t.Errorf("Expected mocked response, got: %s", string(mockBody))
	}
	
	if !activeTest.Expectations["mockAPI"].Fulfilled {
		t.Errorf("Expected mockAPI expectation to be fulfilled")
	}

	respLive, err := client.Do(reqLive)
	if err != nil {
		t.Fatalf("Failed to request live endpoint: %v", err)
	}
	defer respLive.Body.Close()

	liveBody, _ := io.ReadAll(respLive.Body)
	if string(liveBody) != `{"live": true}` {
		t.Errorf("Expected live response from real API, got: %s", string(liveBody))
	}
}

type emptyDestMatchTable struct{}

func (emptyDestMatchTable) ProcessRequest(string, string, []byte) dojo.MatchResult {
	return dojo.MatchResult{}
}
func (emptyDestMatchTable) ProcessResponse(string, string, string, []byte, []byte) {}

type zeroCodeMockTable struct{}

func (zeroCodeMockTable) ProcessRequest(string, string, []byte) dojo.MatchResult {
	return dojo.MatchResult{MatchedID: "id", IsMock: true, MockCode: 0, MockResponse: []byte(`{"ok":1}`)}
}
func (zeroCodeMockTable) ProcessResponse(string, string, string, []byte, []byte) {}

func TestHTTPProxy_RootPathReturns400(t *testing.T) {
	t.Parallel()
	p := proxy.NewHTTPProxy()
	if err := p.Start(context.Background(), "127.0.0.1:0", stubMatchTable{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	resp, err := http.Post("http://"+p.Addr()+"/", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestHTTPProxy_LiveModeNoDestURL(t *testing.T) {
	t.Parallel()
	p := proxy.NewHTTPProxy()
	if err := p.Start(context.Background(), "127.0.0.1:0", emptyDestMatchTable{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	resp, err := http.Post("http://"+p.Addr()+"/someapi/path", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestHTTPProxy_MockCodeZeroNormalization(t *testing.T) {
	t.Parallel()
	p := proxy.NewHTTPProxy()
	if err := p.Start(context.Background(), "127.0.0.1:0", zeroCodeMockTable{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	resp, err := http.Post("http://"+p.Addr()+"/api/endpoint", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
}

func TestHTTPProxy_ProcessRequestErrorReturns502(t *testing.T) {
	p := proxy.NewHTTPProxy()
	if err := p.Start(context.Background(), "127.0.0.1:0", &failingMatchTable{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	resp, err := http.Post("http://"+p.Addr()+"/any/foo", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("want status %d, got %d", http.StatusBadGateway, resp.StatusCode)
	}
}

type slowLiveMatchTable struct {
	destURL string
}

func (s slowLiveMatchTable) ProcessRequest(string, string, []byte) dojo.MatchResult {
	return dojo.MatchResult{DestURL: s.destURL}
}
func (slowLiveMatchTable) ProcessResponse(string, string, string, []byte, []byte) {}

func TestHTTPProxy_UpstreamTimeout(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-done
	}))
	defer func() {
		close(done)
		slow.Close()
	}()

	p := proxy.NewHTTPProxy()
	p.UpstreamTimeout = 100 * time.Millisecond
	if err := p.Start(context.Background(), "127.0.0.1:0", slowLiveMatchTable{destURL: slow.URL}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	resp, err := http.Post("http://"+p.Addr()+"/api/endpoint", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 on upstream timeout, got %d", resp.StatusCode)
	}
}
