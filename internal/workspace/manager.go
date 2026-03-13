package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"goji/internal/config"
	"goji/internal/model"
)

var workspaceKeySanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]`)

type Info struct {
	Path         string
	WorkspaceKey string
	CreatedNow   bool
}

// Manager handles workspace lifecycle and safety invariants.
type Manager struct {
	logger *slog.Logger
}

func NewManager(logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{logger: logger}
}

func (m *Manager) CreateForIssue(ctx context.Context, cfg config.ServiceConfig, issue model.Issue) (Info, error) {
	key := SanitizeKey(issue.Identifier)
	root, err := filepath.Abs(cfg.Workspace.Root)
	if err != nil {
		return Info{}, fmt.Errorf("workspace root abs failed: %w", err)
	}

	workspacePath := filepath.Join(root, key)
	if err := ensurePathUnderRoot(root, workspacePath); err != nil {
		return Info{}, err
	}

	createdNow := false
	stat, err := os.Stat(workspacePath)
	switch {
	case err == nil && stat.IsDir():
		createdNow = false
	case err == nil && !stat.IsDir():
		if rmErr := os.RemoveAll(workspacePath); rmErr != nil {
			return Info{}, fmt.Errorf("remove stale workspace file failed: %w", rmErr)
		}
		if mkErr := os.MkdirAll(workspacePath, 0o755); mkErr != nil {
			return Info{}, fmt.Errorf("workspace create failed: %w", mkErr)
		}
		createdNow = true
	case errors.Is(err, os.ErrNotExist):
		if mkErr := os.MkdirAll(workspacePath, 0o755); mkErr != nil {
			return Info{}, fmt.Errorf("workspace create failed: %w", mkErr)
		}
		createdNow = true
	default:
		return Info{}, fmt.Errorf("workspace stat failed: %w", err)
	}

	if createdNow && strings.TrimSpace(cfg.Hooks.AfterCreate) != "" {
		if err := m.runHook(ctx, cfg, workspacePath, issue, "after_create", cfg.Hooks.AfterCreate, true); err != nil {
			return Info{}, err
		}
	}

	return Info{Path: workspacePath, WorkspaceKey: key, CreatedNow: createdNow}, nil
}

func (m *Manager) RunBeforeRunHook(ctx context.Context, cfg config.ServiceConfig, workspacePath string, issue model.Issue) error {
	if strings.TrimSpace(cfg.Hooks.BeforeRun) == "" {
		return nil
	}
	return m.runHook(ctx, cfg, workspacePath, issue, "before_run", cfg.Hooks.BeforeRun, true)
}

func (m *Manager) RunAfterRunHook(ctx context.Context, cfg config.ServiceConfig, workspacePath string, issue model.Issue) {
	if strings.TrimSpace(cfg.Hooks.AfterRun) == "" {
		return
	}
	if _, err := os.Stat(workspacePath); errors.Is(err, os.ErrNotExist) {
		return
	}
	if err := m.runHook(ctx, cfg, workspacePath, issue, "after_run", cfg.Hooks.AfterRun, false); err != nil {
		m.logger.Warn("after_run hook failed", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "error", err)
	}
}

func (m *Manager) RemoveIssueWorkspace(ctx context.Context, cfg config.ServiceConfig, issueIdentifier string) error {
	key := SanitizeKey(issueIdentifier)
	root, err := filepath.Abs(cfg.Workspace.Root)
	if err != nil {
		return err
	}
	workspacePath := filepath.Join(root, key)
	return m.RemoveWorkspace(ctx, cfg, workspacePath)
}

func (m *Manager) RemoveWorkspace(ctx context.Context, cfg config.ServiceConfig, workspacePath string) error {
	root, err := filepath.Abs(cfg.Workspace.Root)
	if err != nil {
		return err
	}
	if err := ensurePathUnderRoot(root, workspacePath); err != nil {
		return err
	}

	if strings.TrimSpace(cfg.Hooks.BeforeRemove) != "" {
		dummy := model.Issue{Identifier: filepath.Base(workspacePath)}
		if err := m.runHook(ctx, cfg, workspacePath, dummy, "before_remove", cfg.Hooks.BeforeRemove, false); err != nil {
			m.logger.Warn("before_remove hook failed", "workspace", workspacePath, "error", err)
		}
	}

	if err := os.RemoveAll(workspacePath); err != nil {
		return fmt.Errorf("remove workspace failed: %w", err)
	}
	return nil
}

func (m *Manager) runHook(ctx context.Context, cfg config.ServiceConfig, workspacePath string, issue model.Issue, hookName string, script string, fatal bool) error {
	timeout := time.Duration(cfg.Hooks.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(config.DefaultHookTimeoutMS) * time.Millisecond
	}

	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	m.logger.Info("running workspace hook", "hook", hookName, "issue_id", issue.ID, "issue_identifier", issue.Identifier, "workspace", workspacePath)

	cmd := exec.CommandContext(hookCtx, "sh", "-lc", script)
	cmd.Dir = workspacePath

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if hookCtx.Err() == context.DeadlineExceeded {
		hookErr := fmt.Errorf("workspace_hook_timeout: %s", hookName)
		if fatal {
			return hookErr
		}
		m.logger.Warn("workspace hook timed out", "hook", hookName, "issue_id", issue.ID, "issue_identifier", issue.Identifier)
		return hookErr
	}

	if err != nil {
		hookErr := fmt.Errorf("workspace_hook_failed: %s: %w", hookName, err)
		trimmed := truncateForLog(stdout.String()+stderr.String(), 2048)
		if fatal {
			return fmt.Errorf("%w output=%q", hookErr, trimmed)
		}
		m.logger.Warn("workspace hook failed", "hook", hookName, "issue_id", issue.ID, "issue_identifier", issue.Identifier, "output", trimmed)
		return hookErr
	}
	return nil
}

func ensurePathUnderRoot(root, workspacePath string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("workspace root invalid: %w", err)
	}
	absPath, err := filepath.Abs(workspacePath)
	if err != nil {
		return fmt.Errorf("workspace path invalid: %w", err)
	}

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return fmt.Errorf("workspace path relation failed: %w", err)
	}

	if rel == "." {
		return fmt.Errorf("workspace path equals root: %s", absPath)
	}

	if strings.HasPrefix(rel, "..") || strings.HasPrefix(filepath.ToSlash(rel), "../") {
		return fmt.Errorf("workspace path escapes root: workspace=%s root=%s", absPath, absRoot)
	}

	return nil
}

func SanitizeKey(identifier string) string {
	trimmed := strings.TrimSpace(identifier)
	if trimmed == "" {
		trimmed = "issue"
	}
	return workspaceKeySanitizer.ReplaceAllString(trimmed, "_")
}

func truncateForLog(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "... (truncated)"
}
