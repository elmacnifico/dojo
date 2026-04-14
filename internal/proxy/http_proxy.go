// Package proxy implements the HTTP and Postgres interceptors for Dojo.
package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/elmacnifico/dojo/pkg/dojo"
)

const defaultUpstreamTimeout = 30 * time.Second

// HTTPProxy represents the true HTTP Proxy that intercepts traffic, resolves mocks,
// and forwards unknown traffic to the real internet.
type HTTPProxy struct {
	// UpstreamTimeout caps how long a live upstream HTTP call may take. Zero uses
	// [defaultUpstreamTimeout]. Set by the engine from the suite's timeout config.
	UpstreamTimeout time.Duration

	// Trace enables debug logging of HTTP request/response payloads.
	Trace bool

	addr       string
	listener   net.Listener
	mux        *http.ServeMux
	server     *http.Server
	matchTable dojo.MatchTable
	log        *slog.Logger
}

// SetLogger configures the structured logger for the proxy.
func (p *HTTPProxy) SetLogger(l *slog.Logger) {
	p.log = l
}

// NewHTTPProxy initializes a new HTTPProxy.
func NewHTTPProxy() *HTTPProxy {
	return &HTTPProxy{
		mux: http.NewServeMux(),
		log: slog.Default(),
	}
}

// Listen implements the dojo.Adapter interface.
func (p *HTTPProxy) Listen(ctx context.Context, matchTable dojo.MatchTable) error {
	return p.Start(ctx, "127.0.0.1:0", matchTable)
}

// Trigger is a no-op for HTTPProxy.
func (p *HTTPProxy) Trigger(ctx context.Context, payload []byte, endpointConfig map[string]any) error {
	return nil
}

func truncatePayload(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Start boots the HTTP Proxy listener. The provided context controls the server lifecycle;
// when it is cancelled the server initiates a graceful shutdown.
func (p *HTTPProxy) Start(ctx context.Context, listenAddr string, matchTable dojo.MatchTable) error {
	p.matchTable = matchTable
	p.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Read the raw request body for match-table lookup
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			return
		}
		// Restore body for any downstream forwarding
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		if p.matchTable == nil {
			http.Error(w, "MatchTable not initialized", http.StatusInternalServerError)
			return
		}

		pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(pathParts) == 0 || pathParts[0] == "" {
			http.Error(w, "Invalid proxy path", http.StatusBadRequest)
			return
		}
		apiName := pathParts[0]

		reqURL := ""
		if len(pathParts) > 1 {
			reqURL = "/" + strings.Join(pathParts[1:], "/")
		}

		if p.Trace {
			p.log.Info("HTTP Request", "api", apiName, "path", reqURL, "payload", truncatePayload(string(bodyBytes), 500))
		}

		m := p.matchTable.ProcessRequest("http", apiName, bodyBytes, r.Header, reqURL)
		if m.Err != nil {
			http.Error(w, m.Err.Error(), http.StatusBadGateway)
			return
		}

		if p.Trace && m.IsMock {
			p.log.Info("HTTP Mock Response", "api", apiName, "test_id", m.MatchedID, "payload", truncatePayload(string(m.MockResponse), 500))
		}

		if m.IsMock {
			ct := m.MockContentType
			if ct == "" {
				ct = "application/json"
			}
			w.Header().Set("Content-Type", ct)
			if m.MockCode == 0 {
				m.MockCode = 200
			}
			w.WriteHeader(m.MockCode)
			if _, err := w.Write(m.MockResponse); err != nil {
				p.log.Warn("mock response write failed", "error", err)
			}
			return
		}

		if m.DestURL == "" {
			http.Error(w, "API not found in suite or missing dest URL", http.StatusNotFound)
			return
		}

		realPath := "/" + strings.Join(pathParts[1:], "/")
		if r.URL.RawQuery != "" {
			realPath += "?" + r.URL.RawQuery
		}
		target := strings.TrimRight(m.DestURL, "/") + realPath

		proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
		if err != nil {
			http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
			return
		}

		for k, vv := range r.Header {
			if strings.EqualFold(k, "Accept-Encoding") {
				continue
			}
			for _, v := range vv {
				proxyReq.Header.Add(k, v)
			}
		}
		for k, v := range m.Headers {
			proxyReq.Header.Set(k, v)
		}

		timeout := p.UpstreamTimeout
		if timeout == 0 {
			timeout = defaultUpstreamTimeout
		}
		client := &http.Client{Timeout: timeout}
		resp, err := client.Do(proxyReq)
		if err != nil {
			http.Error(w, "Failed to call external API", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		respBuf := new(bytes.Buffer)
		respTee := io.TeeReader(resp.Body, respBuf)
		if _, copyErr := io.Copy(w, respTee); copyErr != nil {
			return
		}

		if m.MatchedID != "" {
			respBytes := make([]byte, respBuf.Len())
			copy(respBytes, respBuf.Bytes())
			reqCopy := make([]byte, len(bodyBytes))
			copy(reqCopy, bodyBytes)
			matchedID := m.MatchedID
			api := apiName

			if p.Trace {
				p.log.Info("HTTP Live Response", "api", api, "test_id", matchedID, "payload", truncatePayload(string(respBytes), 500))
			}

			go p.matchTable.ProcessResponse("http", matchedID, api, reqCopy, respBytes)
		}
	})

	l, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("http proxy listen on %s: %w", listenAddr, err)
	}
	p.listener = l
	p.addr = l.Addr().String()

	p.server = &http.Server{Handler: p.mux}

	go p.server.Serve(p.listener)
	go func() {
		<-ctx.Done()
		p.server.Close()
	}()
	return nil
}

// Stop gracefully shuts down the proxy.
func (p *HTTPProxy) Stop() error {
	if p.server != nil {
		return p.server.Close()
	}
	return nil
}

// Addr returns the proxy's active listen address.
func (p *HTTPProxy) Addr() string {
	return p.addr
}
