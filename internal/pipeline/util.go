package pipeline

import (
	"os"
	"time"

	"wenyi/internal/config"
	"wenyi/internal/llm"
	"wenyi/internal/llmfactory"
)

// buildClientFor 包装 llmfactory（避免 orchestrator 直接依赖 provider 细节）。
func buildClientFor(cfg *config.Config) (llm.LLMClient, error) {
	return llmfactory.BuildClient(cfg)
}

func mkdtemp(prefix string) (string, error) {
	return os.MkdirTemp("", prefix)
}

func removeAll(path string) { _ = os.RemoveAll(path) }

func ensureDir(path string) error { return os.MkdirAll(path, 0o755) }

// timeSleep 测试辅助（可被短路径覆盖）。


var timeSleep = defaultSleep

func defaultSleep(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

// loadDocHelper 测试辅助。



