package proxy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dojo/internal/proxy"
)

func TestHTTPInitiator_Trigger(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		status     int
		wantErr    bool
		errContain string
	}{
		{"success 200", 200, false, ""},
		{"success 201", 201, false, ""},
		{"error 400", 400, true, "HTTP 400"},
		{"error 500", 500, true, "HTTP 500"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			init := proxy.NewHTTPInitiator()
			cfg := map[string]any{"url": srv.URL, "path": "/hook"}
			err := init.Trigger(context.Background(), []byte(`{"test":true}`), cfg)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tc.errContain) {
					t.Fatalf("error %q should contain %q", err.Error(), tc.errContain)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestHTTPInitiator_Trigger_Headers(t *testing.T) {
	t.Parallel()

	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Custom")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	init := proxy.NewHTTPInitiator()
	cfg := map[string]any{
		"url":     srv.URL,
		"path":    "/",
		"headers": map[string]string{"X-Custom": "test-value"},
	}
	if err := init.Trigger(context.Background(), []byte("{}"), cfg); err != nil {
		t.Fatal(err)
	}
	if gotHeader != "test-value" {
		t.Errorf("expected X-Custom header 'test-value', got %q", gotHeader)
	}
}

func TestHTTPInitiator_Trigger_DefaultPath(t *testing.T) {
	t.Parallel()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(200)
	}))
	defer srv.Close()

	init := proxy.NewHTTPInitiator()
	// No "path" key in config
	cfg := map[string]any{"url": srv.URL}
	if err := init.Trigger(context.Background(), []byte("{}"), cfg); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/" {
		t.Errorf("expected path '/', got %q", gotPath)
	}
}

func TestHTTPInitiator_Trigger_DefaultURL(t *testing.T) {
	t.Parallel()
	init := proxy.NewHTTPInitiator()
	// No URL → defaults to http://127.0.0.1:8080 which should fail to connect
	err := init.Trigger(context.Background(), []byte("{}"), map[string]any{})
	if err == nil {
		t.Fatal("expected connection error to default URL")
	}
}

func TestHTTPInitiator_Listen_Noop(t *testing.T) {
	t.Parallel()
	init := proxy.NewHTTPInitiator()
	if err := init.Listen(context.Background(), nil); err != nil {
		t.Fatalf("Listen should be no-op, got: %v", err)
	}
}
