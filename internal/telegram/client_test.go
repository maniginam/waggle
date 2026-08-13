package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendMessageWithButtons(t *testing.T) {
	var gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":{"message_id":42}}`)
	}))
	defer ts.Close()

	c := NewClient(ts.URL)
	id, err := c.SendMessage(context.Background(), 111, "hello",
		[][]InlineButton{{{Text: "Done", CallbackData: "mv:t1:done"}}})
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Errorf("message id = %d", id)
	}
	if !strings.HasSuffix(gotPath, "/sendMessage") {
		t.Errorf("path = %q", gotPath)
	}
	var payload map[string]any
	json.Unmarshal([]byte(gotBody), &payload)
	if payload["chat_id"].(float64) != 111 || payload["text"] != "hello" {
		t.Errorf("bad payload: %s", gotBody)
	}
	if _, ok := payload["reply_markup"]; !ok {
		t.Errorf("expected reply_markup, got %s", gotBody)
	}
}

func TestGetUpdatesParses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true,"result":[{"update_id":5,"message":{"message_id":1,"chat":{"id":111},"text":"/next"}}]}`)
	}))
	defer ts.Close()
	c := NewClient(ts.URL)
	ups, err := c.GetUpdates(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 || ups[0].Message.Text != "/next" || ups[0].Message.Chat.ID != 111 {
		t.Fatalf("bad updates: %+v", ups)
	}
}
