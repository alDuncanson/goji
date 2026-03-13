package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"goji/internal/config"
	"goji/internal/model"
	"goji/internal/tracker"
	"goji/internal/util"
)

var _ tracker.Client = (*Adapter)(nil)

// Adapter reads GitHub issues through the gh CLI.
type Adapter struct{}

func New() *Adapter {
	return &Adapter{}
}

func (a *Adapter) FetchCandidateIssues(ctx context.Context, cfg config.ServiceConfig) ([]model.Issue, error) {
	issues, err := a.fetchIssues(ctx, cfg, cfg.Tracker.CandidateLimit)
	if err != nil {
		return nil, err
	}

	active := cfg.ActiveStateSet()
	terminal := cfg.TerminalStateSet()
	out := make([]model.Issue, 0, len(issues))
	for _, issue := range issues {
		state := config.NormalizeState(issue.State)
		if _, ok := active[state]; !ok {
			continue
		}
		if _, ok := terminal[state]; ok {
			continue
		}
		out = append(out, issue)
	}
	return out, nil
}

func (a *Adapter) FetchIssuesByStates(ctx context.Context, cfg config.ServiceConfig, states []string) ([]model.Issue, error) {
	issues, err := a.fetchIssues(ctx, cfg, cfg.Tracker.CandidateLimit)
	if err != nil {
		return nil, err
	}

	want := map[string]struct{}{}
	for _, state := range states {
		want[config.NormalizeState(state)] = struct{}{}
	}

	out := make([]model.Issue, 0, len(issues))
	for _, issue := range issues {
		if _, ok := want[config.NormalizeState(issue.State)]; ok {
			out = append(out, issue)
		}
	}
	return out, nil
}

func (a *Adapter) FetchIssueStatesByIDs(ctx context.Context, cfg config.ServiceConfig, issueIDs []string) ([]model.Issue, error) {
	out := make([]model.Issue, 0, len(issueIDs))
	for _, issueID := range issueIDs {
		num, err := strconv.Atoi(strings.TrimSpace(issueID))
		if err != nil || num <= 0 {
			continue
		}

		issue, err := a.fetchIssueByNumber(ctx, cfg, num)
		if err != nil {
			return nil, err
		}
		if issue != nil {
			out = append(out, *issue)
		}
	}
	return out, nil
}

func (a *Adapter) fetchIssues(ctx context.Context, cfg config.ServiceConfig, limit int) ([]model.Issue, error) {
	if limit <= 0 {
		limit = config.DefaultCandidateLimit
	}

	result, err := util.RunCommand(
		ctx,
		"",
		nil,
		cfg.Tracker.GHBinary,
		"issue", "list",
		"--repo", cfg.Tracker.Repo,
		"--state", "all",
		"--limit", strconv.Itoa(limit),
		"--json", "id,number,title,body,state,url,labels,createdAt,updatedAt",
	)
	if err != nil {
		return nil, fmt.Errorf("github issue list failed: %w: %s", err, strings.TrimSpace(result.Stderr))
	}

	var payload []ghIssue
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return nil, fmt.Errorf("github issue list decode failed: %w", err)
	}

	issues := make([]model.Issue, 0, len(payload))
	for _, raw := range payload {
		issues = append(issues, normalizeIssue(raw, cfg))
	}
	return issues, nil
}

func (a *Adapter) fetchIssueByNumber(ctx context.Context, cfg config.ServiceConfig, number int) (*model.Issue, error) {
	result, err := util.RunCommand(
		ctx,
		"",
		nil,
		cfg.Tracker.GHBinary,
		"issue", "view", strconv.Itoa(number),
		"--repo", cfg.Tracker.Repo,
		"--json", "id,number,title,body,state,url,labels,createdAt,updatedAt",
	)
	if err != nil {
		stderr := strings.TrimSpace(result.Stderr)
		if strings.Contains(stderr, "Could not resolve to an Issue") || strings.Contains(stderr, "404") {
			return nil, nil
		}
		return nil, fmt.Errorf("github issue view failed: %w: %s", err, stderr)
	}

	var payload ghIssue
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return nil, fmt.Errorf("github issue view decode failed: %w", err)
	}
	issue := normalizeIssue(payload, cfg)
	return &issue, nil
}

type ghIssue struct {
	ID        string    `json:"id"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	URL       string    `json:"url"`
	Labels    []ghLabel `json:"labels"`
	CreatedAt string    `json:"createdAt"`
	UpdatedAt string    `json:"updatedAt"`
}

type ghLabel struct {
	Name string `json:"name"`
}

func normalizeIssue(in ghIssue, cfg config.ServiceConfig) model.Issue {
	labels := make([]string, 0, len(in.Labels))
	for _, label := range in.Labels {
		norm := strings.ToLower(strings.TrimSpace(label.Name))
		if norm != "" {
			labels = append(labels, norm)
		}
	}

	state := deriveState(in, cfg)
	priority := derivePriority(labels, cfg.Tracker.PriorityLabelPrefix)
	blockedBy := deriveBlockedBy(labels, cfg.Tracker.BlockedByLabelPrefix)

	createdAt := parseTimestamp(in.CreatedAt)
	updatedAt := parseTimestamp(in.UpdatedAt)

	id := strconv.Itoa(in.Number)
	if strings.TrimSpace(in.ID) != "" {
		id = strconv.Itoa(in.Number)
	}

	return model.Issue{
		ID:          id,
		Identifier:  fmt.Sprintf("#%d", in.Number),
		Number:      in.Number,
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Body),
		Priority:    priority,
		State:       state,
		URL:         strings.TrimSpace(in.URL),
		Labels:      labels,
		BlockedBy:   blockedBy,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}
}

func deriveState(in ghIssue, cfg config.ServiceConfig) string {
	prefix := strings.ToLower(strings.TrimSpace(cfg.Tracker.StateLabelPrefix))
	for _, label := range in.Labels {
		value := strings.ToLower(strings.TrimSpace(label.Name))
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		state := strings.TrimSpace(value[len(prefix):])
		if state == "" {
			continue
		}
		return humanizeState(state)
	}

	if strings.EqualFold(strings.TrimSpace(in.State), "closed") {
		return "Done"
	}

	return "Todo"
}

func humanizeState(value string) string {
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return "Todo"
	}
	for i, part := range parts {
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func derivePriority(labels []string, prefix string) *int {
	pfx := strings.ToLower(strings.TrimSpace(prefix))
	for _, label := range labels {
		if !strings.HasPrefix(label, pfx) {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(label, pfx))
		value, err := strconv.Atoi(raw)
		if err != nil {
			continue
		}
		return &value
	}
	return nil
}

func deriveBlockedBy(labels []string, prefix string) []model.BlockerRef {
	pfx := strings.ToLower(strings.TrimSpace(prefix))
	if pfx == "" {
		return nil
	}

	blocked := make([]model.BlockerRef, 0)
	for _, label := range labels {
		if !strings.HasPrefix(label, pfx) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(label, pfx))
		if value == "" {
			continue
		}
		blocked = append(blocked, model.BlockerRef{Identifier: value})
	}
	return blocked
}

func parseTimestamp(raw string) *time.Time {
	t := strings.TrimSpace(raw)
	if t == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, t)
	if err != nil {
		return nil
	}
	return &parsed
}
