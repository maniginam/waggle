package telegram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestDigestSendsWhatsNext(t *testing.T) {
	s, api := newAPIServer(t)
	// seed one project so whats-next has content
	if _, err := s.Exec(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('p1','Demo','2026-08-12T00:00:00Z','2026-08-12T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	wg := NewWaggleClient(api.URL)
	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	d := NewDigester(wg, tg, []int64{111})

	d.SendDigest(context.Background())
	mu.Lock()
	joined := strings.Join(sink, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "sendMessage") {
		t.Fatalf("expected a digest sendMessage, got:\n%s", joined)
	}
}

func TestDigestSendsErrorFallbackWhenWhatsNextFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":"boom"}`)
	}))
	defer srv.Close()
	wg := NewWaggleClient(srv.URL)
	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	d := NewDigester(wg, tg, []int64{111})

	d.SendDigest(context.Background())
	mu.Lock()
	joined := strings.Join(sink, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "sendMessage") {
		t.Fatalf("expected a message to still be sent on error, got:\n%s", joined)
	}
	if !strings.Contains(joined, "digest error:") {
		t.Fatalf("expected fallback text \"digest error:\", got:\n%s", joined)
	}
}
