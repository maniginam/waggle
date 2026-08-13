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

// fakeTelegram records outbound sendMessage/editMessageText calls.
func fakeTelegram(t *testing.T, sink *[]string, mu *sync.Mutex) *Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		*sink = append(*sink, r.URL.Path+" "+string(b))
		mu.Unlock()
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	t.Cleanup(ts.Close)
	return NewClient(ts.URL)
}

func TestHandleCommandCreateAndTasks(t *testing.T) {
	_, api := newAPIServer(t)
	wg := NewWaggleClient(api.URL)
	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	h := NewHandler(tg, wg, nil)

	h.HandleCommand(context.Background(), 111, "/create ship the bot")
	tasks, _ := wg.ListTasks("")
	if len(tasks) != 1 || tasks[0]["title"] != "ship the bot" {
		t.Fatalf("task not created: %+v", tasks)
	}
	h.HandleCommand(context.Background(), 111, "/tasks")
	mu.Lock()
	joined := strings.Join(sink, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "sendMessage") || !strings.Contains(joined, "reply_markup") {
		t.Errorf("expected /tasks to send a message with buttons, got:\n%s", joined)
	}
}

type fakeSuppressor struct{ ids []string }

func (f *fakeSuppressor) Suppress(id string) { f.ids = append(f.ids, id) }

func TestHandleCallbackSuppressesBeforeMove(t *testing.T) {
	_, api := newAPIServer(t)
	wg := NewWaggleClient(api.URL)
	created, _ := wg.CreateTask("suppress-me", "")
	id := created["id"].(string)

	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	fs := &fakeSuppressor{}
	h := NewHandler(tg, wg, fs)

	h.HandleCallback(context.Background(), &CallbackQuery{
		ID:      "cb2",
		Data:    "mv:" + id + ":done",
		Message: &Message{MessageID: 9, Chat: Chat{ID: 111}},
	})

	// Suppressor must have recorded the task id.
	if len(fs.ids) != 1 || fs.ids[0] != id {
		t.Errorf("suppressor.ids = %v, want [%s]", fs.ids, id)
	}
	// Task must also have been moved.
	tasks, _ := wg.ListTasks("")
	if tasks[0]["status"] != "done" {
		t.Errorf("status = %v, want done", tasks[0]["status"])
	}
}

func TestHandleCallbackMovesTask(t *testing.T) {
	_, api := newAPIServer(t)
	wg := NewWaggleClient(api.URL)
	created, _ := wg.CreateTask("t", "")
	id := created["id"].(string)

	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	h := NewHandler(tg, wg, nil)

	h.HandleCallback(context.Background(), &CallbackQuery{
		ID:      "cb1",
		Data:    "mv:" + id + ":done",
		Message: &Message{MessageID: 7, Chat: Chat{ID: 111}},
	})
	tasks, _ := wg.ListTasks("")
	if tasks[0]["status"] != "done" {
		t.Errorf("status = %v", tasks[0]["status"])
	}
	mu.Lock()
	joined := strings.Join(sink, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "answerCallbackQuery") {
		t.Errorf("expected answerCallbackQuery, got:\n%s", joined)
	}
}
