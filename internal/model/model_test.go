package model

import "testing"

func TestTaskStatusValid(t *testing.T) {
	valid := []TaskStatus{TaskBacklog, TaskReady, TaskInProgress, TaskReview, TaskDone, TaskBlocked}
	for _, s := range valid {
		if !s.Valid() {
			t.Errorf("expected %q to be valid", s)
		}
	}

	invalid := []TaskStatus{"", "unknown", "pending", "cancelled"}
	for _, s := range invalid {
		if s.Valid() {
			t.Errorf("expected %q to be invalid", s)
		}
	}
}

func TestPriorityValid(t *testing.T) {
	valid := []Priority{PriorityCritical, PriorityHigh, PriorityMedium, PriorityLow}
	for _, p := range valid {
		if !p.Valid() {
			t.Errorf("expected %q to be valid", p)
		}
	}

	invalid := []Priority{"", "urgent", "normal", "none"}
	for _, p := range invalid {
		if p.Valid() {
			t.Errorf("expected %q to be invalid", p)
		}
	}
}

func TestTaskTypeValid(t *testing.T) {
	valid := []TaskType{TaskTypeTask, TaskTypeEpic, TaskTypeStory, TaskTypeIssue}
	for _, tt := range valid {
		if !tt.Valid() {
			t.Errorf("expected %q to be valid", tt)
		}
	}

	invalid := []TaskType{"", "feature", "spike", "subtask"}
	for _, tt := range invalid {
		if tt.Valid() {
			t.Errorf("expected %q to be invalid", tt)
		}
	}
}

func TestCalculateCost(t *testing.T) {
	// Opus: $15/M input, $75/M output
	cost := CalculateCost("opus", 1_000_000, 1_000_000)
	if cost != 90.0 {
		t.Errorf("opus cost: expected 90.0, got %f", cost)
	}

	// Sonnet: $3/M input, $15/M output
	cost = CalculateCost("sonnet", 1_000_000, 1_000_000)
	if cost != 18.0 {
		t.Errorf("sonnet cost: expected 18.0, got %f", cost)
	}

	// Haiku: $0.25/M input, $1.25/M output
	cost = CalculateCost("haiku", 1_000_000, 1_000_000)
	if cost != 1.5 {
		t.Errorf("haiku cost: expected 1.5, got %f", cost)
	}

	// Unknown model defaults to sonnet pricing
	cost = CalculateCost("unknown-model", 1_000_000, 1_000_000)
	if cost != 18.0 {
		t.Errorf("unknown model cost: expected 18.0 (sonnet default), got %f", cost)
	}

	// Zero tokens
	cost = CalculateCost("opus", 0, 0)
	if cost != 0.0 {
		t.Errorf("zero tokens cost: expected 0.0, got %f", cost)
	}
}
