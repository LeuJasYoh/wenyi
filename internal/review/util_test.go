package review

import (
	"wenyi/internal/agents"
	"wenyi/internal/config"
	"wenyi/internal/llm"
)

func agentsNewAnalyzer(client llm.LLMClient, cfg *config.Config) *agents.Analyzer {
	return agents.NewAnalyzer(client, cfg)
}
