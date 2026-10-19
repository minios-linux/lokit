package translate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultProvidersCanonicalizesLegacyCopilotID(t *testing.T) {
	providers := DefaultProviders()

	canonical := providers[ProviderGitHubCopilot]
	if canonical.ID != ProviderGitHubCopilot {
		t.Fatalf("canonical provider ID = %q, want %q", canonical.ID, ProviderGitHubCopilot)
	}
	legacy := providers["copilot"]
	if legacy.ID != ProviderGitHubCopilot {
		t.Fatalf("legacy provider ID = %q, want %q", legacy.ID, ProviderGitHubCopilot)
	}
}

func TestOpenCodeGoUsesDocumentedProtocols(t *testing.T) {
	for _, tc := range []struct {
		model, path, keyHeader, response string
	}{
		{"glm-5.3-flash", "/openai/v1/chat/completions", "Authorization", `{"choices":[{"message":{"content":"Hallo"}}]}`},
		{"gpt-6-luna", "/openai/v1/responses", "Authorization", `{"output":[{"type":"message","content":[{"type":"output_text","text":"Hallo"}]}]}`},
		{"qwen3.8-max", "/anthropic/v1/messages", "x-api-key", `{"content":[{"type":"text","text":"Hallo"}]}`},
		{"gemini-4-argon-contributor", "/google/v1beta/models/gemini-4-argon-contributor:generateContent", "x-goog-api-key", `{"candidates":[{"content":{"parts":[{"text":"Hallo"}]}}]}`},
	} {
		t.Run(tc.model, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path || r.Method != http.MethodPost {
					t.Errorf("request route = %s %s, want POST %s", r.Method, r.URL.Path, tc.path)
				}
				if r.Header.Get("x-opencode-session") == "" || r.Header.Get("User-Agent") != "lokit/1.0" {
					t.Errorf("missing Go session or client identity: %v", r.Header)
				}
				if got := r.Header.Get(tc.keyHeader); got != "go-key" && got != "Bearer go-key" {
					t.Errorf("%s = %q", tc.keyHeader, got)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["model"] != tc.model && tc.model != "gemini-4-argon-contributor" {
					t.Errorf("invalid model request: %v, %v", body, err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			result, err := callOpenCodeGo(context.Background(), Provider{
				ID: ProviderOpenCodeGo, Model: tc.model, BaseURL: server.URL, APIKey: "go-key",
			}, "system", "user", nil, 0, false)
			if err != nil || result != "Hallo" {
				t.Fatalf("translation = %q, %v", result, err)
			}
		})
	}
}

func TestOpenCodeGoDoesNotGuessModelsOrReuseConsoleKey(t *testing.T) {
	for _, tc := range []struct{ model, key, want string }{
		{"glm-5.3-flash", "", "API key"},
		{"qwen3.8-unknown", "go-key", "no known API format"},
	} {
		_, err := callOpenCodeGo(context.Background(), Provider{ID: ProviderOpenCodeGo, Model: tc.model, APIKey: tc.key}, "", "", nil, 0, false)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("model %q: error = %v, want %q", tc.model, err, tc.want)
		}
	}
}

func TestOpenCodeGoKeepsSessionAcrossRetries(t *testing.T) {
	attempts := 0
	sessionID := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		id := r.Header.Get("x-opencode-session")
		if id == "" || sessionID != "" && sessionID != id {
			t.Errorf("session changed during retry: %q -> %q", sessionID, id)
		}
		sessionID = id
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Hallo"}}]}`))
	}))
	defer server.Close()
	result, err := callOpenCodeGo(context.Background(), Provider{
		ID: ProviderOpenCodeGo, Model: "glm-5.3-flash", BaseURL: server.URL, APIKey: "go-key",
	}, "system", "user", nil, 1, false)
	if err != nil || result != "Hallo" || attempts != 2 {
		t.Fatalf("retry result = %q, attempts = %d, error = %v", result, attempts, err)
	}
}

func TestOpenCodeGoKeepsSessionAcrossValidationRetries(t *testing.T) {
	keys := []string{"greeting"}
	attempts := 0
	var sessions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		sessions = append(sessions, r.Header.Get("x-opencode-session"))
		var body struct {
			Messages []providerMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if attempts%2 == 1 {
			// HTTP succeeds, but the translation cannot be parsed as JSON.
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"invalid JSON"}}]}`))
			return
		}
		if len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, "Your previous response was rejected") {
			t.Errorf("validation retry is missing correction feedback: %+v", body.Messages)
		}
		_, _ = w.Write([]byte(identifiedKVProviderResponse(keys, []string{"Hallo"})))
	}))
	defer server.Close()
	opts := Options{
		Provider:   Provider{ID: ProviderOpenCodeGo, Model: "glm-5.3-flash", BaseURL: server.URL, APIKey: "go-key"},
		MaxRetries: 1,
	}
	for batch := 0; batch < 2; batch++ {
		translations, err := translateKVChunk(context.Background(), keys, map[string]string{"greeting": "Hello"}, "system", opts, DefaultKVChunkTranslator(), nil)
		if err != nil || len(translations) != 1 || translations[0] != "Hallo" {
			t.Fatalf("batch %d: translations = %v, error = %v", batch, translations, err)
		}
	}
	if attempts != 4 {
		t.Fatalf("request count = %d, want 4", attempts)
	}
	for i := 0; i < len(sessions); i += 2 {
		if sessions[i] == "" || sessions[i] != sessions[i+1] {
			t.Errorf("session changed during validation retry: %q -> %q", sessions[i], sessions[i+1])
		}
	}
	if sessions[0] == sessions[2] {
		t.Errorf("independent batches reused session %q", sessions[0])
	}
}
