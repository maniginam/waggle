package telegram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniginam/waggle/internal/event"
)

func TestBotRunProcessesOneUpdateThenStops(t *testing.T) {
	_, api := newAPIServer(t)

	var calls int32
	var mu sync.Mutex
	var sent []string
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			if atomic.AddInt32(&calls, 1) == 1 {
				io.WriteString(w, `{"ok":true,"result":[{"update_id":1,"message":{"message_id":1,"chat":{"id":111},"text":"/next"}}]}`)
				return
			}
			io.WriteString(w, `{"ok":true,"result":[]}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		sent = append(sent, r.URL.Path+" "+string(b))
		mu.Unlock()
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	defer tgSrv.Close()

	hub := event.NewHub()
	cfg := Config{AllowedChats: []int64{111}, APIBaseURL: api.URL, TelegramBaseURL: tgSrv.URL}
	bot := New(hub, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	bot.Run(ctx)

	mu.Lock()
	joined := strings.Join(sent, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "sendMessage") {
		t.Fatalf("expected /next to trigger a sendMessage, got:\n%s", joined)
	}
}
