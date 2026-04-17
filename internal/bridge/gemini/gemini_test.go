package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maniginam/waggle/internal/bridge"
)

func TestChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Query().Get("key") != "test-key" {
			t.Errorf("expected key=test-key, got %s", r.URL.Query().Get("key"))
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{
					"parts": []map[string]any{
						{"text": "Hello from Gemini"},
					},
				}},
			},
		})
	}))
	defer srv.Close()

	b := New("test-key", "gemini-2.5-pro")
	b.baseURL = srv.URL

	resp, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "Hello from Gemini" {
		t.Errorf("got %q, want %q", resp, "Hello from Gemini")
	}
}

func TestChatWithSystemPrompt(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{
					"parts": []map[string]any{{"text": "ok"}},
				}},
			},
		})
	}))
	defer srv.Close()

	b := New("key", "gemini-2.5-pro")
	b.baseURL = srv.URL

	b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{SystemPrompt: "Be helpful"})

	si, ok := receivedBody["system_instruction"]
	if !ok {
		t.Fatal("expected system_instruction in request body")
	}
	parts := si.(map[string]any)["parts"].([]any)
	text := parts[0].(map[string]any)["text"].(string)
	if text != "Be helpful" {
		t.Errorf("got system prompt %q, want %q", text, "Be helpful")
	}
}

func TestChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"message":"invalid key"}}`))
	}))
	defer srv.Close()

	b := New("bad-key", "gemini-2.5-pro")
	b.baseURL = srv.URL

	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
}

func TestChatEmptyCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"candidates": []any{}})
	}))
	defer srv.Close()

	b := New("key", "gemini-2.5-pro")
	b.baseURL = srv.URL

	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for empty candidates")
	}
}

func TestProviderAndCapabilities(t *testing.T) {
	b := New("key", "gemini-2.5-pro")
	if b.Provider() != "gemini" {
		t.Errorf("got provider %q, want %q", b.Provider(), "gemini")
	}
	caps := b.Capabilities()
	if len(caps) != 2 {
		t.Errorf("got %d capabilities, want 2", len(caps))
	}
}
