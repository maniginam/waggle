package overseer

import (
	"context"
	"errors"
	"testing"
)

func TestGasTownParsesTrail(t *testing.T) {
	src := NewGasTownSource("gt")
	src.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(`[{"bead":"b1","agent":"polecat-1","rig":"waggle","message":"fix x"}]`), nil
	}
	snap, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(snap.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(snap.Items))
	}
	it := snap.Items[0]
	if it.Event.Type != "agent.commit" || it.Event.AgentID != "polecat-1" || it.Key != "gastown:commit:b1" {
		t.Fatalf("bad item: %+v / %+v", it.Event, it.Key)
	}
}

func TestGasTownNullDegradesEmpty(t *testing.T) {
	src := NewGasTownSource("gt")
	src.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("null\n"), nil
	}
	snap, _ := src.Poll(context.Background())
	if len(snap.Items) != 0 {
		t.Fatalf("null should be empty, got %d", len(snap.Items))
	}
}

func TestGasTownExecErrorDegradesEmpty(t *testing.T) {
	src := NewGasTownSource("gt")
	src.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, errors.New("gt not found")
	}
	snap, err := src.Poll(context.Background())
	if err != nil || len(snap.Items) != 0 {
		t.Fatalf("exec error should degrade empty, got items=%d err=%v", len(snap.Items), err)
	}
}

func TestGasTownRejectsDisallowedSubcommand(t *testing.T) {
	called := false
	src := NewGasTownSource("gt")
	src.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		called = true
		return nil, nil
	}
	if _, err := src.runGT(context.Background(), "down"); err == nil {
		t.Fatal("expected disallowed subcommand error")
	}
	if called {
		t.Fatal("runner must not be invoked for disallowed subcommand")
	}
}
