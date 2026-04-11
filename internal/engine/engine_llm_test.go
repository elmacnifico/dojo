package engine_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"dojo/internal/engine"
	"dojo/internal/testutil"
	"dojo/internal/workspace"
)

// Gemini request types — mirrors the real SUT.

type geminiPart struct {
	Text string `json:"text"`
}
type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}
type geminiSystemInstruction struct {
	Parts []geminiPart `json:"parts"`
}
type geminiGenerationConfig struct {
	Temperature      float64 `json:"temperature"`
	TopP             float64 `json:"topP"`
	TopK             int     `json:"topK"`
	MaxOutputTokens  int     `json:"maxOutputTokens"`
	ResponseMIMEType string  `json:"responseMimeType"`
}
type geminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}
type geminiReq struct {
	Contents          []geminiContent        `json:"contents"`
	SystemInstruction geminiSystemInstruction `json:"systemInstruction"`
	GenerationConfig  geminiGenerationConfig  `json:"generationConfig"`
	SafetySettings    []geminiSafetySetting  `json:"safetySettings"`
}

func buildGeminiRequest(userID, message string) []byte {
	r := geminiReq{
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: message}}},
		},
		SystemInstruction: geminiSystemInstruction{
			Parts: []geminiPart{{Text: fmt.Sprintf(
				"You are a routing assistant. Resolve queries for user %s.", userID)}},
		},
		GenerationConfig: geminiGenerationConfig{
			Temperature: 0.7, TopP: 0.95, TopK: 40,
			MaxOutputTokens: 1024, ResponseMIMEType: "application/json",
		},
		SafetySettings: []geminiSafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
		},
	}
	b, _ := json.Marshal(r)
	return b
}

// WhatsApp request type — mirrors the real SUT.

type whatsappTextBody struct {
	Body string `json:"body"`
}
type whatsappMsg struct {
	MessagingProduct string           `json:"messaging_product"`
	RecipientType    string           `json:"recipient_type"`
	To               string           `json:"to"`
	Type             string           `json:"type"`
	Text             whatsappTextBody `json:"text"`
}

func buildWhatsAppRequest(phone, reply string) []byte {
	m := whatsappMsg{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               phone,
		Type:             "text",
		Text:             whatsappTextBody{Body: reply},
	}
	b, _ := json.Marshal(m)
	return b
}

// TestCrossIDCorrelation exercises four tests running sequentially, each
// triggering a different SQL command (SELECT, INSERT, UPDATE, DELETE) through
// a live Postgres container. The SUT calls Gemini, parses the reply, then
// forwards it via WhatsApp.
//
// Routing uses normalized full SQL / JSON equality on expected_request fixtures;
// suite load rejects duplicate expectations across tests. Fixtures use naming
// convention (<api>_request.json, <api>_response.json) plus suite-level merges.
func TestCrossIDCorrelation(t *testing.T) {
	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		t.Skip("docker not available (required for testcontainers)")
	}

	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(10*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	defer func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Fatalf("terminate postgres container: %v", err)
		}
	}()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	var httpProxyAddr, pgProxyAddr string

	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trigger" {
			http.NotFound(w, r)
			return
		}

		body, _ := io.ReadAll(r.Body)
		var req struct {
			PhoneNumber string `json:"phone_number"`
			UserID      string `json:"user_id"`
			Message     string `json:"message"`
			Action      string `json:"action"`
			DisplayName string `json:"display_name"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		action := req.Action
		if action == "" {
			action = "lookup"
		}

		pgURL := "postgres://postgres:postgres@" + pgProxyAddr + "/postgres?sslmode=disable"
		db, err := sql.Open("postgres", pgURL)
		if err != nil {
			http.Error(w, fmt.Sprintf("pg open: %v", err), http.StatusInternalServerError)
			return
		}
		defer db.Close()

		userID := req.UserID

		switch action {
		case "lookup":
			q := fmt.Sprintf("SELECT user_id FROM users WHERE phone_number = '%s'", req.PhoneNumber)
			if err := db.QueryRow(q).Scan(&userID); err != nil {
				http.Error(w, fmt.Sprintf("pg select: %v", err), http.StatusInternalServerError)
				return
			}
		case "register":
			q := fmt.Sprintf("INSERT INTO users (user_id, phone_number) VALUES ('%s', '%s')", userID, req.PhoneNumber)
			if _, err := db.Exec(q); err != nil {
				http.Error(w, fmt.Sprintf("pg insert: %v", err), http.StatusInternalServerError)
				return
			}
		case "update":
			q := fmt.Sprintf("UPDATE users SET display_name = '%s' WHERE phone_number = '%s'", req.DisplayName, req.PhoneNumber)
			if _, err := db.Exec(q); err != nil {
				http.Error(w, fmt.Sprintf("pg update: %v", err), http.StatusInternalServerError)
				return
			}
		case "deactivate":
			q := fmt.Sprintf("DELETE FROM users WHERE phone_number = '%s'", req.PhoneNumber)
			if _, err := db.Exec(q); err != nil {
				http.Error(w, fmt.Sprintf("pg delete: %v", err), http.StatusInternalServerError)
				return
			}
		}

		replyText := "No response"
		geminiURL := "http://" + httpProxyAddr + "/gemini/v1beta/models/gemini-2.5-flash:generateContent"
		payload := buildGeminiRequest(userID, req.Message)

		resp, err := http.Post(geminiURL, "application/json", bytes.NewReader(payload))
		if err != nil {
			http.Error(w, fmt.Sprintf("gemini call: %v", err), http.StatusBadGateway)
			return
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var gr struct {
			Candidates []struct {
				Content struct {
					Parts []geminiPart `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if json.Unmarshal(respBody, &gr) == nil &&
			len(gr.Candidates) > 0 &&
			len(gr.Candidates[0].Content.Parts) > 0 {
			var inner struct {
				Reply string `json:"reply"`
			}
			if json.Unmarshal([]byte(gr.Candidates[0].Content.Parts[0].Text), &inner) == nil && inner.Reply != "" {
				replyText = inner.Reply
			}
		}

		waURL := "http://" + httpProxyAddr + "/whatsapp/v1/messages"
		waPayload := buildWhatsAppRequest(req.PhoneNumber, replyText)
		waResp, err := http.Post(waURL, "application/json", bytes.NewReader(waPayload))
		if err == nil {
			io.Copy(io.Discard, waResp.Body)
			waResp.Body.Close()
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","user_id":"%s"}`, userID)
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	// Suite-level configs — routing is by normalized expected_request fixtures per test.
	testutil.CreateFile(t, tmpDir, "suite/dojo.config", `{"concurrency":1}`)

	testutil.CreateFile(t, tmpDir, "suite/apis/postgres.json", `{
		"mode": "live",
		"protocol": "postgres",
		"url": "`+connStr+`"
	}`)

	testutil.CreateFile(t, tmpDir, "suite/apis/gemini.json", `{
		"mode": "mock",
		"url": "/v1beta/models/gemini-2.5-flash:generateContent"
	}`)

	testutil.CreateFile(t, tmpDir, "suite/apis/whatsapp.json", `{
		"mode": "mock",
		"url": "/v1/messages",
		"default_response": {"code": 200, "body": "{\"messaging_product\":\"whatsapp\",\"messages\":[{\"id\":\"wamid.test\"}]}"}
	}`)

	testutil.CreateFile(t, tmpDir, "suite/entrypoints/webhook.json", `{
		"type": "http",
		"path": "/trigger",
		"url": "`+sutServer.URL+`"
	}`)

	testutil.CreateFile(t, tmpDir, "suite/seed/schema.sql",
		"CREATE TABLE IF NOT EXISTS users (user_id TEXT, phone_number TEXT, display_name TEXT);")

	geminiResp := func(userID, reply string) string {
		return fmt.Sprintf(`{
  "candidates": [{"content": {"parts": [{"text": "{\"reply\":\"%s\",\"user_id\":\"%s\"}"}], "role": "model"}, "finishReason": "STOP"}],
  "usageMetadata": {"promptTokenCount":10,"candidatesTokenCount":10,"totalTokenCount":20},
  "modelVersion": "gemini-2.5-flash-001"
}`, reply, userID)
	}

	tests := []struct {
		id, phone, userID, message, action, displayName string
		pgSQL, reply                                    string
		needsSeed                                       bool
	}{
		{"test_user_lookup", "+1234567890", "usr_42", "Hello", "", "", "SELECT user_id FROM users WHERE phone_number = '+1234567890'", "Hello from LLM", true},
		{"test_user_register", "+0987654321", "usr_99", "Register me", "register", "", "INSERT INTO users (user_id, phone_number) VALUES ('usr_99', '+0987654321')", "Welcome aboard", false},
		{"test_user_update", "+1112223333", "usr_77", "Update my profile", "update", "Jane", "UPDATE users SET display_name = 'Jane' WHERE phone_number = '+1112223333'", "Profile saved", true},
		{"test_user_deactivate", "+4445556666", "usr_55", "Delete my account", "deactivate", "", "DELETE FROM users WHERE phone_number = '+4445556666'", "Account removed", true},
	}

	for _, tc := range tests {
		incoming := fmt.Sprintf(`{"phone_number":"%s"`, tc.phone)
		if tc.userID != "" && tc.action != "" {
			incoming += fmt.Sprintf(`,"user_id":"%s"`, tc.userID)
		}
		incoming += fmt.Sprintf(`,"message":"%s"`, tc.message)
		if tc.action != "" {
			incoming += fmt.Sprintf(`,"action":"%s"`, tc.action)
		}
		if tc.displayName != "" {
			incoming += fmt.Sprintf(`,"display_name":"%s"`, tc.displayName)
		}
		incoming += "}"

		testutil.CreateFile(t, tmpDir, "suite/"+tc.id+"/test.plan", `
Perform -> entrypoints/webhook -> Payload: incoming.json

Expect -> postgres -> Request: postgres_request.sql
Expect -> gemini -> Request: gemini_request.json -> Respond: gemini_response.json
Expect -> whatsapp -> Request: whatsapp_request.json
`)

		testutil.CreateFile(t, tmpDir, "suite/"+tc.id+"/incoming.json", incoming)

		// Convention-named fixture files — auto-discovered by the workspace loader.
		testutil.CreateFile(t, tmpDir, "suite/"+tc.id+"/postgres_request.sql", tc.pgSQL)
		testutil.CreateFile(t, tmpDir, "suite/"+tc.id+"/gemini_request.json", string(buildGeminiRequest(tc.userID, tc.message)))
		testutil.CreateFile(t, tmpDir, "suite/"+tc.id+"/gemini_response.json", geminiResp(tc.userID, tc.reply))
		testutil.CreateFile(t, tmpDir, "suite/"+tc.id+"/whatsapp_request.json", string(buildWhatsAppRequest(tc.phone, tc.reply)))

		if tc.needsSeed {
			seed := fmt.Sprintf("INSERT INTO users (user_id, phone_number) VALUES ('%s', '%s');", tc.userID, tc.phone)
			if tc.displayName != "" {
				seed = fmt.Sprintf("INSERT INTO users (user_id, phone_number, display_name) VALUES ('%s', '%s', 'OldName');", tc.userID, tc.phone)
			}
			testutil.CreateFile(t, tmpDir, "suite/"+tc.id+"/seed/seed.sql", seed)
		}
	}

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}

	// Verify auto-discovery: tests should have API configs despite having no apis/ dirs.
	for _, tc := range tests {
		test := ws.Suites["suite"].Tests[tc.id]
		for _, apiName := range []string{"postgres", "gemini", "whatsapp"} {
			if _, ok := test.APIs[apiName]; !ok {
				t.Errorf("%s: expected auto-discovered API config for %s", tc.id, apiName)
			}
		}
	}

	eng := engine.NewEngine(ws)


	if _, err := eng.StartProxies(ctx, "suite"); err != nil {
		t.Fatalf("start proxies: %v", err)
	}
	defer eng.StopProxies()

	httpProxyAddr = eng.HTTPProxy.Addr()
	pgProxyAddr = eng.PostgresProxy.Addr()

	suiteCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	summary, err := eng.RunSuite(suiteCtx, "suite", nil)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}

	if summary.TotalRuns != 4 {
		t.Errorf("want 4 total runs, got %d", summary.TotalRuns)
	}
	if summary.Passed != 4 {
		t.Errorf("want 4 passed, got %d", summary.Passed)
	}
	if summary.Failed != 0 {
		for _, f := range summary.Failures {
			t.Logf("failure: %s — %s", f.TestName, f.Reason)
		}
		t.Fatalf("want 0 failed, got %d", summary.Failed)
	}
}
