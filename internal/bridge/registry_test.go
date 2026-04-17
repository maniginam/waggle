package bridge

import (
	"fmt"
	"testing"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register("test", func() (Bridge, error) {
		return &fakeBridge{provider: "test"}, nil
	})

	constructor, ok := r.Get("test")
	if !ok {
		t.Fatal("expected provider 'test' to be registered")
	}
	b, err := constructor()
	if err != nil {
		t.Fatal(err)
	}
	if b.Provider() != "test" {
		t.Errorf("got provider %q, want %q", b.Provider(), "test")
	}
}

func TestRegistryGetUnknown(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected unknown provider to return false")
	}
}

func TestRegistryProviders(t *testing.T) {
	r := NewRegistry()
	r.Register("alpha", func() (Bridge, error) { return nil, nil })
	r.Register("beta", func() (Bridge, error) { return nil, nil })

	names := r.Providers()
	if len(names) != 2 {
		t.Fatalf("got %d providers, want 2", len(names))
	}
}

func TestRegistryConstructorError(t *testing.T) {
	r := NewRegistry()
	r.Register("bad", func() (Bridge, error) {
		return nil, fmt.Errorf("missing API key")
	})

	constructor, ok := r.Get("bad")
	if !ok {
		t.Fatal("expected provider to be registered")
	}
	_, err := constructor()
	if err == nil || err.Error() != "missing API key" {
		t.Errorf("expected 'missing API key' error, got %v", err)
	}
}
