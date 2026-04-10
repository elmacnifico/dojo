package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"
)

type triggerRequest struct {
	PhoneNumber string `json:"phone_number"`
	UserID      string `json:"user_id"`
	Message     string `json:"message"`
	Action      string `json:"action"`
	DisplayName string `json:"display_name"`
}

// Gemini request types.

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

type geminiRequest struct {
	Contents          []geminiContent        `json:"contents"`
	SystemInstruction geminiSystemInstruction `json:"systemInstruction"`
	GenerationConfig  geminiGenerationConfig  `json:"generationConfig"`
	SafetySettings    []geminiSafetySetting  `json:"safetySettings"`
}

// Gemini response types (for parsing the mock response).

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

// WhatsApp Business API request types.

type whatsappTextBody struct {
	Body string `json:"body"`
}

type whatsappMessage struct {
	MessagingProduct string           `json:"messaging_product"`
	RecipientType    string           `json:"recipient_type"`
	To               string           `json:"to"`
	Type             string           `json:"type"`
	Text             whatsappTextBody `json:"text"`
}

func buildGeminiRequest(userID, message string) geminiRequest {
	return geminiRequest{
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: message}}},
		},
		SystemInstruction: geminiSystemInstruction{
			Parts: []geminiPart{{Text: fmt.Sprintf(
				"You are a routing assistant. Resolve queries for user %s.", userID)}},
		},
		GenerationConfig: geminiGenerationConfig{
			Temperature:      0.7,
			TopP:             0.95,
			TopK:             40,
			MaxOutputTokens:  1024,
			ResponseMIMEType: "application/json",
		},
		SafetySettings: []geminiSafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
		},
	}
}

type askRequest struct {
	Message string `json:"message"`
}

type askResponse struct {
	Response       string          `json:"response"`
	Classification json.RawMessage `json:"classification"`
}

const intentSystemPrompt = "You are a customer message classifier for TechCorp. " +
	"Analyze the message and return JSON with fields: " +
	"intent (billing, technical, general, sales, complaint, feature_request), " +
	"priority (low, medium, high), and a one-sentence summary."

const messageSystemPrompt = "You are a customer service response writer for TechCorp. " +
	"Given the original customer message and the intent classification, " +
	"write a helpful, professional response."

func main() {
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	})
	http.HandleFunc("/not-found", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("Not Found"))
	})
	http.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		http.Redirect(w, r, "https://oauth.example.com/authorize?state="+state, http.StatusTemporaryRedirect)
	})
	http.HandleFunc("/secure", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test_secret_key" {
			w.WriteHeader(401)
			w.Write([]byte("Unauthorized"))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"authenticated"}`))
	})
	http.HandleFunc("/trigger", handleTrigger)
	http.HandleFunc("/upload", handleUpload)
	http.HandleFunc("/media-process", handleMediaProcess)
	http.HandleFunc("/ask", handleAsk)

	port := ":8080"
	fmt.Printf("[SUT] Starting server on %s\n", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		fmt.Printf("[SUT] Server crashed: %v\n", err)
		os.Exit(1)
	}
}

func handleTrigger(w http.ResponseWriter, r *http.Request) {
	var req triggerRequest
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &req); err != nil || req.PhoneNumber == "" {
		http.Error(w, "missing phone_number", 400)
		return
	}

	action := req.Action
	if action == "" {
		action = "lookup"
	}
	fmt.Printf("[SUT] action=%s phone=%s\n", action, req.PhoneNumber)

	client := &http.Client{Timeout: 5 * time.Second}

	pgURL := os.Getenv("API_POSTGRES_URL")
	db, err := sql.Open("postgres", pgURL)
	if err != nil {
		http.Error(w, fmt.Sprintf("db open: %v", err), 500)
		return
	}
	defer db.Close()

	userID := req.UserID

	switch action {
	case "lookup":
		q := fmt.Sprintf("SELECT user_id FROM users WHERE phone_number = '%s'", req.PhoneNumber)
		if err := db.QueryRow(q).Scan(&userID); err != nil {
			fmt.Printf("[SUT] SELECT failed: %v\n", err)
		}
	case "register":
		q := fmt.Sprintf("INSERT INTO users (user_id, phone_number) VALUES ('%s', '%s')", userID, req.PhoneNumber)
		if _, err := db.Exec(q); err != nil {
			fmt.Printf("[SUT] INSERT failed: %v\n", err)
		}
	case "update":
		q := fmt.Sprintf("UPDATE users SET display_name = '%s' WHERE phone_number = '%s'", req.DisplayName, req.PhoneNumber)
		if _, err := db.Exec(q); err != nil {
			fmt.Printf("[SUT] UPDATE failed: %v\n", err)
		}
	case "deactivate":
		q := fmt.Sprintf("DELETE FROM users WHERE phone_number = '%s'", req.PhoneNumber)
		if _, err := db.Exec(q); err != nil {
			fmt.Printf("[SUT] DELETE failed: %v\n", err)
		}
	}

	// Step 2: Call Gemini and read the response.
	replyText := "No response"
	geminiURL := os.Getenv("API_GEMINI_URL")
	if geminiURL != "" {
		greq := buildGeminiRequest(userID, req.Message)
		payload, err := json.Marshal(greq)
		if err == nil {
			target := geminiURL + "/v1beta/models/gemini-2.5-flash:generateContent"
			resp, err := client.Post(target, "application/json", bytes.NewReader(payload))
			if err == nil {
				respBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()

				var gr geminiResponse
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
			}
		}
	}

	// Step 3: Forward the LLM reply to the user via WhatsApp.
	whatsappURL := os.Getenv("API_WHATSAPP_URL")
	if whatsappURL != "" {
		waMsg := whatsappMessage{
			MessagingProduct: "whatsapp",
			RecipientType:    "individual",
			To:               req.PhoneNumber,
			Type:             "text",
			Text:             whatsappTextBody{Body: replyText},
		}
		payload, err := json.Marshal(waMsg)
		if err == nil {
			target := whatsappURL + "/v1/messages"
			resp, err := client.Post(target, "application/json", bytes.NewReader(payload))
			if err == nil {
				resp.Body.Close()
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write([]byte(fmt.Sprintf(`{"status":"ok","user_id":"%s"}`, userID)))
}

func handleAsk(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), 400)
		return
	}
	var req askRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Message == "" {
		http.Error(w, "missing message", 400)
		return
	}
	fmt.Printf("[SUT] /ask message=%q\n", req.Message)

	client := &http.Client{Timeout: 30 * time.Second}

	// Agent 1: Intent classification.
	intentReq := geminiRequest{
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: req.Message}}},
		},
		SystemInstruction: geminiSystemInstruction{
			Parts: []geminiPart{{Text: intentSystemPrompt}},
		},
		GenerationConfig: geminiGenerationConfig{
			Temperature:      0.2,
			TopP:             0.95,
			TopK:             40,
			MaxOutputTokens:  256,
			ResponseMIMEType: "application/json",
		},
		SafetySettings: []geminiSafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
		},
	}

	classificationJSON := json.RawMessage(`{}`)
	intentURL := os.Getenv("API_INTENT_URL")
	if intentURL != "" {
		payload, err := json.Marshal(intentReq)
		if err == nil {
			target := intentURL + "/v1beta/models/gemini-2.0-flash:generateContent"
			resp, err := client.Post(target, "application/json", bytes.NewReader(payload))
			if err == nil {
				respBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				var gr geminiResponse
				if json.Unmarshal(respBody, &gr) == nil &&
					len(gr.Candidates) > 0 &&
					len(gr.Candidates[0].Content.Parts) > 0 {
					raw := gr.Candidates[0].Content.Parts[0].Text
					if json.Valid([]byte(raw)) {
						classificationJSON = json.RawMessage(raw)
					}
				}
			} else {
				fmt.Printf("[SUT] intent call failed: %v\n", err)
			}
		}
	}
	fmt.Printf("[SUT] classification=%s\n", classificationJSON)

	// Agent 2: Message generation using classification + original message.
	msgReq := geminiRequest{
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: fmt.Sprintf(
				"Customer message: %s\n\nIntent classification: %s",
				req.Message, classificationJSON)}}},
		},
		SystemInstruction: geminiSystemInstruction{
			Parts: []geminiPart{{Text: messageSystemPrompt}},
		},
		GenerationConfig: geminiGenerationConfig{
			Temperature:      0.7,
			TopP:             0.95,
			TopK:             40,
			MaxOutputTokens:  1024,
			ResponseMIMEType: "text/plain",
		},
		SafetySettings: []geminiSafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
		},
	}

	responseText := "No response generated"
	messageURL := os.Getenv("API_MESSAGE_URL")
	if messageURL != "" {
		payload, err := json.Marshal(msgReq)
		if err == nil {
			target := messageURL + "/v1beta/models/gemini-2.0-flash:generateContent"
			resp, err := client.Post(target, "application/json", bytes.NewReader(payload))
			if err == nil {
				respBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				var gr geminiResponse
				if json.Unmarshal(respBody, &gr) == nil &&
					len(gr.Candidates) > 0 &&
					len(gr.Candidates[0].Content.Parts) > 0 {
					responseText = gr.Candidates[0].Content.Parts[0].Text
				}
			} else {
				fmt.Printf("[SUT] message call failed: %v\n", err)
			}
		}
	}

	out := askResponse{
		Response:       responseText,
		Classification: classificationJSON,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(out)
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	fmt.Printf("[SUT] /upload received %d bytes\n", len(body))

	client := &http.Client{Timeout: 5 * time.Second}

	greq := geminiRequest{
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: "Describe this uploaded image"}}},
		},
		SystemInstruction: geminiSystemInstruction{
			Parts: []geminiPart{{Text: "You are a vision assistant. Analyse uploaded images."}},
		},
		GenerationConfig: geminiGenerationConfig{
			Temperature:      0.7,
			TopP:             0.95,
			TopK:             40,
			MaxOutputTokens:  1024,
			ResponseMIMEType: "application/json",
		},
		SafetySettings: []geminiSafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
		},
	}

	description := "unknown"
	geminiURL := os.Getenv("API_GEMINI_URL")
	if geminiURL != "" {
		payload, err := json.Marshal(greq)
		if err == nil {
			target := geminiURL + "/v1beta/models/gemini-2.5-flash:generateContent"
			resp, err := client.Post(target, "application/json", bytes.NewReader(payload))
			if err == nil {
				respBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()

				var gr geminiResponse
				if json.Unmarshal(respBody, &gr) == nil &&
					len(gr.Candidates) > 0 &&
					len(gr.Candidates[0].Content.Parts) > 0 {
					var inner struct {
						Description string `json:"description"`
					}
					if json.Unmarshal([]byte(gr.Candidates[0].Content.Parts[0].Text), &inner) == nil && inner.Description != "" {
						description = inner.Description
					}
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write([]byte(fmt.Sprintf(`{"status":"ok","description":"%s"}`, description)))
}

func handleMediaProcess(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		MediaID string `json:"media_id"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.MediaID == "" {
		http.Error(w, "missing media_id", 400)
		return
	}
	fmt.Printf("[SUT] /media-process media_id=%s\n", req.MediaID)

	client := &http.Client{Timeout: 5 * time.Second}

	mediaURL := os.Getenv("API_MEDIA_URL")
	if mediaURL == "" {
		http.Error(w, "API_MEDIA_URL not set", 500)
		return
	}
	target := mediaURL + "/download/" + req.MediaID
	resp, err := client.Get(target)
	if err != nil {
		http.Error(w, fmt.Sprintf("media fetch: %v", err), 502)
		return
	}
	mediaBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Printf("[SUT] downloaded %d bytes from media API\n", len(mediaBytes))

	greq := geminiRequest{
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: fmt.Sprintf("Analyse this %d-byte media file", len(mediaBytes))}}},
		},
		SystemInstruction: geminiSystemInstruction{
			Parts: []geminiPart{{Text: "You are a vision assistant. Analyse uploaded images."}},
		},
		GenerationConfig: geminiGenerationConfig{
			Temperature:      0.7,
			TopP:             0.95,
			TopK:             40,
			MaxOutputTokens:  1024,
			ResponseMIMEType: "application/json",
		},
		SafetySettings: []geminiSafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
		},
	}

	description := "unknown"
	geminiURL := os.Getenv("API_GEMINI_URL")
	if geminiURL != "" {
		payload, err := json.Marshal(greq)
		if err == nil {
			target := geminiURL + "/v1beta/models/gemini-2.5-flash:generateContent"
			gResp, err := client.Post(target, "application/json", bytes.NewReader(payload))
			if err == nil {
				respBody, _ := io.ReadAll(gResp.Body)
				gResp.Body.Close()

				var gr geminiResponse
				if json.Unmarshal(respBody, &gr) == nil &&
					len(gr.Candidates) > 0 &&
					len(gr.Candidates[0].Content.Parts) > 0 {
					var inner struct {
						Description string `json:"description"`
					}
					if json.Unmarshal([]byte(gr.Candidates[0].Content.Parts[0].Text), &inner) == nil && inner.Description != "" {
						description = inner.Description
					}
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write([]byte(fmt.Sprintf(`{"status":"ok","description":"%s","bytes":%d}`, description, len(mediaBytes))))
}
