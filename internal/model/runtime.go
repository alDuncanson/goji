package model

import "time"

// AgentUpdate is a normalized runtime update emitted by an agent invocation.
type AgentUpdate struct {
	Event            string         `json:"event"`
	Timestamp        time.Time      `json:"timestamp"`
	SessionID        string         `json:"session_id,omitempty"`
	ThreadID         string         `json:"thread_id,omitempty"`
	TurnID           string         `json:"turn_id,omitempty"`
	AgentPID         int            `json:"agent_pid,omitempty"`
	Message          string         `json:"message,omitempty"`
	InputTokens      int            `json:"input_tokens,omitempty"`
	OutputTokens     int            `json:"output_tokens,omitempty"`
	TotalTokens      int            `json:"total_tokens,omitempty"`
	RateLimits       map[string]any `json:"rate_limits,omitempty"`
	Raw              map[string]any `json:"raw,omitempty"`
	InputRequired    bool           `json:"input_required,omitempty"`
	UnsupportedTool  bool           `json:"unsupported_tool,omitempty"`
	ApprovalRequired bool           `json:"approval_required,omitempty"`
}

// TokenTotals captures aggregate token accounting.
type TokenTotals struct {
	InputTokens    int `json:"input_tokens"`
	OutputTokens   int `json:"output_tokens"`
	TotalTokens    int `json:"total_tokens"`
	SecondsRunning int `json:"seconds_running"`
}

// RuntimeEvent is a compact event for observability surfaces.
type RuntimeEvent struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Type    string    `json:"type"`
	IssueID string    `json:"issue_id,omitempty"`
	Issue   string    `json:"issue_identifier,omitempty"`
	Message string    `json:"message"`
}
