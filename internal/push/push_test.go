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
