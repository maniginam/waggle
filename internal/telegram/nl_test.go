package telegram

import (
	"context"
	"os"
	"testing"
)

type fakeNLParser struct{ intent Intent }

func (f fakeNLParser) Parse(_ context.Context, _ string) (Intent, error) { return f.intent, nil }

func TestNLParserDisabledWithoutKey(t *testing.T) {
	old := os.Getenv("ANTHROPIC_API_KEY")
	os.Unsetenv("ANTHROPIC_API_KEY")
	defer os.Setenv("ANTHROPIC_API_KEY", old)
	if _, ok := NewClaudeNLParser(); ok {
		t.Error("expected NL disabled when ANTHROPIC_API_KEY is unset")
	}
}

func TestFakeNLParserRoundtrips(t *testing.T) {
	f := fakeNLParser{intent: Intent{Action: "create_task", Args: map[string]string{"title": "x"}}}
	got, _ := f.Parse(context.Background(), "make a task called x")
	if got.Action != "create_task" || got.Args["title"] != "x" {
		t.Errorf("got %+v", got)
	}
}
