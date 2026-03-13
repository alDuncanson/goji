package orchestrator

import (
	"testing"
	"time"

	"goji/internal/model"
)

func TestRetryDelayContinuation(t *testing.T) {
	delay := retryDelay(1, 300_000, true)
	if delay != time.Second {
		t.Fatalf("expected 1s continuation delay, got %s", delay)
	}
}

func TestRetryDelayBackoffCapped(t *testing.T) {
	delay := retryDelay(10, 30_000, false)
	if delay != 30*time.Second {
		t.Fatalf("expected capped delay at 30s, got %s", delay)
	}
}

func TestSortForDispatch(t *testing.T) {
	p1 := 1
	p2 := 2
	now := time.Now().Add(-time.Hour)
	older := time.Now().Add(-2 * time.Hour)

	issues := []model.Issue{
		{Identifier: "#2", Priority: &p2, CreatedAt: &older},
		{Identifier: "#1", Priority: &p1, CreatedAt: &now},
		{Identifier: "#3", Priority: &p1, CreatedAt: &older},
	}

	sortForDispatch(issues)
	if issues[0].Identifier != "#3" || issues[1].Identifier != "#1" || issues[2].Identifier != "#2" {
		t.Fatalf("unexpected order: %#v", []string{issues[0].Identifier, issues[1].Identifier, issues[2].Identifier})
	}
}
