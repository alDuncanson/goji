package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultPollIntervalMS      = 30_000
	DefaultHookTimeoutMS       = 60_000
	DefaultMaxConcurrentAgents = 10
	DefaultMaxRetryBackoffMS   = 300_000
	DefaultMaxTurns            = 20
	DefaultTurnTimeoutMS       = 3_600_000
	DefaultReadTimeoutMS       = 5_000
	DefaultStallTimeoutMS      = 300_000
	DefaultCandidateLimit      = 200
)

var (
	ErrMissingTrackerKind        = errors.New("missing_tracker_kind")
	ErrUnsupportedTrackerKind    = errors.New("unsupported_tracker_kind")
	ErrMissingTrackerRepo        = errors.New("missing_tracker_repo")
	ErrMissingAgentCommand       = errors.New("missing_agent_command")
	ErrWorkflowFrontMatterNotMap = errors.New("workflow_front_matter_not_a_map")
)

// ServiceConfig is the typed runtime configuration derived from WORKFLOW.md.
type ServiceConfig struct {
	Tracker        TrackerConfig
	Polling        PollingConfig
	Workspace      WorkspaceConfig
	Hooks          HooksConfig
	Agent          AgentConfig
	Runner         RunnerConfig
	PromptTemplate string
}

// TrackerConfig controls issue tracker integration.
type TrackerConfig struct {
	Kind                 string
	Repo                 string
	GHBinary             string
	ActiveStates         []string
	TerminalStates       []string
	StateLabelPrefix     string
	PriorityLabelPrefix  string
	BlockedByLabelPrefix string
	CandidateLimit       int
}

// PollingConfig controls scheduler cadence.
type PollingConfig struct {
	IntervalMS int
}

// WorkspaceConfig controls workspace pathing.
type WorkspaceConfig struct {
	Root string
}

// HooksConfig controls workspace hook scripts.
type HooksConfig struct {
	AfterCreate  string
	BeforeRun    string
	AfterRun     string
	BeforeRemove string
	TimeoutMS    int
}

// AgentConfig controls orchestrator-level dispatch/retry behavior.
type AgentConfig struct {
	MaxConcurrentAgents        int
	MaxRetryBackoffMS          int
	MaxTurns                   int
	MaxConcurrentAgentsByState map[string]int
}

// RunnerConfig controls coding-agent CLI execution.
type RunnerConfig struct {
	Command        string
	PromptMode     string
	OutputFormat   string
	Env            map[string]string
	TurnTimeoutMS  int
	ReadTimeoutMS  int
	StallTimeoutMS int
}

func Parse(raw map[string]any, promptTemplate string) (ServiceConfig, error) {
	cfg := ServiceConfig{
		Tracker: TrackerConfig{
			Kind:                 "github",
			Repo:                 strings.TrimSpace(os.Getenv("GOJI_GITHUB_REPO")),
			GHBinary:             "gh",
			ActiveStates:         []string{"Todo", "In Progress"},
			TerminalStates:       []string{"Closed", "Cancelled", "Canceled", "Duplicate", "Done"},
			StateLabelPrefix:     "state:",
			PriorityLabelPrefix:  "priority:",
			BlockedByLabelPrefix: "blocked-by:",
			CandidateLimit:       DefaultCandidateLimit,
		},
		Polling:   PollingConfig{IntervalMS: DefaultPollIntervalMS},
		Workspace: WorkspaceConfig{Root: defaultWorkspaceRoot()},
		Hooks:     HooksConfig{TimeoutMS: DefaultHookTimeoutMS},
		Agent: AgentConfig{
			MaxConcurrentAgents:        DefaultMaxConcurrentAgents,
			MaxRetryBackoffMS:          DefaultMaxRetryBackoffMS,
			MaxTurns:                   DefaultMaxTurns,
			MaxConcurrentAgentsByState: map[string]int{},
		},
		Runner: RunnerConfig{
			Command:        "amp --execute --stream-json",
			PromptMode:     "stdin",
			OutputFormat:   "amp_stream_json",
			Env:            map[string]string{},
			TurnTimeoutMS:  DefaultTurnTimeoutMS,
			ReadTimeoutMS:  DefaultReadTimeoutMS,
			StallTimeoutMS: DefaultStallTimeoutMS,
		},
		PromptTemplate: strings.TrimSpace(promptTemplate),
	}

	if tracker := mapValue(raw, "tracker"); tracker != nil {
		cfg.Tracker.Kind = normalizeEmpty(stringValue(tracker, "kind", cfg.Tracker.Kind), cfg.Tracker.Kind)
		cfg.Tracker.Repo = resolveEnvString(stringValue(tracker, "repo", cfg.Tracker.Repo), cfg.Tracker.Repo)
		cfg.Tracker.GHBinary = normalizeEmpty(stringValue(tracker, "gh_binary", cfg.Tracker.GHBinary), cfg.Tracker.GHBinary)
		cfg.Tracker.ActiveStates = normalizeStates(stringSliceValue(tracker, "active_states", cfg.Tracker.ActiveStates))
		cfg.Tracker.TerminalStates = normalizeStates(stringSliceValue(tracker, "terminal_states", cfg.Tracker.TerminalStates))
		cfg.Tracker.StateLabelPrefix = normalizeEmpty(strings.ToLower(stringValue(tracker, "state_label_prefix", cfg.Tracker.StateLabelPrefix)), cfg.Tracker.StateLabelPrefix)
		cfg.Tracker.PriorityLabelPrefix = normalizeEmpty(strings.ToLower(stringValue(tracker, "priority_label_prefix", cfg.Tracker.PriorityLabelPrefix)), cfg.Tracker.PriorityLabelPrefix)
		cfg.Tracker.BlockedByLabelPrefix = normalizeEmpty(strings.ToLower(stringValue(tracker, "blocked_by_label_prefix", cfg.Tracker.BlockedByLabelPrefix)), cfg.Tracker.BlockedByLabelPrefix)
		cfg.Tracker.CandidateLimit = positiveInt(intValue(tracker, "candidate_limit", cfg.Tracker.CandidateLimit), cfg.Tracker.CandidateLimit)
	}

	if polling := mapValue(raw, "polling"); polling != nil {
		cfg.Polling.IntervalMS = positiveInt(intValue(polling, "interval_ms", cfg.Polling.IntervalMS), cfg.Polling.IntervalMS)
	}

	if workspace := mapValue(raw, "workspace"); workspace != nil {
		cfg.Workspace.Root = resolvePathValue(stringValue(workspace, "root", cfg.Workspace.Root), cfg.Workspace.Root)
	}

	if hooks := mapValue(raw, "hooks"); hooks != nil {
		cfg.Hooks.AfterCreate = strings.TrimSpace(stringValue(hooks, "after_create", cfg.Hooks.AfterCreate))
		cfg.Hooks.BeforeRun = strings.TrimSpace(stringValue(hooks, "before_run", cfg.Hooks.BeforeRun))
		cfg.Hooks.AfterRun = strings.TrimSpace(stringValue(hooks, "after_run", cfg.Hooks.AfterRun))
		cfg.Hooks.BeforeRemove = strings.TrimSpace(stringValue(hooks, "before_remove", cfg.Hooks.BeforeRemove))
		cfg.Hooks.TimeoutMS = positiveInt(intValue(hooks, "timeout_ms", cfg.Hooks.TimeoutMS), cfg.Hooks.TimeoutMS)
	}

	if agent := mapValue(raw, "agent"); agent != nil {
		cfg.Agent.MaxConcurrentAgents = positiveInt(intValue(agent, "max_concurrent_agents", cfg.Agent.MaxConcurrentAgents), cfg.Agent.MaxConcurrentAgents)
		cfg.Agent.MaxRetryBackoffMS = positiveInt(intValue(agent, "max_retry_backoff_ms", cfg.Agent.MaxRetryBackoffMS), cfg.Agent.MaxRetryBackoffMS)
		cfg.Agent.MaxTurns = positiveInt(intValue(agent, "max_turns", cfg.Agent.MaxTurns), cfg.Agent.MaxTurns)
		cfg.Agent.MaxConcurrentAgentsByState = normalizeStateLimitMap(mapValue(agent, "max_concurrent_agents_by_state"))
	}

	if codex := mapValue(raw, "codex"); codex != nil {
		cfg.Runner.Command = strings.TrimSpace(stringValue(codex, "command", cfg.Runner.Command))
		cfg.Runner.TurnTimeoutMS = positiveInt(intValue(codex, "turn_timeout_ms", cfg.Runner.TurnTimeoutMS), cfg.Runner.TurnTimeoutMS)
		cfg.Runner.ReadTimeoutMS = positiveInt(intValue(codex, "read_timeout_ms", cfg.Runner.ReadTimeoutMS), cfg.Runner.ReadTimeoutMS)
		cfg.Runner.StallTimeoutMS = intValue(codex, "stall_timeout_ms", cfg.Runner.StallTimeoutMS)
	}

	if runner := mapValue(raw, "runner"); runner != nil {
		cfg.Runner.Command = strings.TrimSpace(stringValue(runner, "command", cfg.Runner.Command))
		cfg.Runner.PromptMode = normalizeEmpty(strings.ToLower(stringValue(runner, "prompt_mode", cfg.Runner.PromptMode)), cfg.Runner.PromptMode)
		cfg.Runner.OutputFormat = normalizeEmpty(strings.ToLower(stringValue(runner, "output_format", cfg.Runner.OutputFormat)), cfg.Runner.OutputFormat)
		cfg.Runner.Env = stringMapValue(runner, "env", cfg.Runner.Env)
		cfg.Runner.TurnTimeoutMS = positiveInt(intValue(runner, "turn_timeout_ms", cfg.Runner.TurnTimeoutMS), cfg.Runner.TurnTimeoutMS)
		cfg.Runner.ReadTimeoutMS = positiveInt(intValue(runner, "read_timeout_ms", cfg.Runner.ReadTimeoutMS), cfg.Runner.ReadTimeoutMS)
		cfg.Runner.StallTimeoutMS = intValue(runner, "stall_timeout_ms", cfg.Runner.StallTimeoutMS)
	}

	if cfg.PromptTemplate == "" {
		cfg.PromptTemplate = "You are working on issue {{ issue.identifier }}.\n\nTitle: {{ issue.title }}\n\nDescription:\n{{ issue.description }}"
	}

	if err := ValidateDispatchConfig(cfg); err != nil {
		return ServiceConfig{}, err
	}

	return cfg, nil
}

func ValidateDispatchConfig(cfg ServiceConfig) error {
	if strings.TrimSpace(cfg.Tracker.Kind) == "" {
		return ErrMissingTrackerKind
	}

	if !strings.EqualFold(cfg.Tracker.Kind, "github") {
		return fmt.Errorf("%w: %s", ErrUnsupportedTrackerKind, cfg.Tracker.Kind)
	}

	if strings.TrimSpace(cfg.Tracker.Repo) == "" {
		return ErrMissingTrackerRepo
	}

	if strings.TrimSpace(cfg.Runner.Command) == "" {
		return ErrMissingAgentCommand
	}

	return nil
}

func (cfg ServiceConfig) MaxConcurrentForState(state string) int {
	norm := NormalizeState(state)
	if norm == "" {
		return cfg.Agent.MaxConcurrentAgents
	}

	if limit, ok := cfg.Agent.MaxConcurrentAgentsByState[norm]; ok && limit > 0 {
		return limit
	}

	return cfg.Agent.MaxConcurrentAgents
}

func (cfg ServiceConfig) ActiveStateSet() map[string]struct{} {
	set := make(map[string]struct{}, len(cfg.Tracker.ActiveStates))
	for _, state := range cfg.Tracker.ActiveStates {
		norm := NormalizeState(state)
		if norm != "" {
			set[norm] = struct{}{}
		}
	}
	return set
}

func (cfg ServiceConfig) TerminalStateSet() map[string]struct{} {
	set := make(map[string]struct{}, len(cfg.Tracker.TerminalStates))
	for _, state := range cfg.Tracker.TerminalStates {
		norm := NormalizeState(state)
		if norm != "" {
			set[norm] = struct{}{}
		}
	}
	return set
}

func NormalizeState(state string) string {
	return strings.ToLower(strings.TrimSpace(state))
}

func normalizeStateLimitMap(raw map[string]any) map[string]int {
	limits := map[string]int{}
	for key, value := range raw {
		normKey := NormalizeState(key)
		if normKey == "" {
			continue
		}
		n, ok := anyToInt(value)
		if !ok || n <= 0 {
			continue
		}
		limits[normKey] = n
	}
	return limits
}

func mapValue(source map[string]any, key string) map[string]any {
	raw, ok := source[key]
	if !ok || raw == nil {
		return map[string]any{}
	}

	switch typed := raw.(type) {
	case map[string]any:
		return typed
	case map[any]any:
		out := make(map[string]any, len(typed))
		for mk, mv := range typed {
			out[fmt.Sprint(mk)] = mv
		}
		return out
	default:
		return map[string]any{}
	}
}

func stringValue(source map[string]any, key, fallback string) string {
	raw, ok := source[key]
	if !ok || raw == nil {
		return fallback
	}

	switch typed := raw.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return fallback
		}
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func intValue(source map[string]any, key string, fallback int) int {
	raw, ok := source[key]
	if !ok || raw == nil {
		return fallback
	}

	if n, ok := anyToInt(raw); ok {
		return n
	}

	return fallback
}

func anyToInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case uint:
		return int(typed), true
	case uint8:
		return int(typed), true
	case uint16:
		return int(typed), true
	case uint32:
		return int(typed), true
	case uint64:
		if typed > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int(typed), true
	case float32:
		return int(typed), true
	case float64:
		return int(typed), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func stringSliceValue(source map[string]any, key string, fallback []string) []string {
	raw, ok := source[key]
	if !ok || raw == nil {
		return append([]string(nil), fallback...)
	}

	vals, ok := raw.([]any)
	if !ok {
		if existing, ok := raw.([]string); ok {
			return append([]string(nil), existing...)
		}
		return append([]string(nil), fallback...)
	}

	out := make([]string, 0, len(vals))
	for _, item := range vals {
		text := strings.TrimSpace(fmt.Sprint(item))
		if text == "" {
			continue
		}
		out = append(out, text)
	}

	if len(out) == 0 {
		return append([]string(nil), fallback...)
	}
	return out
}

func stringMapValue(source map[string]any, key string, fallback map[string]string) map[string]string {
	raw, ok := source[key]
	if !ok || raw == nil {
		return cloneStringMap(fallback)
	}

	input := map[string]any{}
	switch typed := raw.(type) {
	case map[string]any:
		input = typed
	case map[any]any:
		for mk, mv := range typed {
			input[fmt.Sprint(mk)] = mv
		}
	default:
		return cloneStringMap(fallback)
	}

	out := map[string]string{}
	for mk, mv := range input {
		key := strings.TrimSpace(mk)
		if key == "" {
			continue
		}
		out[key] = resolveEnvString(fmt.Sprint(mv), "")
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func positiveInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func normalizeStates(states []string) []string {
	if len(states) == 0 {
		return states
	}
	out := make([]string, 0, len(states))
	seen := map[string]struct{}{}
	for _, state := range states {
		trimmed := strings.TrimSpace(state)
		if trimmed == "" {
			continue
		}
		norm := NormalizeState(trimmed)
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return states
	}
	return out
}

func normalizeEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func defaultWorkspaceRoot() string {
	return filepath.Join(os.TempDir(), "goji_workspaces")
}

func resolvePathValue(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}

	trimmed = resolveEnvString(trimmed, fallback)
	if strings.HasPrefix(trimmed, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			trimmed = strings.Replace(trimmed, "~", home, 1)
		}
	}

	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") || strings.HasPrefix(trimmed, "~") {
		return filepath.Clean(trimmed)
	}

	return trimmed
}

func resolveEnvString(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "$") {
		return trimmed
	}

	name := strings.TrimPrefix(trimmed, "$")
	if name == "" {
		return fallback
	}

	env := os.Getenv(name)
	if strings.TrimSpace(env) == "" {
		return fallback
	}
	return env
}
