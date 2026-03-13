package workflow

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

const defaultPollInterval = time.Second

// Store keeps the last known-good workflow in memory and hot-reloads on change.
type Store struct {
	path    string
	poll    time.Duration
	logger  *slog.Logger
	load    func(string) (Definition, error)
	mu      sync.RWMutex
	current Definition
	stamp   string
	lastErr error
	started bool
}

func NewStore(path string, logger *slog.Logger) (*Store, error) {
	if logger == nil {
		logger = slog.Default()
	}

	def, err := Load(path)
	if err != nil {
		return nil, err
	}

	stamp, err := fileStamp(path)
	if err != nil {
		return nil, err
	}

	return &Store{
		path:    path,
		poll:    defaultPollInterval,
		logger:  logger,
		load:    Load,
		current: def,
		stamp:   stamp,
	}, nil
}

func (s *Store) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()

	go s.loop(ctx)
}

func (s *Store) Current() (Definition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current, s.lastErr
}

func (s *Store) loop(ctx context.Context) {
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.maybeReload()
		}
	}
}

func (s *Store) maybeReload() {
	stamp, err := fileStamp(s.path)
	if err != nil {
		s.setError(err)
		s.logger.Warn("workflow reload check failed", "path", s.path, "error", err)
		return
	}

	s.mu.RLock()
	unchanged := stamp == s.stamp
	s.mu.RUnlock()
	if unchanged {
		return
	}

	def, err := s.load(s.path)
	if err != nil {
		s.setError(err)
		s.logger.Error("workflow reload failed; keeping last known good", "path", s.path, "error", err)
		return
	}

	s.mu.Lock()
	s.current = def
	s.stamp = stamp
	s.lastErr = nil
	s.mu.Unlock()

	s.logger.Info("workflow reloaded", "path", s.path)
}

func (s *Store) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = err
}

func fileStamp(path string) (string, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	hash := sha1.Sum(content)
	return fmt.Sprintf("%d:%d:%s", stat.ModTime().UnixNano(), stat.Size(), hex.EncodeToString(hash[:])), nil
}
