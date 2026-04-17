package claude

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
		if r.URL.Path != "/v1/messages" {
			t.Errorf("expected /v1/messages, got %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("expected x-api-key test-key, got %s", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("expected anthropic-version 2023-06-01, got %s", r.Header.Get("anthropic-version"))
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "claude-sonnet-4-6" {
			t.Errorf("expected model claude-sonnet-4-6, got %v", body["model"])
		}

		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Hello from Claude"},
			},
		})
	}))
	defer srv.Close()

	b := New("test-key", "claude-sonnet-4-6")
	b.baseURL = srv.URL

	resp, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "Hello from Claude" {
		t.Errorf("got %q, want %q", resp, "Hello from Claude")
	}
}

func TestChatWithSystemPrompt(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "ok"}},
		})
	}))
	defer srv.Close()

	b := New("key", "claude-sonnet-4-6")
	b.baseURL = srv.URL

	b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{SystemPrompt: "Be helpful"})

	system, ok := receivedBody["system"].(string)
	if !ok || system != "Be helpful" {
		t.Errorf("expected system 'Be helpful', got %v", receivedBody["system"])
	}
}

func TestChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid key"}}`))
	}))
	defer srv.Close()

	b := New("bad-key", "claude-sonnet-4-6")
	b.baseURL = srv.URL

	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestChatEmptyContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"content": []any{}})
	}))
	defer srv.Close()

	b := New("key", "claude-sonnet-4-6")
	b.baseURL = srv.URL

	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestProviderAndCapabilities(t *testing.T) {
	b := New("key", "claude-sonnet-4-6")
	if b.Provider() != "claude-api" {
		t.Errorf("got provider %q, want %q", b.Provider(), "claude-api")
	}
	caps := b.Capabilities()
	if len(caps) != 2 {
		t.Errorf("got %d capabilities, want 2", len(caps))
	}
}
