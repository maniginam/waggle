package api

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveClaudeBin_PathHit(t *testing.T) {
	lookPath := func(string) (string, error) { return "/usr/local/bin/claude", nil }
	// Candidates must be ignored when PATH lookup succeeds.
	got, err := resolveClaudeBin(lookPath, []string{"/should/not/be/used"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/usr/local/bin/claude" {
		t.Fatalf("got %q, want /usr/local/bin/claude", got)
	}
}

func TestResolveClaudeBin_FallbackToCandidate(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	lookPath := func(string) (string, error) { return "", errors.New("not in PATH") }
	got, err := resolveClaudeBin(lookPath, []string{filepath.Join(dir, "missing"), bin})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != bin {
		t.Fatalf("got %q, want %q", got, bin)
	}
}

func TestResolveClaudeBin_SkipsDirAndNonExecutable(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "claude-dir")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	nonExec := filepath.Join(dir, "claude-noexec")
	if err := os.WriteFile(nonExec, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "claude-good")
	if err := os.WriteFile(good, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	lookPath := func(string) (string, error) { return "", errors.New("not in PATH") }
	got, err := resolveClaudeBin(lookPath, []string{subdir, nonExec, good})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != good {
		t.Fatalf("got %q, want %q (should skip dir and non-executable)", got, good)
	}
}

func TestResolveClaudeBin_NotFound(t *testing.T) {
	lookPath := func(string) (string, error) { return "", errors.New("not in PATH") }
	_, err := resolveClaudeBin(lookPath, []string{"/no/such/claude"})
	if err == nil {
		t.Fatal("expected error when claude is nowhere, got nil")
	}
}

func TestDefaultClaudeCandidates_OrderAndContents(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	got := defaultClaudeCandidates()
	want := []string{
		"/home/tester/.local/bin/claude",
		"/opt/homebrew/bin/claude",
		"/usr/local/bin/claude",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v (order matters: most-preferred first)", got, want)
	}
}

func TestDefaultClaudeCandidates_OmitsHomeWhenUnset(t *testing.T) {
	t.Setenv("HOME", "")
	got := defaultClaudeCandidates()
	for _, c := range got {
		if c == "/.local/bin/claude" {
			t.Fatalf("expected no malformed home candidate, got %v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 system candidates when HOME unset", got)
	}
}
