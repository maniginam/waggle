package bedrock

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

		auth := r.Header.Get("Authorization")
		if auth == "" {
			t.Error("expected Authorization header with Sigv4 signature")
		}

		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Hello from Bedrock"},
			},
		})
	}))
	defer srv.Close()

	b := New("us-east-1", "AKID", "SECRET", "claude-sonnet-4-6")
	b.endpointURL = srv.URL

	resp, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "Hello from Bedrock" {
		t.Errorf("got %q, want %q", resp, "Hello from Bedrock")
	}
}

func TestChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"access denied"}`))
	}))
	defer srv.Close()

	b := New("us-east-1", "AKID", "SECRET", "claude-sonnet-4-6")
	b.endpointURL = srv.URL

	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
}

func TestChatEmptyContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"content": []any{}})
	}))
	defer srv.Close()

	b := New("us-east-1", "AKID", "SECRET", "claude-sonnet-4-6")
	b.endpointURL = srv.URL

	_, err := b.Chat(context.Background(), []bridge.Message{
		{Role: "user", Content: "hi"},
	}, bridge.ChatOpts{})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestProviderAndCapabilities(t *testing.T) {
	b := New("us-east-1", "AKID", "SECRET", "claude-sonnet-4-6")
	if b.Provider() != "bedrock" {
		t.Errorf("got provider %q, want %q", b.Provider(), "bedrock")
	}
	caps := b.Capabilities()
	if len(caps) != 2 {
		t.Errorf("got %d capabilities, want 2", len(caps))
	}
}
