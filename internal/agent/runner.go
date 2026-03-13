package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"goji/internal/config"
	"goji/internal/model"
)

const maxLineBytes = 10 * 1024 * 1024

type Runner struct {
	logger *slog.Logger
}

func NewRunner(logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{logger: logger}
}

type TurnInput struct {
	Issue         model.Issue
	WorkspacePath string
	Prompt        string
	Attempt       *int
	TurnNumber    int
}

type TurnResult struct {
	ExitCode int
	Duration time.Duration
}

func (r *Runner) RunTurn(ctx context.Context, cfg config.ServiceConfig, input TurnInput, onUpdate func(model.AgentUpdate)) (TurnResult, error) {
	timeout := time.Duration(cfg.Runner.TurnTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(config.DefaultTurnTimeoutMS) * time.Millisecond
	}

	turnCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if onUpdate == nil {
		onUpdate = func(model.AgentUpdate) {}
	}

	promptFile := filepath.Join(input.WorkspacePath, ".goji_prompt.txt")
	if err := os.WriteFile(promptFile, []byte(input.Prompt), 0o644); err != nil {
		return TurnResult{}, fmt.Errorf("write prompt file failed: %w", err)
	}

	cmd := exec.CommandContext(turnCtx, "sh", "-lc", cfg.Runner.Command)
	cmd.Dir = input.WorkspacePath
	cmd.Env = append(os.Environ(), buildRunnerEnv(cfg, input, promptFile)...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return TurnResult{}, fmt.Errorf("stdout pipe failed: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return TurnResult{}, fmt.Errorf("stderr pipe failed: %w", err)
	}

	if strings.EqualFold(cfg.Runner.PromptMode, "stdin") {
		stdinPipe, pipeErr := cmd.StdinPipe()
		if pipeErr != nil {
			return TurnResult{}, fmt.Errorf("stdin pipe failed: %w", pipeErr)
		}
		go func() {
			_, _ = io.WriteString(stdinPipe, input.Prompt)
			_ = stdinPipe.Close()
		}()
	}

	start := time.Now().UTC()
	if err := cmd.Start(); err != nil {
		return TurnResult{}, fmt.Errorf("agent command start failed: %w", err)
	}

	onUpdate(model.AgentUpdate{
		Event:     "session_started",
		Timestamp: start,
		AgentPID:  cmd.Process.Pid,
		Message:   fmt.Sprintf("started turn %d", input.TurnNumber),
	})

	var wg sync.WaitGroup
	stdoutErr := make(chan error, 1)
	stderrErr := make(chan error, 1)

	wg.Add(1)
	go func() {
		defer wg.Done()
		stdoutErr <- r.consumeOutput(stdoutPipe, cfg.Runner.OutputFormat, cmd.Process.Pid, onUpdate)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		stderrErr <- consumeStderr(stderrPipe, cmd.Process.Pid, onUpdate)
	}()

	waitErr := cmd.Wait()
	wg.Wait()

	select {
	case err := <-stdoutErr:
		if err != nil {
			r.logger.Warn("stdout parsing failed", "error", err)
		}
	default:
	}
	select {
	case err := <-stderrErr:
		if err != nil {
			r.logger.Warn("stderr parsing failed", "error", err)
		}
	default:
	}

	duration := time.Since(start)
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	if waitErr != nil {
		if errors.Is(turnCtx.Err(), context.DeadlineExceeded) {
			onUpdate(model.AgentUpdate{Event: "turn_timeout", Timestamp: time.Now().UTC(), AgentPID: cmd.Process.Pid})
			return TurnResult{ExitCode: exitCode, Duration: duration}, fmt.Errorf("turn_timeout")
		}

		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode = status.ExitStatus()
			}
		}
		onUpdate(model.AgentUpdate{Event: "turn_failed", Timestamp: time.Now().UTC(), AgentPID: cmd.Process.Pid, Message: waitErr.Error()})
		return TurnResult{ExitCode: exitCode, Duration: duration}, fmt.Errorf("turn_failed: %w", waitErr)
	}

	onUpdate(model.AgentUpdate{Event: "turn_completed", Timestamp: time.Now().UTC(), AgentPID: cmd.Process.Pid})
	return TurnResult{ExitCode: exitCode, Duration: duration}, nil
}

func (r *Runner) consumeOutput(reader io.Reader, format string, pid int, onUpdate func(model.AgentUpdate)) error {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxLineBytes)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		update := parseOutputLine(line, format)
		update.Timestamp = time.Now().UTC()
		update.AgentPID = pid
		onUpdate(update)
	}
	return scanner.Err()
}

func consumeStderr(reader io.Reader, pid int, onUpdate func(model.AgentUpdate)) error {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxLineBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		onUpdate(model.AgentUpdate{
			Event:     "stderr",
			Timestamp: time.Now().UTC(),
			AgentPID:  pid,
			Message:   truncate(line, 1024),
		})
	}
	return scanner.Err()
}

func buildRunnerEnv(cfg config.ServiceConfig, input TurnInput, promptFile string) []string {
	env := []string{
		"GOJI_ISSUE_ID=" + input.Issue.ID,
		"GOJI_ISSUE_IDENTIFIER=" + input.Issue.Identifier,
		"GOJI_ISSUE_NUMBER=" + strconv.Itoa(input.Issue.Number),
		"GOJI_ISSUE_TITLE=" + input.Issue.Title,
		"GOJI_ISSUE_STATE=" + input.Issue.State,
		"GOJI_ISSUE_URL=" + input.Issue.URL,
		"GOJI_WORKSPACE=" + input.WorkspacePath,
		"GOJI_PROMPT_FILE=" + promptFile,
		"GOJI_PROMPT=" + input.Prompt,
		"GOJI_OUTPUT_FORMAT=" + cfg.Runner.OutputFormat,
		"GOJI_TURN_NUMBER=" + strconv.Itoa(input.TurnNumber),
	}

	if input.Attempt != nil {
		env = append(env, "GOJI_ATTEMPT="+strconv.Itoa(*input.Attempt))
	} else {
		env = append(env, "GOJI_ATTEMPT=")
	}

	for key, value := range cfg.Runner.Env {
		env = append(env, key+"="+value)
	}

	return env
}

func parseOutputLine(line, format string) model.AgentUpdate {
	if strings.EqualFold(format, "plain") {
		return model.AgentUpdate{Event: "output", Message: truncate(line, 1024)}
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return model.AgentUpdate{Event: "output", Message: truncate(line, 1024)}
	}

	update := model.AgentUpdate{
		Event:   firstString(payload, "event", "type", "method"),
		Message: firstString(payload, "message", "text", "content"),
		Raw:     payload,
	}
	if update.Event == "" {
		update.Event = "notification"
	}

	threadID := findString(payload, "thread_id", "threadId")
	turnID := findString(payload, "turn_id", "turnId")
	sessionID := findString(payload, "session_id", "sessionId")
	if sessionID == "" && threadID != "" && turnID != "" {
		sessionID = threadID + "-" + turnID
	}
	update.ThreadID = threadID
	update.TurnID = turnID
	update.SessionID = sessionID

	in, out, total := extractUsage(payload)
	update.InputTokens = in
	update.OutputTokens = out
	update.TotalTokens = total
	if limits := extractRateLimits(payload); len(limits) > 0 {
		update.RateLimits = limits
	}

	if strings.Contains(strings.ToLower(update.Event), "input_required") {
		update.InputRequired = true
	}
	if strings.Contains(strings.ToLower(update.Event), "approval") {
		update.ApprovalRequired = true
	}
	if strings.Contains(strings.ToLower(update.Event), "unsupported") && strings.Contains(strings.ToLower(update.Event), "tool") {
		update.UnsupportedTool = true
	}

	return update
}

func findString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if val, ok := payload[key]; ok {
			if text, ok := val.(string); ok {
				if strings.TrimSpace(text) != "" {
					return strings.TrimSpace(text)
				}
			}
		}
	}

	for _, key := range keys {
		for _, nested := range []string{"data", "payload", "result", "params", "msg", "info"} {
			if nestedMap, ok := payload[nested].(map[string]any); ok {
				if text := findString(nestedMap, key); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if val, ok := payload[key]; ok {
			if text, ok := val.(string); ok {
				text = strings.TrimSpace(text)
				if text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func extractUsage(payload map[string]any) (int, int, int) {
	candidates := []map[string]any{payload}
	for _, key := range []string{"usage", "tokenUsage", "rate_usage", "payload", "result", "data", "info", "msg", "params"} {
		if nested, ok := payload[key].(map[string]any); ok {
			candidates = append(candidates, nested)
		}
	}

	best := [3]int{}
	for _, candidate := range candidates {
		in := intFromAny(firstAny(candidate,
			"input_tokens", "inputTokens", "prompt_tokens", "promptTokens", "input"))
		out := intFromAny(firstAny(candidate,
			"output_tokens", "outputTokens", "completion_tokens", "completionTokens", "output"))
		total := intFromAny(firstAny(candidate,
			"total_tokens", "totalTokens", "total"))

		if total == 0 {
			total = in + out
		}
		if total > best[2] {
			best = [3]int{in, out, total}
		}
	}
	return best[0], best[1], best[2]
}

func extractRateLimits(payload map[string]any) map[string]any {
	for _, key := range []string{"rate_limits", "rateLimits"} {
		if nested, ok := payload[key].(map[string]any); ok {
			return nested
		}
	}
	for _, key := range []string{"payload", "data", "result", "info", "params"} {
		if nested, ok := payload[key].(map[string]any); ok {
			if limits := extractRateLimits(nested); len(limits) > 0 {
				return limits
			}
		}
	}
	return nil
}

func firstAny(payload map[string]any, keys ...string) any {
	for _, key := range keys {
		if val, ok := payload[key]; ok {
			return val
		}
	}
	return nil
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return n
		}
	}
	return 0
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
