package github

import (
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

type Issue struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

type Client struct {
	repo string
}

func NewClient(repo string) *Client {
	return &Client{repo: repo}
}

func (c *Client) CreateIssue(title, body string, labels []string) (*Issue, error) {
	args := []string{"issue", "create",
		"--repo", c.repo,
		"--title", title,
		"--body", body,
	}
	for _, l := range labels {
		args = append(args, "--label", l)
	}

	out, err := exec.Command("gh", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh issue create: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// gh issue create returns the issue URL on stdout
	issueURL := strings.TrimSpace(string(out))

	// Extract issue number from URL (https://github.com/owner/repo/issues/123)
	parts := strings.Split(issueURL, "/")
	var number int
	if len(parts) > 0 {
		fmt.Sscanf(parts[len(parts)-1], "%d", &number)
	}

	return &Issue{Number: number, URL: issueURL}, nil
}

func (c *Client) CloseIssue(number int) error {
	out, err := exec.Command("gh", "issue", "close",
		"--repo", c.repo,
		fmt.Sprintf("%d", number),
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh issue close: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (c *Client) ReopenIssue(number int) error {
	out, err := exec.Command("gh", "issue", "reopen",
		"--repo", c.repo,
		fmt.Sprintf("%d", number),
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh issue reopen: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (c *Client) CommentIssue(number int, body string) error {
	out, err := exec.Command("gh", "issue", "comment",
		"--repo", c.repo,
		fmt.Sprintf("%d", number),
		"--body", body,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh issue comment: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Available checks if the gh CLI is installed and authenticated.
func Available() bool {
	out, err := exec.Command("gh", "auth", "status").CombinedOutput()
	if err != nil {
		log.Printf("gh CLI not available: %s", strings.TrimSpace(string(out)))
		return false
	}
	return true
}

// RepoFromProject maps a waggle project name to a GitHub repo.
// Returns empty string if no mapping exists.
func RepoFromProject(projectName string) string {
	// This can be extended with a config file later.
	// For now, the waggle project itself maps to maniginam/waggle.
	repos := map[string]string{
		"waggle":      "maniginam/waggle",
		"musicbox":    "maniginam/ai-musicbox",
		"legacylift":  "maniginam/legacy-lift",
		"legacy-lift": "maniginam/legacy-lift",
		"adjutant":    "maniginam/adjutant",
	}
	return repos[strings.ToLower(projectName)]
}

// IssueBody builds a GitHub issue body from task fields.
func IssueBody(description string, criteria []string, priority, taskType, taskID string) string {
	var b strings.Builder
	if description != "" {
		b.WriteString(description)
		b.WriteString("\n\n")
	}
	if len(criteria) > 0 {
		b.WriteString("## Acceptance Criteria\n")
		for _, c := range criteria {
			b.WriteString("- [ ] ")
			b.WriteString(c)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("**Priority:** %s | **Type:** %s\n", priority, taskType))
	b.WriteString(fmt.Sprintf("**Waggle Task ID:** `%s`\n", taskID))
	return b.String()
}

// LabelsFromTask returns GitHub labels based on task fields.
func LabelsFromTask(priority, taskType string) []string {
	var labels []string
	switch priority {
	case "critical":
		labels = append(labels, "priority:critical")
	case "high":
		labels = append(labels, "priority:high")
	}
	switch taskType {
	case "epic":
		labels = append(labels, "epic")
	case "story":
		labels = append(labels, "story")
	case "issue":
		labels = append(labels, "bug")
	}
	return labels
}

// Ensure labels exist in the repo (creates them if missing).
func (c *Client) EnsureLabels(labels []string) {
	for _, label := range labels {
		// Try to create; ignore errors if label already exists
		exec.Command("gh", "label", "create",
			"--repo", c.repo,
			label,
			"--force",
		).Run()
	}
}

// ListIssues returns open issues for the repo (as raw JSON).
func (c *Client) ListIssues(limit int) ([]json.RawMessage, error) {
	out, err := exec.Command("gh", "issue", "list",
		"--repo", c.repo,
		"--json", "number,title,state,url",
		"--limit", fmt.Sprintf("%d", limit),
	).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh issue list: %s: %w", strings.TrimSpace(string(out)), err)
	}
	var issues []json.RawMessage
	json.Unmarshal(out, &issues)
	return issues, nil
}
