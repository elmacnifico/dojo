package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func firstPerformLine(t *testing.T, plan string) ParsedLine {
	t.Helper()
	doc, err := ParsePlan(plan)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Lines) == 0 {
		t.Fatal("no lines")
	}
	return doc.Lines[0]
}

func TestResolveHTTPPerform_NamedEntrypointImplicitJSON(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "webhook.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{
			"webhook": {Type: "http", Path: "/hook"},
		},
	}
	test := &Test{}
	ln := firstPerformLine(t, "Perform -> entrypoints/webhook")
	ep, payload, status, err := ResolveHTTPPerform(ln, test, suite, tmp, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Path != "/hook" {
		t.Fatalf("path %q", ep.Path)
	}
	if string(payload) != `{"a":1}` {
		t.Fatalf("payload %q", payload)
	}
	if status != 0 {
		t.Fatalf("status %d", status)
	}
}

func TestResolveHTTPPerform_PayloadFileAndStatusAndHeader(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "in.json"), []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{
			"w": {Type: "http", Path: "/p"},
		},
	}
	test := &Test{}
	st := "418"
	ln := ParsedLine{
		Action: "Perform",
		Target: "entrypoints/w",
		Clauses: []ParsedClause{
			{Key: "Payload", Value: ptr("in.json")},
			{Key: "Status", Value: &st},
			{Key: "Header", Value: ptr("X-Test: yes")},
		},
	}
	ep, payload, status, err := ResolveHTTPPerform(ln, test, suite, tmp, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if status != 418 {
		t.Fatalf("status %d", status)
	}
	if ep.Headers["X-Test"] != "yes" {
		t.Fatalf("headers %#v", ep.Headers)
	}
	if string(payload) != `{"x":1}` {
		t.Fatalf("payload %q", payload)
	}
}

func TestResolveHTTPPerform_WithMerge(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "webhook.json"), []byte(`{"a":1,"b":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{
			"webhook": {Type: "http", Path: "/"},
		},
	}
	test := &Test{}
	ln := ParsedLine{
		Action: "Perform",
		Target: "entrypoints/webhook",
		Clauses: []ParsedClause{
			{Key: "With", Value: ptr(`{"a":9}`)},
		},
	}
	ep, payload, _, err := ResolveHTTPPerform(ln, test, suite, tmp, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Path != "/" {
		t.Fatalf("path %q", ep.Path)
	}
	ps := string(payload)
	if !strings.Contains(ps, `"a":9`) || !strings.Contains(ps, `"b":2`) {
		t.Fatalf("merged payload %q", payload)
	}
}

func TestResolveHTTPPerform_TestEntrypointOverride(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "webhook.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{
			"webhook": {Type: "http", Path: "/suite"},
		},
	}
	test := &Test{
		Entrypoints: map[string]EntrypointConfig{
			"webhook": {Type: "http", Path: "/test"},
		},
	}
	ln := firstPerformLine(t, "Perform -> entrypoints/webhook")
	ep, _, _, err := ResolveHTTPPerform(ln, test, suite, tmp, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Path != "/test" {
		t.Fatalf("got %q want /test", ep.Path)
	}
}

func TestResolveHTTPPerform_InlineMethod(t *testing.T) {
	t.Parallel()
	suite := &Suite{}
	test := &Test{}
	ln := firstPerformLine(t, "Perform -> GET /health")
	ep, _, _, err := ResolveHTTPPerform(ln, test, suite, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ep.Method != "GET" || ep.Path != "/health" {
		t.Fatalf("ep=%+v", ep)
	}
}

func TestResolveHTTPPerform_InlineOPTIONS(t *testing.T) {
	t.Parallel()
	ln := firstPerformLine(t, "Perform -> OPTIONS /cors")
	ep, _, _, err := ResolveHTTPPerform(ln, &Test{}, &Suite{}, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ep.Method != "OPTIONS" {
		t.Fatalf("method %q", ep.Method)
	}
}

func TestResolveHTTPPerform_HeaderClauseWithoutColonValueIgnored(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "x.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{"w": {Type: "http", Path: "/"}},
	}
	ln := ParsedLine{
		Action: "Perform",
		Target: "entrypoints/w",
		Clauses: []ParsedClause{
			{Key: "Payload", Value: ptr("x.json")},
			{Key: "Header", Value: ptr("NoColon")},
		},
	}
	ep, _, _, err := ResolveHTTPPerform(ln, &Test{}, suite, tmp, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(ep.Headers) != 0 {
		t.Fatalf("headers %#v", ep.Headers)
	}
}

func TestResolveHTTPPerform_MissingPayloadFile(t *testing.T) {
	t.Parallel()
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{"w": {Type: "http", Path: "/"}},
	}
	test := &Test{}
	ln := firstPerformLine(t, "Perform -> entrypoints/w -> Payload: missing.json")
	_, _, _, err := ResolveHTTPPerform(ln, test, suite, t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveHTTPPerform_StatusClauseWithoutValue(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "x.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{"w": {Type: "http", Path: "/"}},
	}
	ln := ParsedLine{
		Action: "Perform",
		Target: "entrypoints/w",
		Clauses: []ParsedClause{
			{Key: "Payload", Value: ptr("x.json")},
			{Key: "Status", Value: nil},
		},
	}
	_, _, status, err := ResolveHTTPPerform(ln, &Test{}, suite, tmp, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if status != 0 {
		t.Fatalf("status %d", status)
	}
}

func TestResolveHTTPPerform_InvalidStatus(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "x.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{"w": {Type: "http", Path: "/"}},
	}
	test := &Test{}
	ln := firstPerformLine(t, "Perform -> entrypoints/w -> Payload: x.json -> Status: nan")
	_, _, _, err := ResolveHTTPPerform(ln, test, suite, tmp, tmp)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveHTTPPerform_WithWithoutBaseFile(t *testing.T) {
	t.Parallel()
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{"webhook": {Type: "http", Path: "/"}},
	}
	test := &Test{}
	ln := ParsedLine{
		Action: "Perform",
		Target: "entrypoints/webhook",
		Clauses: []ParsedClause{
			{Key: "With", Value: ptr(`{"a":1}`)},
		},
	}
	_, _, _, err := ResolveHTTPPerform(ln, test, suite, t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveHTTPPerform_WithNonObjectOverlay_ReplacesPerDeepMerge(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "webhook.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{"webhook": {Type: "http", Path: "/"}},
	}
	test := &Test{}
	ln := ParsedLine{
		Action: "Perform",
		Target: "entrypoints/webhook",
		Clauses: []ParsedClause{
			{Key: "With", Value: ptr("not-json")},
		},
	}
	_, payload, _, err := ResolveHTTPPerform(ln, test, suite, tmp, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "not-json" {
		t.Fatalf("got %q", payload)
	}
}

func TestResolveHTTPPerform_WithClauseWithoutValue(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "webhook.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{"webhook": {Type: "http", Path: "/"}},
	}
	ln := ParsedLine{
		Action: "Perform",
		Target: "entrypoints/webhook",
		Clauses: []ParsedClause{
			{Key: "With", Value: nil},
		},
	}
	_, _, _, err := ResolveHTTPPerform(ln, &Test{}, suite, tmp, tmp)
	if err != nil {
		t.Fatal(err)
	}
}

func TestResolveHTTPPerform_PayloadClauseWithoutValue(t *testing.T) {
	t.Parallel()
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{"w": {Type: "http", Path: "/"}},
	}
	ln := ParsedLine{
		Action: "Perform",
		Target: "entrypoints/w",
		Clauses: []ParsedClause{
			{Key: "Payload", Value: nil},
		},
	}
	_, payload, _, err := ResolveHTTPPerform(ln, &Test{}, suite, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != 0 {
		t.Fatalf("expected empty payload, got %q", payload)
	}
}

func TestResolveHTTPPerform_InlinePayloadLiteral(t *testing.T) {
	t.Parallel()
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{"w": {Type: "http", Path: "/"}},
	}
	test := &Test{}
	ln := ParsedLine{
		Action: "Perform",
		Target: "entrypoints/w",
		Clauses: []ParsedClause{
			{Key: "Payload", Value: ptr("plain-body")},
		},
	}
	_, payload, _, err := ResolveHTTPPerform(ln, test, suite, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "plain-body" {
		t.Fatalf("got %q", payload)
	}
}
