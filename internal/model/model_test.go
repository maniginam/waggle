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

func TestProjectStatusValid(t *testing.T) {
	valid := []ProjectStatus{ProjectActive, ProjectDormant, ProjectPaused, ProjectEarning, ProjectBroken, ProjectKilled}
	for _, s := range valid {
		if !s.Valid() {
			t.Errorf("expected %q to be valid", s)
		}
	}

	invalid := []ProjectStatus{"", "archived", "pending", "unknown"}
	for _, s := range invalid {
		if s.Valid() {
			t.Errorf("expected %q to be invalid", s)
		}
	}
}

