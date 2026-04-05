package proxy

import (
	"bytes"
	"context"
	"dojo/pkg/dojo"
	"fmt"
	"net/http"
)

// HTTPInitiator triggers the SUT via HTTP.
type HTTPInitiator struct {
}

// NewHTTPInitiator initializes an initiator.
func NewHTTPInitiator() *HTTPInitiator {
	return &HTTPInitiator{}
}

// Listen is a no-op for HTTPInitiator because it is an active trigger.
func (i *HTTPInitiator) Listen(ctx context.Context, matchTable dojo.MatchTable) error {
	return nil
}
func (i *HTTPInitiator) Trigger(ctx context.Context, payload []byte, endpointConfig map[string]any) error {
	url, ok := endpointConfig["url"].(string)
	if !ok || url == "" {
		url = "http://127.0.0.1:8080"
	}
	path, ok := endpointConfig["path"].(string)
	if !ok {
		path = "/"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("building trigger request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if headers, ok := endpointConfig["headers"].(map[string]string); ok {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("trigger request to %s%s: %w", url, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("entrypoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}
