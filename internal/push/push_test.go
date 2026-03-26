package push

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maniginam/waggle/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNewNotifierGeneratesKeys(t *testing.T) {
	// Override HOME so keys go to temp dir
	origHome := os.Getenv("HOME")
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	s := testStore(t)
	n, err := NewNotifier(s)
	if err != nil {
		t.Fatal(err)
	}

	if n.PublicKey() == "" {
		t.Error("expected non-empty public key")
	}
	if n.vapidPriv == "" {
		t.Error("expected non-empty private key")
	}

	// Verify keys were saved to disk
	keyPath := filepath.Join(tmpHome, ".waggle", "vapid.json")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("expected vapid.json to be written: %v", err)
	}
	var keys vapidKeys
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("invalid vapid.json: %v", err)
	}
	if keys.PublicKey != n.PublicKey() {
		t.Error("saved public key doesn't match notifier")
	}
}

func TestNewNotifierLoadsExistingKeys(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	// Pre-create key file
	keyDir := filepath.Join(tmpHome, ".waggle")
	os.MkdirAll(keyDir, 0755)
	keys := vapidKeys{PublicKey: "test-pub-key", PrivateKey: "test-priv-key"}
	data, _ := json.Marshal(keys)
	os.WriteFile(filepath.Join(keyDir, "vapid.json"), data, 0600)

	s := testStore(t)
	n, err := NewNotifier(s)
	if err != nil {
		t.Fatal(err)
	}

	if n.PublicKey() != "test-pub-key" {
		t.Errorf("expected test-pub-key, got %s", n.PublicKey())
	}
	if n.vapidPriv != "test-priv-key" {
		t.Errorf("expected test-priv-key, got %s", n.vapidPriv)
	}
}

func TestSendNoSubscriptionsIsNoop(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	s := testStore(t)
	n, err := NewNotifier(s)
	if err != nil {
		t.Fatal(err)
	}

	// Should not panic or error with no subscriptions
	n.Send(PushPayload{
		Title: "Test",
		Body:  "No subscribers",
		Tag:   "test",
	})
}

func TestPushPayloadMarshal(t *testing.T) {
	p := PushPayload{
		Title: "Task done",
		Body:  "Build completed",
		Tag:   "task_completed",
		URL:   "/",
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	json.Unmarshal(data, &decoded)
	if decoded["title"] != "Task done" {
		t.Errorf("expected title 'Task done', got %s", decoded["title"])
	}
	if decoded["tag"] != "task_completed" {
		t.Errorf("expected tag 'task_completed', got %s", decoded["tag"])
	}
}

func TestSendWithSubscriptionsHandlesErrors(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	s := testStore(t)
	n, err := NewNotifier(s)
	if err != nil {
		t.Fatal(err)
	}

	// Add a subscription with a fake endpoint (will fail to send)
	s.SavePushSubscription(&store.PushSubscription{
		Endpoint: "https://fake-push-service.invalid/sub/short",
		Auth:     "fakeauth",
		P256dh:   "fakep256dh",
	})

	// Should not panic — exercises the error path in Send
	n.Send(PushPayload{Title: "Test", Body: "Error path"})
}

func TestSendWithLongEndpointTruncates(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	s := testStore(t)
	n, err := NewNotifier(s)
	if err != nil {
		t.Fatal(err)
	}

	// Long endpoint to exercise truncation (>40 chars)
	longEndpoint := "https://fake-push-service.invalid/very-long-subscription-endpoint-identifier"
	s.SavePushSubscription(&store.PushSubscription{
		Endpoint: longEndpoint,
		Auth:     "fakeauth",
		P256dh:   "fakep256dh",
	})

	// Should not panic — exercises truncation path
	n.Send(PushPayload{Title: "Test", Body: "Long endpoint"})
}

func TestSendWithMultipleSubscriptions(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	s := testStore(t)
	n, err := NewNotifier(s)
	if err != nil {
		t.Fatal(err)
	}

	s.SavePushSubscription(&store.PushSubscription{
		Endpoint: "https://fake1.invalid/sub1",
		Auth:     "auth1",
		P256dh:   "p256dh1",
	})
	s.SavePushSubscription(&store.PushSubscription{
		Endpoint: "https://fake2.invalid/sub2",
		Auth:     "auth2",
		P256dh:   "p256dh2",
	})

	// Should iterate through all subs without panic
	n.Send(PushPayload{Title: "Multi", Body: "Multiple subs"})
}

func TestPayloadOmitsEmptyFields(t *testing.T) {
	p := PushPayload{Title: "Minimal", Body: "Just title and body"}
	data, _ := json.Marshal(p)
	var decoded map[string]any
	json.Unmarshal(data, &decoded)
	if _, ok := decoded["tag"]; ok {
		t.Error("expected tag to be omitted when empty")
	}
	if _, ok := decoded["url"]; ok {
		t.Error("expected url to be omitted when empty")
	}
}
