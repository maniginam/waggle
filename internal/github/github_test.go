package github

import (
	"errors"
	"strings"
	"testing"
)

type mockRunner struct {
	output []byte
	err    error
	calls  [][]string
}

func (m *mockRunner) Run(name string, args ...string) ([]byte, error) {
	m.calls = append(m.calls, append([]string{name}, args...))
	return m.output, m.err
}

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

func TestCreateIssueSuccess(t *testing.T) {
	m := &mockRunner{output: []byte("https://github.com/owner/repo/issues/42\n")}
	c := NewClientWithRunner("owner/repo", m)

	issue, err := c.CreateIssue("Bug title", "Bug body", []string{"bug", "priority:high"})
	if err != nil {
		t.Fatal(err)
	}
	if issue.Number != 42 {
		t.Errorf("expected issue number 42, got %d", issue.Number)
	}
	if issue.URL != "https://github.com/owner/repo/issues/42" {
		t.Errorf("unexpected URL: %s", issue.URL)
	}

	// Verify the correct args were passed
	if len(m.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.calls))
	}
	args := m.calls[0]
	if args[0] != "gh" {
		t.Errorf("expected gh command, got %s", args[0])
	}
	// Should contain --label bug --label priority:high
	argsStr := strings.Join(args, " ")
	if !strings.Contains(argsStr, "--label bug") {
		t.Error("expected --label bug in args")
	}
	if !strings.Contains(argsStr, "--label priority:high") {
		t.Error("expected --label priority:high in args")
	}
}

func TestCreateIssueNoLabels(t *testing.T) {
	m := &mockRunner{output: []byte("https://github.com/owner/repo/issues/1\n")}
	c := NewClientWithRunner("owner/repo", m)

	issue, err := c.CreateIssue("Title", "Body", nil)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Number != 1 {
		t.Errorf("expected 1, got %d", issue.Number)
	}
	argsStr := strings.Join(m.calls[0], " ")
	if strings.Contains(argsStr, "--label") {
		t.Error("should not have --label when labels is nil")
	}
}

func TestCreateIssueError(t *testing.T) {
	m := &mockRunner{output: []byte("permission denied"), err: errors.New("exit 1")}
	c := NewClientWithRunner("owner/repo", m)

	_, err := c.CreateIssue("Title", "Body", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("expected error to contain output, got: %s", err.Error())
	}
}

func TestCloseIssueSuccess(t *testing.T) {
	m := &mockRunner{output: []byte("Closed issue #5\n")}
	c := NewClientWithRunner("owner/repo", m)

	if err := c.CloseIssue(5); err != nil {
		t.Fatal(err)
	}
	if len(m.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.calls))
	}
	args := m.calls[0]
	if args[len(args)-1] != "5" {
		t.Errorf("expected issue number 5, got %s", args[len(args)-1])
	}
}

func TestCloseIssueError(t *testing.T) {
	m := &mockRunner{output: []byte("not found"), err: errors.New("exit 1")}
	c := NewClientWithRunner("owner/repo", m)

	err := c.CloseIssue(999)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "gh issue close") {
		t.Errorf("expected 'gh issue close' in error, got: %s", err.Error())
	}
}

func TestReopenIssueSuccess(t *testing.T) {
	m := &mockRunner{output: []byte("Reopened issue #3\n")}
	c := NewClientWithRunner("owner/repo", m)

	if err := c.ReopenIssue(3); err != nil {
		t.Fatal(err)
	}
	args := m.calls[0]
	if args[len(args)-1] != "3" {
		t.Errorf("expected 3, got %s", args[len(args)-1])
	}
}

func TestReopenIssueError(t *testing.T) {
	m := &mockRunner{output: []byte("err"), err: errors.New("exit 1")}
	c := NewClientWithRunner("owner/repo", m)

	err := c.ReopenIssue(1)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "gh issue reopen") {
		t.Errorf("expected 'gh issue reopen' in error, got: %s", err.Error())
	}
}

func TestCommentIssueSuccess(t *testing.T) {
	m := &mockRunner{output: []byte("ok\n")}
	c := NewClientWithRunner("owner/repo", m)

	if err := c.CommentIssue(7, "Great work!"); err != nil {
		t.Fatal(err)
	}
	argsStr := strings.Join(m.calls[0], " ")
	if !strings.Contains(argsStr, "Great work!") {
		t.Error("expected comment body in args")
	}
}

func TestCommentIssueError(t *testing.T) {
	m := &mockRunner{output: []byte("fail"), err: errors.New("exit 1")}
	c := NewClientWithRunner("owner/repo", m)

	err := c.CommentIssue(1, "body")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "gh issue comment") {
		t.Errorf("expected 'gh issue comment' in error, got: %s", err.Error())
	}
}

func TestAvailableTrue(t *testing.T) {
	old := availableRunner
	defer func() { availableRunner = old }()

	availableRunner = &mockRunner{output: []byte("Logged in")}
	if !Available() {
		t.Error("expected Available() to return true")
	}
}

func TestAvailableFalse(t *testing.T) {
	old := availableRunner
	defer func() { availableRunner = old }()

	availableRunner = &mockRunner{output: []byte("not logged in"), err: errors.New("exit 1")}
	if Available() {
		t.Error("expected Available() to return false")
	}
}

func TestEnsureLabels(t *testing.T) {
	m := &mockRunner{output: []byte("ok")}
	c := NewClientWithRunner("owner/repo", m)

	c.EnsureLabels([]string{"bug", "enhancement"})
	if len(m.calls) != 2 {
		t.Errorf("expected 2 calls (one per label), got %d", len(m.calls))
	}
}

func TestEnsureLabelsEmpty(t *testing.T) {
	m := &mockRunner{}
	c := NewClientWithRunner("owner/repo", m)

	c.EnsureLabels(nil)
	if len(m.calls) != 0 {
		t.Errorf("expected 0 calls for empty labels, got %d", len(m.calls))
	}
}

func TestListIssuesSuccess(t *testing.T) {
	jsonOut := `[{"number":1,"title":"Bug","state":"OPEN","url":"https://github.com/o/r/issues/1"}]`
	m := &mockRunner{output: []byte(jsonOut)}
	c := NewClientWithRunner("owner/repo", m)

	issues, err := c.ListIssues(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Errorf("expected 1 issue, got %d", len(issues))
	}

	argsStr := strings.Join(m.calls[0], " ")
	if !strings.Contains(argsStr, "--limit 10") {
		t.Error("expected --limit 10 in args")
	}
}

func TestListIssuesError(t *testing.T) {
	m := &mockRunner{output: []byte("err"), err: errors.New("exit 1")}
	c := NewClientWithRunner("owner/repo", m)

	_, err := c.ListIssues(5)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "gh issue list") {
		t.Errorf("expected 'gh issue list' in error, got: %s", err.Error())
	}
}

func TestListIssuesEmptyResult(t *testing.T) {
	m := &mockRunner{output: []byte("[]")}
	c := NewClientWithRunner("owner/repo", m)

	issues, err := c.ListIssues(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues, got %d", len(issues))
	}
}
