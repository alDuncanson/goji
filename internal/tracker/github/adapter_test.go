package github

import (
	"testing"

	"goji/internal/config"
)

func TestDeriveStateFromLabel(t *testing.T) {
	cfg := config.ServiceConfig{Tracker: config.TrackerConfig{StateLabelPrefix: "state:"}}
	issue := ghIssue{
		State: "OPEN",
		Labels: []ghLabel{
			{Name: "state:in-progress"},
		},
	}

	state := deriveState(issue, cfg)
	if state != "In Progress" {
		t.Fatalf("expected In Progress, got %q", state)
	}
}

func TestDeriveStateFallbackClosed(t *testing.T) {
	cfg := config.ServiceConfig{Tracker: config.TrackerConfig{StateLabelPrefix: "state:"}}
	issue := ghIssue{State: "CLOSED"}
	if state := deriveState(issue, cfg); state != "Done" {
		t.Fatalf("expected Done, got %q", state)
	}
}

func TestDerivePriority(t *testing.T) {
	priority := derivePriority([]string{"priority:2"}, "priority:")
	if priority == nil || *priority != 2 {
		t.Fatalf("expected priority 2, got %#v", priority)
	}
}

func TestDeriveBlockedBy(t *testing.T) {
	blocked := deriveBlockedBy([]string{"blocked-by:#12", "other"}, "blocked-by:")
	if len(blocked) != 1 || blocked[0].Identifier != "#12" {
		t.Fatalf("unexpected blocked_by: %#v", blocked)
	}
}
