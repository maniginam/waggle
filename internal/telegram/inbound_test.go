package telegram

import (
	"context"
	"sync"
	"testing"
)

func TestDispatchIgnoresDisallowedChat(t *testing.T) {
	_, api := newAPIServer(t)
	wg := NewWaggleClient(api.URL)
	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	r := NewRouter(Config{AllowedChats: []int64{999}}, NewHandler(tg, wg, nil), nil)

	r.Dispatch(context.Background(), Update{Message: &Message{Chat: Chat{ID: 111}, Text: "/next"}})
	mu.Lock()
	n := len(sink)
	mu.Unlock()
	if n != 0 {
		t.Errorf("expected no outbound calls for disallowed chat, got %d", n)
	}
}

func TestDispatchNLIntentCreatesTask(t *testing.T) {
	_, api := newAPIServer(t)
	wg := NewWaggleClient(api.URL)
	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	nl := fakeNLParser{intent: Intent{Action: "create_task", Args: map[string]string{"title": "from nl"}}}
	r := NewRouter(Config{AllowedChats: []int64{111}}, NewHandler(tg, wg, nil), nl)

	r.Dispatch(context.Background(), Update{Message: &Message{Chat: Chat{ID: 111}, Text: "please make a task from nl"}})
	tasks, _ := wg.ListTasks("")
	if len(tasks) != 1 || tasks[0]["title"] != "from nl" {
		t.Fatalf("nl intent did not create task: %+v", tasks)
	}
}
