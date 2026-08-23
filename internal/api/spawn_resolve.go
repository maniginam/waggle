package api

import (
	"fmt"
	"os"
	"path/filepath"
)

// resolveClaudeBin finds the claude CLI. It prefers the PATH lookup, then
// falls back to well-known install directories so a daemon launched with a
// minimal PATH (e.g. launchd) can still spawn agents.
func resolveClaudeBin(lookPath func(string) (string, error), candidates []string) (string, error) {
	if p, err := lookPath("claude"); err == nil {
		return p, nil
	}
	for _, c := range candidates {
		// os.Stat follows symlinks intentionally: the homebrew claude is a
		// symlink into node_modules, so a symlink candidate is expected and
		// valid. The candidate dirs are system-owned, not user-writable.
		info, err := os.Stat(c)
		if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
			continue
		}
		return c, nil
	}
	return "", fmt.Errorf("claude CLI not found in PATH or known install locations")
}

// defaultClaudeCandidates lists common claude install paths, most-preferred first.
func defaultClaudeCandidates() []string {
	var cands []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		cands = append(cands, filepath.Join(home, ".local", "bin", "claude"))
	}
	cands = append(cands,
		"/opt/homebrew/bin/claude",
		"/usr/local/bin/claude",
	)
	return cands
}
