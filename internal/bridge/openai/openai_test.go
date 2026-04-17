package openai

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
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %s", r.Header.Get("Authorization"))
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "gpt-4o" {
			t.Errorf("expected model gpt-4o, got %v", body["model"])
		}

		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "Hello from GPT"}},
			},
		})
	}))
	defer srv.Close()

	b := New(srv.URL, "test-key", "gpt-4o", []bridge.Capability{bridge.CapChat, bridge.CapCode})

	resp, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "Hello from GPT" {
		t.Errorf("got %q, want %q", resp, "Hello from GPT")
	}
}

func TestChatWithSystemPrompt(t *testing.T) {
	var receivedMessages []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		msgs, _ := body["messages"].([]any)
		for _, m := range msgs {
			receivedMessages = append(receivedMessages, m.(map[string]any))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "ok"}},
			},
		})
	}))
	defer srv.Close()

	b := New(srv.URL, "key", "gpt-4o", nil)
	b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{SystemPrompt: "You are helpful"})

	if len(receivedMessages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(receivedMessages))
	}
	if receivedMessages[0]["role"] != "system" {
		t.Errorf("first message should be system, got %s", receivedMessages[0]["role"])
	}
}

func TestChatModelOverride(t *testing.T) {
	var receivedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		receivedModel, _ = body["model"].(string)
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "ok"}},
			},
		})
	}))
	defer srv.Close()

	b := New(srv.URL, "key", "gpt-4o", nil)
	b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{Model: "o3"})

	if receivedModel != "o3" {
		t.Errorf("expected model override 'o3', got %q", receivedModel)
	}
}

func TestChatNoAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("expected no auth header for keyless provider")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "local response"}},
			},
		})
	}))
	defer srv.Close()

	b := New(srv.URL, "", "llama3", nil)
	resp, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "local response" {
		t.Errorf("got %q, want %q", resp, "local response")
	}
}

func TestChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "rate limited"},
		})
	}))
	defer srv.Close()

	b := New(srv.URL, "key", "gpt-4o", nil)
	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for 429 response")
	}
}

func TestChatEmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	}))
	defer srv.Close()

	b := New(srv.URL, "key", "gpt-4o", nil)
	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestProviderAndCapabilities(t *testing.T) {
	b := New("http://localhost", "key", "gpt-4o", []bridge.Capability{bridge.CapChat, bridge.CapVision})
	if b.Provider() != "openai" {
		t.Errorf("got provider %q, want %q", b.Provider(), "openai")
	}
	if len(b.Capabilities()) != 2 {
		t.Errorf("got %d capabilities, want 2", len(b.Capabilities()))
	}
}
