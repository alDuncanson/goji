package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse(map[string]any{
		"tracker": map[string]any{"repo": "acme/widgets"},
	}, "")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if cfg.Tracker.Kind != "github" {
		t.Fatalf("unexpected tracker kind: %s", cfg.Tracker.Kind)
	}
	if cfg.Agent.MaxConcurrentAgents != DefaultMaxConcurrentAgents {
		t.Fatalf("unexpected max concurrency: %d", cfg.Agent.MaxConcurrentAgents)
	}
	if cfg.Runner.Command == "" {
		t.Fatal("runner command should default")
	}
}

func TestParseResolvesEnvAndPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home lookup failed: %v", err)
	}
	_ = os.Setenv("GOJI_TEST_WORKSPACE", "~/tmp/goji-ws")
	defer os.Unsetenv("GOJI_TEST_WORKSPACE")

	cfg, err := Parse(map[string]any{
		"tracker":   map[string]any{"repo": "acme/widgets"},
		"workspace": map[string]any{"root": "$GOJI_TEST_WORKSPACE"},
	}, "")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	expected := filepath.Clean(filepath.Join(home, "tmp/goji-ws"))
	if cfg.Workspace.Root != expected {
		t.Fatalf("expected workspace root %q, got %q", expected, cfg.Workspace.Root)
	}
}

func TestNormalizeStateLimits(t *testing.T) {
	cfg, err := Parse(map[string]any{
		"tracker": map[string]any{"repo": "acme/widgets"},
		"agent": map[string]any{
			"max_concurrent_agents_by_state": map[string]any{
				"In Progress": 3,
				"Todo":        0,
				"":            99,
			},
		},
	}, "")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if got := cfg.MaxConcurrentForState("in progress"); got != 3 {
		t.Fatalf("expected state limit 3, got %d", got)
	}
	if _, ok := cfg.Agent.MaxConcurrentAgentsByState[""]; ok {
		t.Fatal("blank state key should be ignored")
	}
}
