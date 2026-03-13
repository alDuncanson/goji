package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"goji/internal/agent"
	"goji/internal/config"
	"goji/internal/model"
	"goji/internal/prompt"
	"goji/internal/tracker"
	"goji/internal/workflow"
	"goji/internal/workspace"
)

const (
	continuationRetryDelay = time.Second
	heartbeatInterval      = 250 * time.Millisecond
	maxEvents              = 100
)

type Overrides struct {
	Repo         string
	AgentCommand string
}

type Orchestrator struct {
	logger    *slog.Logger
	workflow  *workflow.Store
	tracker   tracker.Client
	workspace *workspace.Manager
	runner    *agent.Runner
	overrides Overrides

	refreshCh  chan struct{}
	updateCh   chan workerUpdate
	doneCh     chan workerDone
	snapshotCh chan snapshotRequest
	startedCh  chan struct{}
	stoppedCh  chan struct{}
}

type workerUpdate struct {
	IssueID       string
	IssueIdentity string
	WorkspacePath string
	Update        model.AgentUpdate
}

type workerDone struct {
	IssueID       string
	IssueIdentity string
	WorkspacePath string
	Err           error
}

type snapshotRequest struct {
	resp chan Snapshot
}

type Snapshot struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Running     []RunningSnapshot    `json:"running"`
	Retrying    []RetrySnapshot      `json:"retrying"`
	Totals      model.TokenTotals    `json:"codex_totals"`
	RateLimits  map[string]any       `json:"rate_limits,omitempty"`
	Polling     PollingSnapshot      `json:"polling"`
	Events      []model.RuntimeEvent `json:"events"`
	Counts      map[string]int       `json:"counts"`
}

type RunningSnapshot struct {
	IssueID        string    `json:"issue_id"`
	Identifier     string    `json:"issue_identifier"`
	State          string    `json:"state"`
	WorkspacePath  string    `json:"workspace_path"`
	SessionID      string    `json:"session_id,omitempty"`
	ThreadID       string    `json:"thread_id,omitempty"`
	TurnID         string    `json:"turn_id,omitempty"`
	AgentPID       int       `json:"agent_pid,omitempty"`
	LastEvent      string    `json:"last_event,omitempty"`
	LastMessage    string    `json:"last_message,omitempty"`
	StartedAt      time.Time `json:"started_at"`
	LastEventAt    time.Time `json:"last_event_at,omitempty"`
	TurnCount      int       `json:"turn_count"`
	RetryAttempt   int       `json:"retry_attempt"`
	InputTokens    int       `json:"input_tokens"`
	OutputTokens   int       `json:"output_tokens"`
	TotalTokens    int       `json:"total_tokens"`
	RuntimeSeconds int       `json:"runtime_seconds"`
}

type RetrySnapshot struct {
	IssueID    string    `json:"issue_id"`
	Identifier string    `json:"issue_identifier"`
	Attempt    int       `json:"attempt"`
	DueAt      time.Time `json:"due_at"`
	DueInMS    int       `json:"due_in_ms"`
	Error      string    `json:"error,omitempty"`
}

type PollingSnapshot struct {
	Checking       bool `json:"checking"`
	NextPollInMS   int  `json:"next_poll_in_ms"`
	PollIntervalMS int  `json:"poll_interval_ms"`
}

type state struct {
	cfg           config.ServiceConfig
	pollInterval  time.Duration
	nextPollDueAt time.Time
	pollingNow    bool
	running       map[string]*runningEntry
	retrying      map[string]*retryEntry
	claimed       map[string]struct{}
	completed     map[string]struct{}
	totals        model.TokenTotals
	rateLimits    map[string]any
	events        []model.RuntimeEvent
	lastKnownErr  error
}

type runningEntry struct {
	issue              model.Issue
	identifier         string
	workspacePath      string
	startedAt          time.Time
	lastEventAt        time.Time
	lastEvent          string
	lastMessage        string
	sessionID          string
	threadID           string
	turnID             string
	agentPID           int
	turnCount          int
	retryAttempt       int
	inputTokens        int
	outputTokens       int
	totalTokens        int
	lastReportedInput  int
	lastReportedOutput int
	lastReportedTotal  int
	cancel             context.CancelFunc
}

type retryEntry struct {
	issueID    string
	identifier string
	attempt    int
	dueAt      time.Time
	error      string
}

func New(logger *slog.Logger, wf *workflow.Store, tr tracker.Client, ws *workspace.Manager, runner *agent.Runner, overrides Overrides) *Orchestrator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Orchestrator{
		logger:     logger,
		workflow:   wf,
		tracker:    tr,
		workspace:  ws,
		runner:     runner,
		overrides:  overrides,
		refreshCh:  make(chan struct{}, 1),
		updateCh:   make(chan workerUpdate, 256),
		doneCh:     make(chan workerDone, 128),
		snapshotCh: make(chan snapshotRequest, 8),
		startedCh:  make(chan struct{}),
		stoppedCh:  make(chan struct{}),
	}
}

func (o *Orchestrator) Start(ctx context.Context) error {
	cfg, err := o.loadConfig()
	if err != nil {
		return err
	}

	st := state{
		cfg:           cfg,
		pollInterval:  time.Duration(cfg.Polling.IntervalMS) * time.Millisecond,
		nextPollDueAt: time.Now().UTC(),
		running:       map[string]*runningEntry{},
		retrying:      map[string]*retryEntry{},
		claimed:       map[string]struct{}{},
		completed:     map[string]struct{}{},
		totals:        model.TokenTotals{},
	}

	o.startupTerminalCleanup(ctx, st.cfg)
	go o.loop(ctx, st)
	<-o.startedCh
	return nil
}

func (o *Orchestrator) Wait() {
	<-o.stoppedCh
}

func (o *Orchestrator) RequestRefresh() {
	select {
	case o.refreshCh <- struct{}{}:
	default:
	}
}

func (o *Orchestrator) Snapshot(ctx context.Context) (Snapshot, error) {
	resp := make(chan Snapshot, 1)
	select {
	case o.snapshotCh <- snapshotRequest{resp: resp}:
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	}

	select {
	case snap := <-resp:
		return snap, nil
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	}
}

func (o *Orchestrator) loop(ctx context.Context, st state) {
	close(o.startedCh)
	defer close(o.stoppedCh)

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			o.shutdownRunning(st)
			return
		case <-ticker.C:
			now := time.Now().UTC()
			st = o.handleDueRetries(ctx, st, now)
			if !st.pollingNow && (st.nextPollDueAt.IsZero() || !now.Before(st.nextPollDueAt)) {
				st = o.runTick(ctx, st, now)
			}
		case <-o.refreshCh:
			st.nextPollDueAt = time.Now().UTC()
			o.recordEvent(&st, "info", "refresh_requested", "manual refresh requested", "", "")
		case update := <-o.updateCh:
			st = o.applyWorkerUpdate(st, update)
		case done := <-o.doneCh:
			st = o.handleWorkerDone(ctx, st, done)
		case req := <-o.snapshotCh:
			req.resp <- o.makeSnapshot(st)
		}
	}
}

func (o *Orchestrator) runTick(ctx context.Context, st state, now time.Time) state {
	cfg, err := o.loadConfig()
	if err != nil {
		st.lastKnownErr = err
		o.recordEvent(&st, "error", "config_reload_failed", err.Error(), "", "")
		o.logger.Error("config reload failed; keeping last known good config", "error", err)
		cfg = st.cfg
	} else {
		st.cfg = cfg
		st.pollInterval = time.Duration(cfg.Polling.IntervalMS) * time.Millisecond
	}

	st.pollingNow = true
	st.nextPollDueAt = time.Time{}

	st = o.reconcileRunning(ctx, st)

	if err := config.ValidateDispatchConfig(st.cfg); err != nil {
		o.recordEvent(&st, "error", "dispatch_validation_failed", err.Error(), "", "")
		o.logger.Error("dispatch validation failed", "error", err)
		st.pollingNow = false
		st.nextPollDueAt = now.Add(st.pollInterval)
		return st
	}

	issues, err := o.tracker.FetchCandidateIssues(ctx, st.cfg)
	if err != nil {
		o.recordEvent(&st, "error", "candidate_fetch_failed", err.Error(), "", "")
		o.logger.Error("candidate issue fetch failed", "error", err)
		st.pollingNow = false
		st.nextPollDueAt = now.Add(st.pollInterval)
		return st
	}

	sortForDispatch(issues)
	for _, issue := range issues {
		if o.availableSlots(st) <= 0 {
			break
		}
		if !o.shouldDispatch(issue, st) {
			continue
		}
		st = o.dispatchIssue(ctx, st, issue, nil)
	}

	st.pollingNow = false
	st.nextPollDueAt = now.Add(st.pollInterval)
	return st
}

func (o *Orchestrator) reconcileRunning(ctx context.Context, st state) state {
	if len(st.running) == 0 {
		return st
	}

	st = o.reconcileStalled(ctx, st)
	if len(st.running) == 0 {
		return st
	}

	ids := make([]string, 0, len(st.running))
	for issueID := range st.running {
		ids = append(ids, issueID)
	}

	issues, err := o.tracker.FetchIssueStatesByIDs(ctx, st.cfg, ids)
	if err != nil {
		o.logger.Warn("running state refresh failed; keeping workers active", "error", err)
		o.recordEvent(&st, "warn", "running_state_refresh_failed", err.Error(), "", "")
		return st
	}

	lookup := map[string]model.Issue{}
	for _, issue := range issues {
		lookup[issue.ID] = issue
	}

	active := st.cfg.ActiveStateSet()
	terminal := st.cfg.TerminalStateSet()

	for issueID, entry := range st.running {
		issue, ok := lookup[issueID]
		if !ok {
			o.recordEvent(&st, "info", "running_issue_missing", "running issue no longer visible in tracker; stopping worker", issueID, entry.identifier)
			st = o.terminateRunningIssue(ctx, st, issueID, false, false, "")
			continue
		}

		norm := config.NormalizeState(issue.State)
		if _, ok := terminal[norm]; ok {
			o.recordEvent(&st, "info", "running_issue_terminal", "running issue moved to terminal state; stopping worker and cleaning workspace", issue.ID, issue.Identifier)
			st = o.terminateRunningIssue(ctx, st, issueID, true, false, "")
			continue
		}
		if _, ok := active[norm]; !ok {
			o.recordEvent(&st, "info", "running_issue_non_active", "running issue moved to non-active state; stopping worker", issue.ID, issue.Identifier)
			st = o.terminateRunningIssue(ctx, st, issueID, false, false, "")
			continue
		}

		entry.issue = issue
		entry.identifier = issue.Identifier
	}

	return st
}

func (o *Orchestrator) reconcileStalled(ctx context.Context, st state) state {
	stallTimeout := st.cfg.Runner.StallTimeoutMS
	if stallTimeout <= 0 {
		return st
	}
	now := time.Now().UTC()
	for issueID, entry := range st.running {
		last := entry.startedAt
		if !entry.lastEventAt.IsZero() {
			last = entry.lastEventAt
		}
		if now.Sub(last) <= time.Duration(stallTimeout)*time.Millisecond {
			continue
		}

		message := fmt.Sprintf("stalled for %s without agent activity", now.Sub(last).Truncate(time.Second))
		o.recordEvent(&st, "warn", "worker_stalled", message, issueID, entry.identifier)
		st = o.terminateRunningIssue(ctx, st, issueID, false, true, message)
	}
	return st
}

func (o *Orchestrator) dispatchIssue(ctx context.Context, st state, issue model.Issue, attempt *int) state {
	if !o.shouldDispatch(issue, st) {
		return st
	}

	// Revalidate right before dispatch to reduce stale launches.
	refreshed, err := o.tracker.FetchIssueStatesByIDs(ctx, st.cfg, []string{issue.ID})
	if err == nil && len(refreshed) > 0 {
		issue = refreshed[0]
	}
	if !o.isCandidate(issue, st.cfg) || o.todoBlockedByNonTerminal(issue, st.cfg) {
		return st
	}

	workerCtx, cancel := context.WithCancel(ctx)
	retryAttempt := 0
	if attempt != nil && *attempt > 0 {
		retryAttempt = *attempt
	}

	entry := &runningEntry{
		issue:        issue,
		identifier:   issue.Identifier,
		startedAt:    time.Now().UTC(),
		retryAttempt: retryAttempt,
		cancel:       cancel,
	}
	st.running[issue.ID] = entry
	st.claimed[issue.ID] = struct{}{}
	delete(st.retrying, issue.ID)

	o.recordEvent(&st, "info", "issue_dispatched", "issue dispatched to agent", issue.ID, issue.Identifier)

	go o.runWorker(workerCtx, st.cfg, issue, attempt)
	return st
}

func (o *Orchestrator) runWorker(ctx context.Context, cfg config.ServiceConfig, issue model.Issue, attempt *int) {
	info, err := o.workspace.CreateForIssue(ctx, cfg, issue)
	if err != nil {
		o.doneCh <- workerDone{IssueID: issue.ID, IssueIdentity: issue.Identifier, Err: fmt.Errorf("workspace_create_failed: %w", err)}
		return
	}

	o.updateCh <- workerUpdate{
		IssueID:       issue.ID,
		IssueIdentity: issue.Identifier,
		WorkspacePath: info.Path,
		Update: model.AgentUpdate{
			Event:     "workspace_ready",
			Timestamp: time.Now().UTC(),
			Message:   info.Path,
		},
	}

	if err := o.workspace.RunBeforeRunHook(ctx, cfg, info.Path, issue); err != nil {
		o.doneCh <- workerDone{IssueID: issue.ID, IssueIdentity: issue.Identifier, WorkspacePath: info.Path, Err: fmt.Errorf("before_run_failed: %w", err)}
		return
	}
	defer o.workspace.RunAfterRunHook(context.Background(), cfg, info.Path, issue)

	currentIssue := issue
	for turn := 1; turn <= cfg.Agent.MaxTurns; turn++ {
		turnPrompt, err := buildTurnPrompt(cfg, currentIssue, attempt, turn)
		if err != nil {
			o.doneCh <- workerDone{IssueID: issue.ID, IssueIdentity: issue.Identifier, WorkspacePath: info.Path, Err: err}
			return
		}

		_, err = o.runner.RunTurn(ctx, cfg, agent.TurnInput{
			Issue:         currentIssue,
			WorkspacePath: info.Path,
			Prompt:        turnPrompt,
			Attempt:       attempt,
			TurnNumber:    turn,
		}, func(update model.AgentUpdate) {
			o.updateCh <- workerUpdate{IssueID: issue.ID, IssueIdentity: issue.Identifier, WorkspacePath: info.Path, Update: update}
		})
		if err != nil {
			o.doneCh <- workerDone{IssueID: issue.ID, IssueIdentity: issue.Identifier, WorkspacePath: info.Path, Err: err}
			return
		}

		refreshed, err := o.tracker.FetchIssueStatesByIDs(ctx, cfg, []string{currentIssue.ID})
		if err != nil {
			o.doneCh <- workerDone{IssueID: issue.ID, IssueIdentity: issue.Identifier, WorkspacePath: info.Path, Err: fmt.Errorf("issue_state_refresh_failed: %w", err)}
			return
		}
		if len(refreshed) > 0 {
			currentIssue = refreshed[0]
		}

		if !stateInSet(currentIssue.State, cfg.ActiveStateSet()) {
			break
		}
	}

	o.doneCh <- workerDone{IssueID: issue.ID, IssueIdentity: issue.Identifier, WorkspacePath: info.Path, Err: nil}
}

func buildTurnPrompt(cfg config.ServiceConfig, issue model.Issue, attempt *int, turn int) (string, error) {
	if turn == 1 {
		return prompt.Render(cfg.PromptTemplate, issue, attempt)
	}

	return fmt.Sprintf("Continuation turn %d for issue %s (%s). Resume from current workspace state and complete remaining active work.", turn, issue.Identifier, issue.Title), nil
}

func (o *Orchestrator) applyWorkerUpdate(st state, event workerUpdate) state {
	entry, ok := st.running[event.IssueID]
	if !ok {
		return st
	}

	entry.workspacePath = event.WorkspacePath
	entry.lastEventAt = event.Update.Timestamp
	entry.lastEvent = event.Update.Event
	if strings.TrimSpace(event.Update.Message) != "" {
		entry.lastMessage = strings.TrimSpace(event.Update.Message)
	}
	if event.Update.SessionID != "" {
		entry.sessionID = event.Update.SessionID
	}
	if event.Update.ThreadID != "" {
		entry.threadID = event.Update.ThreadID
	}
	if event.Update.TurnID != "" {
		entry.turnID = event.Update.TurnID
	}
	if event.Update.AgentPID != 0 {
		entry.agentPID = event.Update.AgentPID
	}
	if event.Update.Event == "session_started" {
		entry.turnCount++
	}

	inDelta := tokenDelta(event.Update.InputTokens, entry.lastReportedInput)
	outDelta := tokenDelta(event.Update.OutputTokens, entry.lastReportedOutput)
	totalDelta := tokenDelta(event.Update.TotalTokens, entry.lastReportedTotal)

	entry.inputTokens += inDelta
	entry.outputTokens += outDelta
	entry.totalTokens += totalDelta
	entry.lastReportedInput = maxInt(entry.lastReportedInput, event.Update.InputTokens)
	entry.lastReportedOutput = maxInt(entry.lastReportedOutput, event.Update.OutputTokens)
	entry.lastReportedTotal = maxInt(entry.lastReportedTotal, event.Update.TotalTokens)

	st.totals.InputTokens += inDelta
	st.totals.OutputTokens += outDelta
	st.totals.TotalTokens += totalDelta

	if len(event.Update.RateLimits) > 0 {
		st.rateLimits = cloneMap(event.Update.RateLimits)
	}

	o.recordEvent(&st, "debug", event.Update.Event, truncate(event.Update.Message, 180), event.IssueID, entry.identifier)
	return st
}

func (o *Orchestrator) handleWorkerDone(ctx context.Context, st state, done workerDone) state {
	entry, ok := st.running[done.IssueID]
	if !ok {
		return st
	}

	delete(st.running, done.IssueID)
	runtimeSeconds := int(time.Since(entry.startedAt).Seconds())
	if runtimeSeconds > 0 {
		st.totals.SecondsRunning += runtimeSeconds
	}

	if done.Err == nil {
		st.completed[done.IssueID] = struct{}{}
		st = o.scheduleRetry(st, done.IssueID, 1, done.IssueIdentity, "", true)
		o.recordEvent(&st, "info", "worker_completed", "worker exited normally; queued continuation retry", done.IssueID, entry.identifier)
		return st
	}

	nextAttempt := 1
	if entry.retryAttempt > 0 {
		nextAttempt = entry.retryAttempt + 1
	}
	st = o.scheduleRetry(st, done.IssueID, nextAttempt, done.IssueIdentity, done.Err.Error(), false)
	o.recordEvent(&st, "warn", "worker_failed", done.Err.Error(), done.IssueID, entry.identifier)
	return st
}

func (o *Orchestrator) terminateRunningIssue(ctx context.Context, st state, issueID string, cleanupWorkspace bool, retry bool, reason string) state {
	entry, ok := st.running[issueID]
	if !ok {
		delete(st.claimed, issueID)
		delete(st.retrying, issueID)
		return st
	}

	if entry.cancel != nil {
		entry.cancel()
	}
	delete(st.running, issueID)

	runtimeSeconds := int(time.Since(entry.startedAt).Seconds())
	if runtimeSeconds > 0 {
		st.totals.SecondsRunning += runtimeSeconds
	}

	if cleanupWorkspace {
		if err := o.workspace.RemoveIssueWorkspace(ctx, st.cfg, entry.identifier); err != nil {
			o.logger.Warn("workspace cleanup failed", "issue_id", issueID, "issue_identifier", entry.identifier, "error", err)
		}
	}

	if retry {
		nextAttempt := 1
		if entry.retryAttempt > 0 {
			nextAttempt = entry.retryAttempt + 1
		}
		st = o.scheduleRetry(st, issueID, nextAttempt, entry.identifier, reason, false)
	} else {
		delete(st.claimed, issueID)
		delete(st.retrying, issueID)
	}

	return st
}

func (o *Orchestrator) scheduleRetry(st state, issueID string, attempt int, identifier string, errText string, continuation bool) state {
	delay := retryDelay(attempt, st.cfg.Agent.MaxRetryBackoffMS, continuation)
	entry := &retryEntry{
		issueID:    issueID,
		identifier: identifier,
		attempt:    attempt,
		dueAt:      time.Now().UTC().Add(delay),
		error:      errText,
	}
	st.retrying[issueID] = entry
	st.claimed[issueID] = struct{}{}
	return st
}

func retryDelay(attempt int, maxBackoffMS int, continuation bool) time.Duration {
	if continuation && attempt == 1 {
		return continuationRetryDelay
	}
	if attempt <= 0 {
		attempt = 1
	}
	if maxBackoffMS <= 0 {
		maxBackoffMS = config.DefaultMaxRetryBackoffMS
	}
	delay := 10 * time.Second
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= time.Duration(maxBackoffMS)*time.Millisecond {
			return time.Duration(maxBackoffMS) * time.Millisecond
		}
	}
	maxDelay := time.Duration(maxBackoffMS) * time.Millisecond
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func (o *Orchestrator) handleDueRetries(ctx context.Context, st state, now time.Time) state {
	if len(st.retrying) == 0 {
		return st
	}

	due := make([]*retryEntry, 0)
	for _, entry := range st.retrying {
		if !now.Before(entry.dueAt) {
			due = append(due, entry)
		}
	}
	if len(due) == 0 {
		return st
	}

	for _, entry := range due {
		delete(st.retrying, entry.issueID)
		st = o.handleRetry(ctx, st, entry)
	}
	return st
}

func (o *Orchestrator) handleRetry(ctx context.Context, st state, entry *retryEntry) state {
	issues, err := o.tracker.FetchCandidateIssues(ctx, st.cfg)
	if err != nil {
		st = o.scheduleRetry(st, entry.issueID, entry.attempt+1, entry.identifier, "retry poll failed: "+err.Error(), false)
		return st
	}

	var target *model.Issue
	for idx := range issues {
		if issues[idx].ID == entry.issueID {
			target = &issues[idx]
			break
		}
	}
	if target == nil {
		delete(st.claimed, entry.issueID)
		return st
	}

	if !o.shouldDispatch(*target, st) {
		st = o.scheduleRetry(st, entry.issueID, entry.attempt+1, target.Identifier, "no available orchestrator slots", false)
		return st
	}

	attempt := entry.attempt
	return o.dispatchIssue(ctx, st, *target, &attempt)
}

func (o *Orchestrator) shouldDispatch(issue model.Issue, st state) bool {
	if !o.isCandidate(issue, st.cfg) {
		return false
	}
	if o.todoBlockedByNonTerminal(issue, st.cfg) {
		return false
	}
	if _, ok := st.claimed[issue.ID]; ok {
		return false
	}
	if _, ok := st.running[issue.ID]; ok {
		return false
	}
	if o.availableSlots(st) <= 0 {
		return false
	}
	if !o.stateSlotsAvailable(issue.State, st) {
		return false
	}
	return true
}

func (o *Orchestrator) isCandidate(issue model.Issue, cfg config.ServiceConfig) bool {
	if !issue.HasRequiredDispatchFields() {
		return false
	}
	active := cfg.ActiveStateSet()
	terminal := cfg.TerminalStateSet()
	norm := config.NormalizeState(issue.State)
	if _, ok := active[norm]; !ok {
		return false
	}
	if _, ok := terminal[norm]; ok {
		return false
	}
	return true
}

func (o *Orchestrator) todoBlockedByNonTerminal(issue model.Issue, cfg config.ServiceConfig) bool {
	if config.NormalizeState(issue.State) != "todo" {
		return false
	}
	if len(issue.BlockedBy) == 0 {
		return false
	}
	terminal := cfg.TerminalStateSet()
	for _, blocker := range issue.BlockedBy {
		if blocker.State == "" {
			return true
		}
		if _, ok := terminal[config.NormalizeState(blocker.State)]; !ok {
			return true
		}
	}
	return false
}

func (o *Orchestrator) availableSlots(st state) int {
	remaining := st.cfg.Agent.MaxConcurrentAgents - len(st.running)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (o *Orchestrator) stateSlotsAvailable(stateName string, st state) bool {
	limit := st.cfg.MaxConcurrentForState(stateName)
	if limit <= 0 {
		return false
	}
	used := 0
	norm := config.NormalizeState(stateName)
	for _, entry := range st.running {
		if config.NormalizeState(entry.issue.State) == norm {
			used++
		}
	}
	return used < limit
}

func (o *Orchestrator) makeSnapshot(st state) Snapshot {
	now := time.Now().UTC()
	running := make([]RunningSnapshot, 0, len(st.running))
	for issueID, entry := range st.running {
		lastAt := entry.lastEventAt
		runtimeSeconds := int(now.Sub(entry.startedAt).Seconds())
		if runtimeSeconds < 0 {
			runtimeSeconds = 0
		}
		running = append(running, RunningSnapshot{
			IssueID:        issueID,
			Identifier:     entry.identifier,
			State:          entry.issue.State,
			WorkspacePath:  entry.workspacePath,
			SessionID:      entry.sessionID,
			ThreadID:       entry.threadID,
			TurnID:         entry.turnID,
			AgentPID:       entry.agentPID,
			LastEvent:      entry.lastEvent,
			LastMessage:    entry.lastMessage,
			StartedAt:      entry.startedAt,
			LastEventAt:    lastAt,
			TurnCount:      entry.turnCount,
			RetryAttempt:   entry.retryAttempt,
			InputTokens:    entry.inputTokens,
			OutputTokens:   entry.outputTokens,
			TotalTokens:    entry.totalTokens,
			RuntimeSeconds: runtimeSeconds,
		})
	}
	sort.Slice(running, func(i, j int) bool { return running[i].Identifier < running[j].Identifier })

	retrying := make([]RetrySnapshot, 0, len(st.retrying))
	for _, entry := range st.retrying {
		dueIn := int(entry.dueAt.Sub(now).Milliseconds())
		if dueIn < 0 {
			dueIn = 0
		}
		retrying = append(retrying, RetrySnapshot{
			IssueID:    entry.issueID,
			Identifier: entry.identifier,
			Attempt:    entry.attempt,
			DueAt:      entry.dueAt,
			DueInMS:    dueIn,
			Error:      entry.error,
		})
	}
	sort.Slice(retrying, func(i, j int) bool { return retrying[i].DueInMS < retrying[j].DueInMS })

	nextPollIn := int(st.nextPollDueAt.Sub(now).Milliseconds())
	if nextPollIn < 0 {
		nextPollIn = 0
	}

	totals := st.totals
	for _, entry := range st.running {
		runtime := int(now.Sub(entry.startedAt).Seconds())
		if runtime > 0 {
			totals.SecondsRunning += runtime
		}
	}

	events := append([]model.RuntimeEvent(nil), st.events...)
	return Snapshot{
		GeneratedAt: now,
		Running:     running,
		Retrying:    retrying,
		Totals:      totals,
		RateLimits:  cloneMap(st.rateLimits),
		Polling: PollingSnapshot{
			Checking:       st.pollingNow,
			NextPollInMS:   nextPollIn,
			PollIntervalMS: int(st.pollInterval / time.Millisecond),
		},
		Events: events,
		Counts: map[string]int{
			"running":  len(running),
			"retrying": len(retrying),
		},
	}
}

func (o *Orchestrator) loadConfig() (config.ServiceConfig, error) {
	def, wfErr := o.workflow.Current()
	if wfErr != nil {
		o.logger.Warn("workflow watcher has pending reload error", "error", wfErr)
	}
	cfg, err := config.Parse(def.Config, def.PromptTemplate)
	if err != nil {
		return config.ServiceConfig{}, err
	}
	if strings.TrimSpace(o.overrides.Repo) != "" {
		cfg.Tracker.Repo = strings.TrimSpace(o.overrides.Repo)
	}
	if strings.TrimSpace(o.overrides.AgentCommand) != "" {
		cfg.Runner.Command = strings.TrimSpace(o.overrides.AgentCommand)
	}
	return cfg, nil
}

func (o *Orchestrator) startupTerminalCleanup(ctx context.Context, cfg config.ServiceConfig) {
	issues, err := o.tracker.FetchIssuesByStates(ctx, cfg, cfg.Tracker.TerminalStates)
	if err != nil {
		o.logger.Warn("startup terminal cleanup skipped; unable to fetch terminal issues", "error", err)
		return
	}
	for _, issue := range issues {
		if issue.Identifier == "" {
			continue
		}
		if err := o.workspace.RemoveIssueWorkspace(ctx, cfg, issue.Identifier); err != nil {
			o.logger.Warn("terminal workspace cleanup failed", "issue_identifier", issue.Identifier, "error", err)
		}
	}
}

func (o *Orchestrator) shutdownRunning(st state) {
	for _, entry := range st.running {
		if entry.cancel != nil {
			entry.cancel()
		}
	}
}

func (o *Orchestrator) recordEvent(st *state, level, eventType, msg, issueID, identifier string) {
	e := model.RuntimeEvent{
		At:      time.Now().UTC(),
		Level:   level,
		Type:    eventType,
		IssueID: issueID,
		Issue:   identifier,
		Message: msg,
	}
	st.events = append(st.events, e)
	if len(st.events) > maxEvents {
		st.events = append([]model.RuntimeEvent(nil), st.events[len(st.events)-maxEvents:]...)
	}

	attrs := []any{"event", eventType, "message", msg}
	if issueID != "" {
		attrs = append(attrs, "issue_id", issueID)
	}
	if identifier != "" {
		attrs = append(attrs, "issue_identifier", identifier)
	}

	switch level {
	case "error":
		o.logger.Error(msg, attrs...)
	case "warn":
		o.logger.Warn(msg, attrs...)
	default:
		o.logger.Info(msg, attrs...)
	}
}

func sortForDispatch(issues []model.Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		left := issues[i]
		right := issues[j]
		if priorityRank(left.Priority) != priorityRank(right.Priority) {
			return priorityRank(left.Priority) < priorityRank(right.Priority)
		}
		if createdSortKey(left.CreatedAt) != createdSortKey(right.CreatedAt) {
			return createdSortKey(left.CreatedAt) < createdSortKey(right.CreatedAt)
		}
		return left.Identifier < right.Identifier
	})
}

func priorityRank(priority *int) int {
	if priority != nil && *priority >= 1 && *priority <= 4 {
		return *priority
	}
	return 5
}

func createdSortKey(ts *time.Time) int64 {
	if ts == nil {
		return int64(^uint64(0) >> 1)
	}
	return ts.UTC().UnixMicro()
}

func stateInSet(state string, set map[string]struct{}) bool {
	_, ok := set[config.NormalizeState(state)]
	return ok
}

func tokenDelta(next, prev int) int {
	if next <= 0 || next < prev {
		return 0
	}
	return next - prev
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func truncate(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= limit {
		return trimmed
	}
	return trimmed[:limit] + "..."
}

var errOrchestratorUnavailable = errors.New("orchestrator unavailable")

func (o *Orchestrator) MustSnapshot() Snapshot {
	snap, err := o.Snapshot(context.Background())
	if err != nil {
		return Snapshot{GeneratedAt: time.Now().UTC()}
	}
	return snap
}

func (o *Orchestrator) IsUnavailable(err error) bool {
	return errors.Is(err, errOrchestratorUnavailable)
}
