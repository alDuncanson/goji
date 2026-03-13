package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"goji/internal/agent"
	"goji/internal/orchestrator"
	"goji/internal/tracker/github"
	"goji/internal/tui"
	"goji/internal/version"
	"goji/internal/workflow"
	"goji/internal/workspace"
)

func Run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			printUsage()
			return nil
		case "-v", "--version", "version":
			fmt.Println(version.String())
			return nil
		case "run":
			args = args[1:]
		}
	}

	return runCommand(args)
}

func runCommand(args []string) error {
	fs := flag.NewFlagSet("goji run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	workflowPath := fs.String("workflow", "WORKFLOW.md", "Path to WORKFLOW.md")
	noTUI := fs.Bool("no-tui", false, "Disable Bubble Tea TUI")
	logsRoot := fs.String("logs-root", "log", "Directory root for JSON logs")
	repoOverride := fs.String("repo", "", "GitHub repo override (owner/name)")
	agentCommandOverride := fs.String("agent-command", "", "Agent command override")

	if err := fs.Parse(args); err != nil {
		printUsage()
		return err
	}

	if fs.NArg() > 0 {
		*workflowPath = fs.Arg(0)
	}

	wfPath := filepath.Clean(*workflowPath)
	if _, err := os.Stat(wfPath); err != nil {
		return fmt.Errorf("workflow file not found: %s", wfPath)
	}

	logger, closer, err := setupLogger(*logsRoot)
	if err != nil {
		return err
	}
	defer closer()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store, err := workflow.NewStore(wfPath, logger)
	if err != nil {
		return err
	}
	store.Start(ctx)

	orch := orchestrator.New(
		logger,
		store,
		github.New(),
		workspace.NewManager(logger),
		agent.NewRunner(logger),
		orchestrator.Overrides{Repo: *repoOverride, AgentCommand: *agentCommandOverride},
	)

	if err := orch.Start(ctx); err != nil {
		return err
	}

	if *noTUI {
		logger.Info("goji running in headless mode", "workflow", wfPath)
		<-ctx.Done()
		orch.Wait()
		return nil
	}

	program := tea.NewProgram(tui.New(orch), tea.WithAltScreen())
	if _, err := program.Run(); err != nil && !errors.Is(err, context.Canceled) {
		cancel()
		orch.Wait()
		return err
	}

	cancel()
	orch.Wait()
	return nil
}

func setupLogger(logsRoot string) (*slog.Logger, func(), error) {
	if strings.TrimSpace(logsRoot) == "" {
		logsRoot = "log"
	}
	if err := os.MkdirAll(logsRoot, 0o755); err != nil {
		return nil, func() {}, fmt.Errorf("create logs root failed: %w", err)
	}

	filePath := filepath.Join(logsRoot, "goji.log")
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open log file failed: %w", err)
	}

	writer := io.MultiWriter(os.Stderr, file)
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger, func() { _ = file.Close() }, nil
}

func printUsage() {
	fmt.Print(`goji - Symphony-style issue orchestrator (GitHub + pluggable agent CLI)

Usage:
  goji run [flags] [WORKFLOW.md]
  goji --version

Flags:
  --workflow <path>         Path to workflow file (default: WORKFLOW.md)
  --no-tui                  Run without Bubble Tea dashboard
  --logs-root <path>        Log directory root (default: ./log)
  --repo <owner/name>       Override tracker.repo
  --agent-command <cmd>     Override runner/codex command
`)
}
