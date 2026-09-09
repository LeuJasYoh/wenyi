package config

import (
	"encoding/json"
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

func TestAPIKeyDirectStorage(t *testing.T) {
	cfg, err := FromDict(map[string]any{
		"llm": map[string]any{"provider": "deepseek", "api_key": "sk-test-123"},
	})
	if err != nil {
		t.Fatalf("FromDict: %v", err)
	}
	if cfg.LLM.APIKey == nil || *cfg.LLM.APIKey != "sk-test-123" {
		t.Fatalf("api_key 应直存，实际 %v", cfg.LLM.APIKey)
	}
	// JSON 配置同样解析（JSON 是 YAML 子集）
	jsonCfg, err := Load(writeTemp(t, `{"llm":{"provider":"deepseek","api_key":"sk-json"}}`))
	if err != nil {
		t.Fatalf("Load JSON: %v", err)
	}
	if jsonCfg.LLM.APIKey == nil || *jsonCfg.LLM.APIKey != "sk-json" {
		t.Fatalf("JSON api_key 应直存，实际 %v", jsonCfg.LLM.APIKey)
	}
}

func TestStageTiersOverride(t *testing.T) {
	cfg, err := FromDict(map[string]any{
		"pipeline": map[string]any{"stage_tiers": map[string]any{
			"translator": "cheap", "glossary_extractor": "strong", "language_detect": "fast",
		}},
	})
	if err != nil {
		t.Fatalf("FromDict: %v", err)
	}
	if got := cfg.Pipeline.TierFor("Translator", "strong"); got != "cheap" {
		t.Errorf("Translator 覆盖应生效，实际 %q", got)
	}
	if got := cfg.Pipeline.TierFor("GlossaryExtractor", "fast"); got != "strong" {
		t.Errorf("GlossaryExtractor 覆盖应生效，实际 %q", got)
	}
	if got := cfg.Pipeline.TierFor("language_detect", "cheap"); got != "fast" {
		t.Errorf("language_detect 覆盖应生效，实际 %q", got)
	}
	if got := cfg.Pipeline.TierFor("Polisher", "strong"); got != "strong" {
		t.Errorf("未覆盖环节应回落默认，实际 %q", got)
	}
	// 未知环节 key 报错
	if _, err := FromDict(map[string]any{
		"pipeline": map[string]any{"stage_tiers": map[string]any{"bogus_stage": "cheap"}},
	}); err == nil {
		t.Error("未知环节应报错")
	}
	// 非法档位报错
	if _, err := FromDict(map[string]any{
		"pipeline": map[string]any{"stage_tiers": map[string]any{"translator": "mega"}},
	}); err == nil {
		t.Error("非法档位应报错")
	}
}

func TestDefaultConfigJSONRoundTrip(t *testing.T) {
	js := DefaultConfigJSON()
	cfg, err := Load(writeTemp(t, js))
	if err != nil {
		t.Fatalf("默认 JSON 应可加载：%v\n%s", err, js)
	}
	yamlCfg, err := Load(writeTemp(t, DefaultConfigYAML()))
	if err != nil {
		t.Fatalf("默认 YAML 应可加载：%v", err)
	}
	if cfg.LLM.Provider != yamlCfg.LLM.Provider || cfg.Pipeline.Polish != yamlCfg.Pipeline.Polish ||
		cfg.Segment.MaxCharsPerBatch != yamlCfg.Segment.MaxCharsPerBatch {
		t.Fatal("默认 JSON 与 YAML 内容应等价")
	}
}

func TestCreateDefaultFileJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	created, err := CreateDefaultFile(p)
	if err != nil || !created {
		t.Fatalf("CreateDefaultFile(json)：%v created=%v", err, created)
	}
	var v any
	if err := json.Unmarshal([]byte(mustRead(t, p)), &v); err != nil {
		t.Fatalf("生成的应为合法 JSON：%v", err)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.tmp")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
