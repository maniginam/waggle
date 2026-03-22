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
