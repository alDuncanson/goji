package prompt

import (
	"fmt"
	"strings"
	"time"

	"github.com/osteele/liquid"

	"goji/internal/model"
)

var engine = func() *liquid.Engine {
	e := liquid.NewEngine()
	e.StrictVariables()
	return e
}()

// Render renders a strict Liquid template with issue and attempt bindings.
func Render(template string, issue model.Issue, attempt *int) (string, error) {
	tpl := strings.TrimSpace(template)
	if tpl == "" {
		tpl = "You are working on issue {{ issue.identifier }}.\n\nTitle: {{ issue.title }}"
	}

	compiled, err := engine.ParseString(tpl)
	if err != nil {
		return "", fmt.Errorf("template_parse_error: %w", err)
	}

	bindings := liquid.Bindings{
		"issue":   issueToMap(issue),
		"attempt": attempt,
	}

	rendered, err := compiled.RenderString(bindings)
	if err != nil {
		return "", fmt.Errorf("template_render_error: %w", err)
	}

	return strings.TrimSpace(rendered), nil
}

func issueToMap(issue model.Issue) map[string]any {
	out := map[string]any{
		"id":          issue.ID,
		"identifier":  issue.Identifier,
		"title":       issue.Title,
		"description": issue.Description,
		"state":       issue.State,
		"branch_name": issue.BranchName,
		"url":         issue.URL,
		"labels":      issue.Labels,
		"blocked_by":  issue.BlockedBy,
		"number":      issue.Number,
	}

	if issue.Priority != nil {
		out["priority"] = *issue.Priority
	} else {
		out["priority"] = nil
	}

	if issue.CreatedAt != nil {
		out["created_at"] = issue.CreatedAt.UTC().Format(time.RFC3339)
	} else {
		out["created_at"] = nil
	}
	if issue.UpdatedAt != nil {
		out["updated_at"] = issue.UpdatedAt.UTC().Format(time.RFC3339)
	} else {
		out["updated_at"] = nil
	}

	return out
}
