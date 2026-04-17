package bridge

import (
	"context"
	"testing"
)

type fakeBridge struct {
	response string
	caps     []Capability
	provider string
}

func (f *fakeBridge) Chat(_ context.Context, msgs []Message, _ ChatOpts) (string, error) {
	return f.response, nil
}

func (f *fakeBridge) Capabilities() []Capability { return f.caps }
func (f *fakeBridge) Provider() string           { return f.provider }

func TestBridgeInterfaceCompliance(t *testing.T) {
	var b Bridge = &fakeBridge{
		response: "hello",
		caps:     []Capability{CapChat, CapCode},
		provider: "fake",
	}

	resp, err := b.Chat(context.Background(), []Message{
		{Role: "user", Content: "hi"},
	}, ChatOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "hello" {
		t.Errorf("got %q, want %q", resp, "hello")
	}
	if len(b.Capabilities()) != 2 {
		t.Errorf("got %d capabilities, want 2", len(b.Capabilities()))
	}
	if b.Provider() != "fake" {
		t.Errorf("got provider %q, want %q", b.Provider(), "fake")
	}
}

func TestModeConstants(t *testing.T) {
	if ModeMessageOnly != "message_only" {
		t.Errorf("got %q, want %q", ModeMessageOnly, "message_only")
	}
	if ModeFullParticipant != "full_participant" {
		t.Errorf("got %q, want %q", ModeFullParticipant, "full_participant")
	}
}

func TestChatOptsDefaults(t *testing.T) {
	opts := ChatOpts{}
	if opts.Model != "" || opts.MaxTokens != 0 || opts.Temperature != 0 || opts.SystemPrompt != "" {
		t.Error("zero-value ChatOpts should have empty defaults")
	}
}
