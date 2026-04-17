package providers

import (
	"os"
	"testing"
)

func TestDefaultRegistryHasAllProviders(t *testing.T) {
	r := DefaultRegistry()
	expected := []string{"bedrock", "claude-api", "gemini", "grok", "ollama", "openai"}
	providers := r.Providers()

	if len(providers) != len(expected) {
		t.Fatalf("got %d providers %v, want %d %v", len(providers), providers, len(expected), expected)
	}
	for i, name := range expected {
		if providers[i] != name {
			t.Errorf("provider[%d] = %q, want %q", i, providers[i], name)
		}
	}
}

func TestDefaultRegistryOpenAIRequiresKey(t *testing.T) {
	r := DefaultRegistry()
	constructor, ok := r.Get("openai")
	if !ok {
		t.Fatal("openai not registered")
	}

	origKey := os.Getenv("OPENAI_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	defer os.Setenv("OPENAI_API_KEY", origKey)

	_, err := constructor()
	if err == nil {
		t.Error("expected error when OPENAI_API_KEY is not set")
	}
}

func TestDefaultRegistryOllamaNoKeyRequired(t *testing.T) {
	r := DefaultRegistry()
	constructor, ok := r.Get("ollama")
	if !ok {
		t.Fatal("ollama not registered")
	}

	b, err := constructor()
	if err != nil {
		t.Fatalf("ollama should not require an API key: %v", err)
	}
	if b.Provider() != "openai" {
		t.Errorf("got provider %q, want %q", b.Provider(), "openai")
	}
}
