package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 迁移自 tests/test_config.py::TestConfigFileCreation（断言语义等价）。

func TestCreateDefaultFileCanBeLoaded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.yaml")
	created, err := CreateDefaultFile(path)
	if err != nil {
		t.Fatalf("CreateDefaultFile: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !created {
		t.Errorf("期望 created=true")
	}
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		t.Errorf("期望文件已创建")
	}
	if cfg.LLM.Provider != "deepseek" {
		t.Errorf("provider = %q", cfg.LLM.Provider)
	}
	if cfg.LLM.BaseURL == nil || *cfg.LLM.BaseURL != "https://api.deepseek.com" {
		t.Errorf("base_url = %v", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKeyEnv == nil || *cfg.LLM.APIKeyEnv != "DEEPSEEK_API_KEY" {
		t.Errorf("api_key_env = %v", cfg.LLM.APIKeyEnv)
	}
	wantTiers := map[string]bool{"strong": true, "cheap": true, "fast": true}
	if len(cfg.LLM.Tiers) != 3 {
		t.Fatalf("tiers 数量 = %d", len(cfg.LLM.Tiers))
	}
	for name := range wantTiers {
		if _, ok := cfg.LLM.Tiers[name]; !ok {
			t.Errorf("缺少 tier %q", name)
		}
	}
	if m := cfg.LLM.Tiers["strong"].Model; m == nil || *m != "deepseek-v4-pro" {
		t.Errorf("strong.model = %v", m)
	}
	if m := cfg.LLM.Tiers["cheap"].Model; m == nil || *m != "deepseek-v4-flash" {
		t.Errorf("cheap.model = %v", m)
	}
	if m := cfg.LLM.Tiers["fast"].Model; m == nil || *m != "deepseek-v4-flash" {
		t.Errorf("fast.model = %v", m)
	}
	if thinking, _ := cfg.LLM.Tiers["fast"].Options["thinking"].(bool); thinking {
		t.Errorf("内嵌默认的 fast.thinking 应为 false")
	}
	generated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	g := string(generated)
	for _, want := range []string{
		"# trans-novel 配置",
		"  base_url: https://api.deepseek.com",
		"  api_key_env: DEEPSEEK_API_KEY",
		"  tiers:\n",
		"output:\n",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("生成的配置缺少 %q", want)
		}
	}
	if !cfg.Output.Mono {
		t.Errorf("output.mono 应为 true")
	}
	if cfg.Output.Bilingual {
		t.Errorf("output.bilingual 应为 false")
	}
	if cfg.Output.BilingualOrder != "target_first" {
		t.Errorf("bilingual_order = %q", cfg.Output.BilingualOrder)
	}
	if cfg.Output.BilingualPreserveSourceStyle {
		t.Errorf("bilingual_preserve_source_style 应为 false")
	}
	if !cfg.Output.AboutPage {
		t.Errorf("about_page 应为 true")
	}
	if cfg.Pipeline.Review {
		t.Errorf("pipeline.review 应为 false")
	}
	if !cfg.Pipeline.Polish {
		t.Errorf("pipeline.polish 应为 true")
	}
	if cfg.Pipeline.BacktranslateSample != 0.0 {
		t.Errorf("backtranslate_sample = %v", cfg.Pipeline.BacktranslateSample)
	}
	if cfg.Pipeline.ConsistencyQA {
		t.Errorf("consistency_qa 应为 false")
	}
	if !cfg.Pipeline.AnnotationAlignment {
		t.Errorf("annotation_alignment 应为 true")
	}
	if cfg.Pipeline.ReviewConcurrency != 4 {
		t.Errorf("review_concurrency = %d", cfg.Pipeline.ReviewConcurrency)
	}
	if cfg.Pipeline.ReviewOutputRetries != 2 {
		t.Errorf("review_output_retries = %d", cfg.Pipeline.ReviewOutputRetries)
	}
	if !cfg.Pipeline.ReviewAgentLoop {
		t.Errorf("review_agent_loop 应为 true")
	}
	if cfg.Pipeline.ReviewAgentTier != "strong" {
		t.Errorf("review_agent_tier = %q", cfg.Pipeline.ReviewAgentTier)
	}
	if cfg.Pipeline.ReviewAgentMaxEvidenceRounds != 2 {
		t.Errorf("review_agent_max_evidence_rounds = %d", cfg.Pipeline.ReviewAgentMaxEvidenceRounds)
	}
	if !cfg.Pipeline.ReviewConflictArbitration {
		t.Errorf("review_conflict_arbitration 应为 true")
	}
	if !cfg.Pipeline.ReviewFixLoop {
		t.Errorf("review_fix_loop 应为 true")
	}
	if cfg.Pipeline.ReviewFixMaxRounds != 2 {
		t.Errorf("review_fix_max_rounds = %d", cfg.Pipeline.ReviewFixMaxRounds)
	}
	if cfg.Pipeline.ReviewCleanConfirmations != 2 {
		t.Errorf("review_clean_confirmations = %d", cfg.Pipeline.ReviewCleanConfirmations)
	}
}

func TestLoadNeverOverwritesExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "language:\n  source: en\n  target: zh\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SourceLang != "en" {
		t.Errorf("source_lang = %q", cfg.SourceLang)
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Errorf("已有配置被改写")
	}
}

func TestPartialConfigUsesYAMLPipelineDefaults(t *testing.T) {
	cfg, err := FromDict(map[string]any{"pipeline": map[string]any{"review": false}})
	if err != nil {
		t.Fatalf("FromDict: %v", err)
	}
	if cfg.Pipeline.Review {
		t.Errorf("review 应为 false")
	}
	if !cfg.Pipeline.Polish || cfg.Pipeline.BacktranslateSample != 0.0 || cfg.Pipeline.ConsistencyQA ||
		!cfg.Pipeline.AnnotationAlignment || cfg.Pipeline.ReviewConcurrency != 4 ||
		cfg.Pipeline.ReviewOutputRetries != 2 || !cfg.Pipeline.ReviewAgentLoop ||
		cfg.Pipeline.ReviewAgentTier != "strong" || cfg.Pipeline.ReviewAgentMaxEvidenceRounds != 2 ||
		!cfg.Pipeline.ReviewConflictArbitration || !cfg.Pipeline.ReviewFixLoop ||
		cfg.Pipeline.ReviewFixMaxRounds != 2 || cfg.Pipeline.ReviewCleanConfirmations != 2 {
		t.Errorf("流水线默认值不符： %+v", cfg.Pipeline)
	}
}

func TestAboutPageCanBeDisabled(t *testing.T) {
	cfg, err := FromDict(map[string]any{"output": map[string]any{"about_page": false}})
	if err != nil {
		t.Fatalf("FromDict: %v", err)
	}
	if cfg.Output.AboutPage {
		t.Errorf("about_page 应为 false")
	}
}

func TestCompatibleReasoningStyleIsLoaded(t *testing.T) {
	cfg, err := FromDict(map[string]any{
		"llm": map[string]any{"provider": "openai-compatible", "reasoning_style": "deepseek"},
	})
	if err != nil {
		t.Fatalf("FromDict: %v", err)
	}
	if cfg.LLM.ReasoningStyle != "deepseek" {
		t.Errorf("reasoning_style = %q", cfg.LLM.ReasoningStyle)
	}
}
