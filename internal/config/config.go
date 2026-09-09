// Package config 对应 trans_novel/config.py（主规格 §3 + 分册 01 §3）。
package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

//go:embed default_config.yaml
var defaultConfigYAML string

// DefaultConfigYAML 返回内嵌默认配置全文（_DEFAULT_CONFIG_YAML 等价物）。
func DefaultConfigYAML() string { return defaultConfigYAML }

// TierConfig 对应 TierConfig（extra="forbid"：tier 内未知键报错）。
type TierConfig struct {
	Model   *string
	Options map[string]any
}

// LLMConfig 对应 LLMConfig。
type LLMConfig struct {
	Provider       string
	BaseURL        *string
	APIKey         *string // 直存密钥（WebUI 配置；优先于 api_key_env）
	APIKeyEnv      *string
	ReasoningStyle string
	Timeout        int
	MaxRetries     int
	Tiers          map[string]*TierConfig
}

// SegmentConfig 对应 SegmentConfig。
type SegmentConfig struct {
	MaxCharsPerBatch   int
	MaxCharsPerSegment int
}

// PipelineConfig 对应 PipelineConfig。
type PipelineConfig struct {
	Review                        bool
	AlignRetryLimit               int
	Polish                        bool
	BacktranslateSample           float64
	ConsistencyQA                 bool
	RollingContextSegments        int
	BookUnderstanding             bool
	PrescanConcurrency            int
	AnnotationAlignment           bool
	ReviewConcurrency             int
	ReviewOutputRetries           int
	ReviewAgentLoop               bool
	ReviewAgentTier               string
	ReviewAgentMaxEvidenceRounds  int
	ReviewConflictArbitration     bool
	ReviewFixLoop                 bool
	ReviewFixMaxRounds            int
	ReviewCleanConfirmations      int
	GlossaryScope                 string
	StageTiers                    map[string]string // 环节 → 档位覆盖（key 见 validStageTierKeys）
}

// OutputConfig 对应 OutputConfig。
type OutputConfig struct {
	Mono                         bool
	Bilingual                    bool
	BilingualOrder               string
	BilingualPreserveSourceStyle bool
	AboutPage                    bool
}

// Config 对应顶层 Config。
type Config struct {
	SourceLang           string
	TargetLang           string
	LLM                  LLMConfig
	Segment              SegmentConfig
	Pipeline             PipelineConfig
	Output               OutputConfig
	HonorificStrategy    string
	PunctuationNormalize bool
	StateDir             string
}

// DefaultConfig 返回全默认值 Config。
func DefaultConfig() *Config {
	return &Config{
		SourceLang: "auto",
		TargetLang: "zh",
		LLM: LLMConfig{
			Provider: "deepseek", BaseURL: nil, APIKeyEnv: nil,
			ReasoningStyle: "none", Timeout: 600, MaxRetries: 4,
			Tiers: map[string]*TierConfig{},
		},
		Segment: SegmentConfig{MaxCharsPerBatch: 1800, MaxCharsPerSegment: 1200},
		Pipeline: PipelineConfig{
			Review: false, AlignRetryLimit: 2, Polish: true, BacktranslateSample: 0.0,
			ConsistencyQA: false, RollingContextSegments: 6, BookUnderstanding: true,
			PrescanConcurrency: 4, AnnotationAlignment: true, ReviewConcurrency: 4,
			ReviewOutputRetries: 2, ReviewAgentLoop: true, ReviewAgentTier: "strong",
			ReviewAgentMaxEvidenceRounds: 2, ReviewConflictArbitration: true,
			ReviewFixLoop: true, ReviewFixMaxRounds: 2, ReviewCleanConfirmations: 2,
			GlossaryScope: "chapter",
		},
		Output: OutputConfig{
			Mono: true, Bilingual: false, BilingualOrder: "target_first",
			BilingualPreserveSourceStyle: false, AboutPage: true,
		},
		HonorificStrategy:    "keep_style",
		PunctuationNormalize: true,
		StateDir:             "state",
	}
}

// ---- YAML / dict 取值辅助（宽容类型转换，等价 pydantic lax 语义的必要子集）----

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func getAny(m map[string]any, key string) any {
	if m == nil {
		return nil
	}
	return m[key]
}

func getStr(m map[string]any, key, def string) (string, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return def, false
	}
	switch t := v.(type) {
	case string:
		return t, true
	default:
		return fmt.Sprintf("%v", v), true
	}
}

func getInt(m map[string]any, key string, def int) (int, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return def, false
	}
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case uint64:
		return int(t), true
	case float64:
		return int(t), true
	case string:
		var n int
		if _, err := fmt.Sscanf(t, "%d", &n); err == nil {
			return n, true
		}
	}
	return def, false
}

func getFloat(m map[string]any, key string, def float64) (float64, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return def, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint64:
		return float64(t), true
	}
	return def, false
}

// pyBool 模拟 Python bool() 强转。
func pyBool(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return len(t) > 0
	case int:
		return t != 0
	case int64:
		return t != 0
	case uint64:
		return t != 0
	case float64:
		return t != 0
	default:
		return true
	}
}

func getBoolPy(m map[string]any, key string, def bool) bool {
	v, ok := m[key]
	if !ok {
		return def
	}
	return pyBool(v)
}

// ---- 校验 ----

var validReasoningStyles = map[string]bool{"none": true, "deepseek": true, "openai": true, "openrouter": true}
var validAgentTiers = map[string]bool{"strong": true, "cheap": true, "fast": true}

// 环节档位覆盖允许的环节 key（审校智能体三环节走既有 review_agent_tier，不在此列）。
var validStageTierKeys = map[string]bool{
	"translator": true, "polisher": true, "reviewer": true, "analyzer": true, "synopsizer": true,
	"glossary_extractor": true, "annotationaligner": true, "annotation_aligner": true,
	"back_translator": true, "backtranslator": true, "consistency_checker": true,
	"language_detect": true, "title_translate": true,
}

var stageTierKeyList = []string{
	"translator", "polisher", "reviewer", "analyzer", "synopsizer", "glossary_extractor",
	"annotationaligner", "annotation_aligner", "back_translator", "backtranslator",
	"consistency_checker", "language_detect", "title_translate",
}

// TierFor 环节档位覆盖：stage 为用量归因名（如 "Translator"/"language_detect"），
// 命中 stage_tiers 返回覆盖值，否则返回 def。CamelCase 自动转 snake 匹配。
func (p *PipelineConfig) TierFor(stage, def string) string {
	if len(p.StageTiers) == 0 {
		return def
	}
	if t, ok := p.StageTiers[stageTierKey(stage)]; ok {
		return t
	}
	return def
}

func stageTierKey(stage string) string {
	var b strings.Builder
	for i, r := range stage {
		if r >= 'A' && r <= 'Z' && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func inRange(name string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("pipeline.%s 必须在 %d..%d 之间（实际 %d）", name, lo, hi, v)
	}
	return nil
}

// parseTier 校验并构造 TierConfig（extra=forbid）。
func parseTier(name string, raw any) (*TierConfig, error) {
	m := asMap(raw)
	if m == nil {
		return nil, fmt.Errorf("llm.tiers.%s 必须是对象", name)
	}
	tc := &TierConfig{Options: map[string]any{}}
	for k, v := range m {
		switch k {
		case "model":
			if v == nil {
				tc.Model = nil
			} else if s, ok := v.(string); ok {
				tc.Model = &s
			} else {
				return nil, fmt.Errorf("llm.tiers.%s.model 必须是字符串", name)
			}
		case "options":
			om := asMap(v)
			if om == nil {
				return nil, fmt.Errorf("llm.tiers.%s.options 必须是对象", name)
			}
			tc.Options = om
		default:
			return nil, fmt.Errorf("llm.tiers.%s 含未知键 %q（允许：model/options）", name, k)
		}
	}
	return tc, nil
}

// FromDict 对应 Config.from_dict（主规格 §3.3 / 分册 01 §3.4）。
func FromDict(raw map[string]any) (*Config, error) {
	cfg := DefaultConfig()
	lang := asMap(getAny(raw, "language"))
	if lang != nil {
		cfg.SourceLang, _ = getStr(lang, "source", cfg.SourceLang)
		cfg.TargetLang, _ = getStr(lang, "target", cfg.TargetLang)
	}
	llmRaw := asMap(getAny(raw, "llm"))
	if llmRaw != nil {
		cfg.LLM.Provider, _ = getStr(llmRaw, "provider", cfg.LLM.Provider)
		if v, ok := llmRaw["base_url"]; ok {
			if v == nil {
				cfg.LLM.BaseURL = nil
			} else if s, ok2 := v.(string); ok2 {
				cfg.LLM.BaseURL = &s
			}
		}
		if v, ok := llmRaw["api_key"]; ok {
			if v == nil {
				cfg.LLM.APIKey = nil
			} else if s, ok2 := v.(string); ok2 {
				cfg.LLM.APIKey = &s
			}
		}
		if v, ok := llmRaw["api_key_env"]; ok {
			if v == nil {
				cfg.LLM.APIKeyEnv = nil
			} else if s, ok2 := v.(string); ok2 {
				cfg.LLM.APIKeyEnv = &s
			}
		}
		if s, ok := getStr(llmRaw, "reasoning_style", ""); ok {
			if !validReasoningStyles[s] {
				return nil, fmt.Errorf("llm.reasoning_style 非法值 %q（允许：none/deepseek/openai/openrouter）", s)
			}
			cfg.LLM.ReasoningStyle = s
		}
		cfg.LLM.Timeout, _ = getInt(llmRaw, "timeout", cfg.LLM.Timeout)
		cfg.LLM.MaxRetries, _ = getInt(llmRaw, "max_retries", cfg.LLM.MaxRetries)
		if tiersRaw := asMap(getAny(llmRaw, "tiers")); tiersRaw != nil {
			for name, t := range tiersRaw {
				tc, err := parseTier(name, t)
				if err != nil {
					return nil, err
				}
				cfg.LLM.Tiers[name] = tc
			}
		}
	}
	if seg := asMap(getAny(raw, "segment")); seg != nil {
		cfg.Segment.MaxCharsPerBatch, _ = getInt(seg, "max_chars_per_batch", cfg.Segment.MaxCharsPerBatch)
		cfg.Segment.MaxCharsPerSegment, _ = getInt(seg, "max_chars_per_segment", cfg.Segment.MaxCharsPerSegment)
	}
	if p := asMap(getAny(raw, "pipeline")); p != nil {
		pc := &cfg.Pipeline
		pc.Review = getBoolPy(p, "review", pc.Review)
		pc.AlignRetryLimit, _ = getInt(p, "align_retry_limit", pc.AlignRetryLimit)
		pc.Polish = getBoolPy(p, "polish", pc.Polish)
		pc.BacktranslateSample, _ = getFloat(p, "backtranslate_sample", pc.BacktranslateSample)
		pc.ConsistencyQA = getBoolPy(p, "consistency_qa", pc.ConsistencyQA)
		pc.RollingContextSegments, _ = getInt(p, "rolling_context_segments", pc.RollingContextSegments)
		pc.BookUnderstanding = getBoolPy(p, "book_understanding", pc.BookUnderstanding)
		pc.PrescanConcurrency, _ = getInt(p, "prescan_concurrency", pc.PrescanConcurrency)
		pc.AnnotationAlignment = getBoolPy(p, "annotation_alignment", pc.AnnotationAlignment)
		pc.ReviewConcurrency, _ = getInt(p, "review_concurrency", pc.ReviewConcurrency)
		if v, ok := getInt(p, "review_output_retries", pc.ReviewOutputRetries); ok {
			if err := inRange("review_output_retries", v, 0, 5); err != nil {
				return nil, err
			}
			pc.ReviewOutputRetries = v
		}
		pc.ReviewAgentLoop = getBoolPy(p, "review_agent_loop", pc.ReviewAgentLoop)
		if s, ok := getStr(p, "review_agent_tier", ""); ok {
			if !validAgentTiers[s] {
				return nil, fmt.Errorf("pipeline.review_agent_tier 非法值 %q（允许：strong/cheap/fast）", s)
			}
			pc.ReviewAgentTier = s
		}
		if v, ok := getInt(p, "review_agent_max_evidence_rounds", pc.ReviewAgentMaxEvidenceRounds); ok {
			if err := inRange("review_agent_max_evidence_rounds", v, 0, 2); err != nil {
				return nil, err
			}
			pc.ReviewAgentMaxEvidenceRounds = v
		}
		pc.ReviewConflictArbitration = getBoolPy(p, "review_conflict_arbitration", pc.ReviewConflictArbitration)
		pc.ReviewFixLoop = getBoolPy(p, "review_fix_loop", pc.ReviewFixLoop)
		if v, ok := getInt(p, "review_fix_max_rounds", pc.ReviewFixMaxRounds); ok {
			if err := inRange("review_fix_max_rounds", v, 0, 4); err != nil {
				return nil, err
			}
			pc.ReviewFixMaxRounds = v
		}
		if v, ok := getInt(p, "review_clean_confirmations", pc.ReviewCleanConfirmations); ok {
			if err := inRange("review_clean_confirmations", v, 1, 2); err != nil {
				return nil, err
			}
			pc.ReviewCleanConfirmations = v
		}
		pc.GlossaryScope, _ = getStr(p, "glossary_scope", pc.GlossaryScope)
		if st := asMap(getAny(p, "stage_tiers")); st != nil {
			pc.StageTiers = map[string]string{}
			for k, v := range st {
				if !validStageTierKeys[k] {
					return nil, fmt.Errorf("pipeline.stage_tiers 含未知环节 %q（允许：%s）", k, strings.Join(stageTierKeyList, "/"))
				}
				s, ok := v.(string)
				if !ok || !validAgentTiers[s] {
					return nil, fmt.Errorf("pipeline.stage_tiers.%s 非法值 %v（允许：strong/cheap/fast）", k, v)
				}
				pc.StageTiers[k] = s
			}
		}
	}
	if o := asMap(getAny(raw, "output")); o != nil {
		oc := &cfg.Output
		oc.Mono = getBoolPy(o, "mono", oc.Mono)
		oc.Bilingual = getBoolPy(o, "bilingual", oc.Bilingual)
		oc.BilingualOrder, _ = getStr(o, "bilingual_order", oc.BilingualOrder)
		oc.BilingualPreserveSourceStyle = getBoolPy(o, "bilingual_preserve_source_style", oc.BilingualPreserveSourceStyle)
		oc.AboutPage = getBoolPy(o, "about_page", oc.AboutPage)
	}
	if h := asMap(getAny(raw, "honorific")); h != nil {
		cfg.HonorificStrategy, _ = getStr(h, "strategy", cfg.HonorificStrategy)
	}
	if p := asMap(getAny(raw, "punctuation")); p != nil {
		cfg.PunctuationNormalize = getBoolPy(p, "normalize", true)
	}
	if paths := asMap(getAny(raw, "paths")); paths != nil {
		cfg.StateDir, _ = getStr(paths, "state_dir", cfg.StateDir)
	}
	return cfg, nil
}

// Load 读配置（YAML 或 JSON——JSON 为 YAML 子集，同一解析器兼容）→ FromDict；
// 空文件/纯注释 → 全默认。文件不存在或语法错误时原样上抛。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return FromDict(map[string]any{})
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("配置文件顶层必须是对象")
	}
	return FromDict(m)
}

// ResolveDefaultPath 无显式 -c 时的配置解析链：
// exe 同目录 config.json → exe 同目录 config.yaml → CWD config.yaml（旧行为兜底）。
func ResolveDefaultPath() string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		for _, name := range []string{"config.json", "config.yaml"} {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	return "config.yaml"
}

// DefaultConfigJSON 默认配置的 JSON 形态（由默认 YAML 转换，保证内容一致）。
func DefaultConfigJSON() string {
	var v any
	if err := yaml.Unmarshal([]byte(defaultConfigYAML), &v); err != nil || v == nil {
		return "{}"
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b) + "\n"
}

// CreateDefaultFile 对应 Config.create_default_file：独占模式原子创建默认配置；
// 已存在返回 false。父目录不存在时递归创建。
func CreateDefaultFile(path string) (bool, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	content := defaultConfigYAML
	if strings.EqualFold(filepath.Ext(path), ".json") {
		content = DefaultConfigJSON()
	}
	if _, err := f.WriteString(content); err != nil {
		return false, err
	}
	return true, nil
}
