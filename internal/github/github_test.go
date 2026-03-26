package github

import (
	"strings"
	"testing"
)

func TestRepoFromProject(t *testing.T) {
	tests := []struct {
		project string
		want    string
	}{
		{"waggle", "maniginam/waggle"},
		{"Waggle", "maniginam/waggle"},
		{"WAGGLE", "maniginam/waggle"},
		{"musicbox", "maniginam/ai-musicbox"},
		{"legacylift", "maniginam/legacy-lift"},
		{"legacy-lift", "maniginam/legacy-lift"},
		{"adjutant", "maniginam/adjutant"},
		{"unknown", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := RepoFromProject(tt.project)
		if got != tt.want {
			t.Errorf("RepoFromProject(%q) = %q, want %q", tt.project, got, tt.want)
		}
	}
}

func TestIssueBody(t *testing.T) {
	body := IssueBody("Fix the auth bug", []string{"Login works", "No regressions"}, "high", "issue", "wg-abc123")

	if !strings.Contains(body, "Fix the auth bug") {
		t.Error("expected description in body")
	}
	if !strings.Contains(body, "## Acceptance Criteria") {
		t.Error("expected criteria header")
	}
	if !strings.Contains(body, "- [ ] Login works") {
		t.Error("expected criteria checkbox")
	}
	if !strings.Contains(body, "**Priority:** high") {
		t.Error("expected priority")
	}
	if !strings.Contains(body, "`wg-abc123`") {
		t.Error("expected task ID")
	}
}

func TestIssueBodyNoCriteria(t *testing.T) {
	body := IssueBody("Simple task", nil, "medium", "task", "wg-xyz")
	if strings.Contains(body, "Acceptance Criteria") {
		t.Error("should not have criteria section when empty")
	}
	if !strings.Contains(body, "Simple task") {
		t.Error("expected description")
	}
}

func TestIssueBodyNoDescription(t *testing.T) {
	body := IssueBody("", []string{"Criterion"}, "low", "task", "wg-xyz")
	if strings.HasPrefix(body, "\n") {
		t.Error("should not start with blank lines when no description")
	}
	if !strings.Contains(body, "- [ ] Criterion") {
		t.Error("expected criteria")
	}
}

func TestLabelsFromTask(t *testing.T) {
	tests := []struct {
		priority string
		taskType string
		want     []string
	}{
		{"critical", "epic", []string{"priority:critical", "epic"}},
		{"high", "story", []string{"priority:high", "story"}},
		{"medium", "issue", []string{"bug"}},
		{"low", "task", nil},
		{"medium", "task", nil},
	}
	for _, tt := range tests {
		got := LabelsFromTask(tt.priority, tt.taskType)
		if len(got) != len(tt.want) {
			t.Errorf("LabelsFromTask(%q, %q) = %v, want %v", tt.priority, tt.taskType, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("LabelsFromTask(%q, %q)[%d] = %q, want %q", tt.priority, tt.taskType, i, got[i], tt.want[i])
			}
		}
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient("owner/repo")
	if c.repo != "owner/repo" {
		t.Errorf("expected owner/repo, got %s", c.repo)
	}
}
