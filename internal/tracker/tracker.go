package tracker

import (
	"context"

	"goji/internal/config"
	"goji/internal/model"
)

// Client defines tracker read operations required by orchestration.
type Client interface {
	FetchCandidateIssues(ctx context.Context, cfg config.ServiceConfig) ([]model.Issue, error)
	FetchIssuesByStates(ctx context.Context, cfg config.ServiceConfig, states []string) ([]model.Issue, error)
	FetchIssueStatesByIDs(ctx context.Context, cfg config.ServiceConfig, issueIDs []string) ([]model.Issue, error)
}
