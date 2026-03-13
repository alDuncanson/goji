package model

import "time"

// BlockerRef captures a normalized blocker relationship for an issue.
type BlockerRef struct {
	ID         string `json:"id,omitempty"`
	Identifier string `json:"identifier,omitempty"`
	State      string `json:"state,omitempty"`
}

// Issue is the tracker-neutral issue shape consumed by orchestration.
type Issue struct {
	ID          string       `json:"id"`
	Identifier  string       `json:"identifier"`
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Priority    *int         `json:"priority,omitempty"`
	State       string       `json:"state"`
	BranchName  string       `json:"branch_name,omitempty"`
	URL         string       `json:"url,omitempty"`
	Labels      []string     `json:"labels,omitempty"`
	BlockedBy   []BlockerRef `json:"blocked_by,omitempty"`
	CreatedAt   *time.Time   `json:"created_at,omitempty"`
	UpdatedAt   *time.Time   `json:"updated_at,omitempty"`
	Number      int          `json:"number,omitempty"`
}

func (i Issue) HasRequiredDispatchFields() bool {
	return i.ID != "" && i.Identifier != "" && i.Title != "" && i.State != ""
}
