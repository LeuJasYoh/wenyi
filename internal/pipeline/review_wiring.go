package pipeline

// review_wiring.go 阶段 4 接线：RunReviewFunc → review.RunReviewSession。

import (
	"wenyi/internal/agents"
	"wenyi/internal/config"
	"wenyi/internal/glossary"
	"wenyi/internal/review"
)

func init() {
	RunReviewFunc = runReviewSessionBridge
}

func runReviewSessionBridge(o *Orchestrator, store *RunStore, terms []*glossary.Term, progress ProgressFn) (*review.Outcome, error) {
	deps := review.SessionDeps{
		Store:  store,
		RunDir: store.RunDir,
		Client:   o.Client,
		Config:   o.Config,
		Analyzer: o.Analyzer,
		FlushUsage: func(scope string) (map[string]any, error) {
			return o.FlushUsage(store, scope)
		},
		LogEvent: func(event string, data map[string]any) error {
			return store.LogEvent(event, data)
		},
	}
	outcome, err := review.RunReviewSession(terms, deps, func(done, total int, label string) {
		if progress != nil {
			progress(done, total, label)
		}
	})
	if err != nil {
		return nil, err
	}
	return outcome, nil
}

// 确保 config/agents 依赖被引用（编译期自检）。
var _ = config.DefaultConfig
var _ = agents.NewAnalyzer
