package workflow

import (
	"errors"
	"testing"
)

func TestParseWithFrontMatter(t *testing.T) {
	raw := `---
tracker:
  kind: github
  repo: owner/repo
polling:
  interval_ms: 5000
---

hello {{ issue.identifier }}`

	cfg, prompt, err := parse(raw)
	if err != nil {
		t.Fatalf("parse returned error: %v", err)
	}

	trackerMap, ok := cfg["tracker"].(map[string]any)
	if !ok {
		t.Fatalf("tracker config missing: %#v", cfg)
	}
	if trackerMap["kind"] != "github" {
		t.Fatalf("unexpected tracker.kind: %#v", trackerMap["kind"])
	}
	if prompt != "\nhello {{ issue.identifier }}" {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
}

func TestParseFrontMatterNotMap(t *testing.T) {
	raw := `---
- one
- two
---
hello`

	_, _, err := parse(raw)
	if !errors.Is(err, ErrWorkflowFrontMatterNotMap) {
		t.Fatalf("expected ErrWorkflowFrontMatterNotMap, got: %v", err)
	}
}

func TestParseWithoutFrontMatter(t *testing.T) {
	raw := `hello
world`

	cfg, prompt, err := parse(raw)
	if err != nil {
		t.Fatalf("parse returned error: %v", err)
	}
	if len(cfg) != 0 {
		t.Fatalf("expected empty config, got %#v", cfg)
	}
	if prompt != raw {
		t.Fatalf("unexpected prompt body: %q", prompt)
	}
}
