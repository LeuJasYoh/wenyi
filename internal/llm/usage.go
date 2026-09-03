package llm

import (
	"math"
	"sync"
)

// UsageSample 一次调用的用量快照（主规格 §5.6）。
type UsageSample struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	CacheHitTokens   int64
	CacheMissTokens  int64
}

// UsageFields 是参与 delta/merge 的字段集合（不含 cache_hit_rate）。
var UsageFields = []string{"calls", "prompt_tokens", "completion_tokens", "total_tokens", "cache_hit_tokens", "cache_miss_tokens"}

// readUsageInt 从对象（map）读整数；缺失/非法返回 0（等价 read_usage_int）。
func readUsageInt(usage map[string]any, key string) int64 {
	if usage == nil {
		return 0
	}
	switch t := usage[key].(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	}
	return 0
}

func readUsageValue(usage map[string]any, key string) any {
	if usage == nil {
		return nil
	}
	return usage[key]
}

// MakeUsageSample 从 usage 读 prompt/completion/total（缺失按 0，total 缺失=两者之和）。
func MakeUsageSample(usage map[string]any, cacheHitTokens, cacheMissTokens int64) *UsageSample {
	if usage == nil {
		return nil
	}
	prompt := readUsageInt(usage, "prompt_tokens")
	completion := readUsageInt(usage, "completion_tokens")
	total := int64(0)
	if v := readUsageValue(usage, "total_tokens"); v != nil {
		total = readUsageInt(usage, "total_tokens")
	} else {
		total = prompt + completion
	}
	return &UsageSample{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		CacheHitTokens:   cacheHitTokens,
		CacheMissTokens:  cacheMissTokens,
	}
}

type usageSlot struct {
	Calls            int64
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	CacheHitTokens   int64
	CacheMissTokens  int64
}

func (s *usageSlot) addSample(sample *UsageSample) {
	s.Calls++
	if sample == nil {
		return
	}
	s.PromptTokens += sample.PromptTokens
	s.CompletionTokens += sample.CompletionTokens
	s.TotalTokens += sample.TotalTokens
	s.CacheHitTokens += sample.CacheHitTokens
	s.CacheMissTokens += sample.CacheMissTokens
}

func (s *usageSlot) toMap() map[string]any {
	return map[string]any{
		"calls":              s.Calls,
		"prompt_tokens":      s.PromptTokens,
		"completion_tokens":  s.CompletionTokens,
		"total_tokens":       s.TotalTokens,
		"cache_hit_tokens":   s.CacheHitTokens,
		"cache_miss_tokens":  s.CacheMissTokens,
	}
}

func (s *usageSlot) hitRate() float64 {
	d := s.CacheHitTokens + s.CacheMissTokens
	if d <= 0 {
		return 0.0
	}
	return round4(float64(s.CacheHitTokens) / float64(d))
}

func (s *usageSlot) toMapWithRate() map[string]any {
	m := s.toMap()
	m["cache_hit_rate"] = s.hitRate()
	return m
}

func round4(x float64) float64 {
	return math.Round(x*10000) / 10000
}

// UsageTracker 线程安全的用量追踪（by_tier 与 by_stage 双视图）。
type UsageTracker struct {
	mu      sync.Mutex
	byTier  map[string]*usageSlot
	byStage map[string]*usageSlot
}

func NewUsageTracker() *UsageTracker {
	return &UsageTracker{byTier: map[string]*usageSlot{}, byStage: map[string]*usageSlot{}}
}

// Record 记录一次调用；sample 为 nil 时只跳过（calls 也不计）。
func (u *UsageTracker) Record(tier string, sample *UsageSample, stage string) {
	if sample == nil {
		return // None usage 完全不记录（calls 也不计）
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.byTier == nil {
		u.byTier = map[string]*usageSlot{}
	}
	if u.byStage == nil {
		u.byStage = map[string]*usageSlot{}
	}
	if _, ok := u.byTier[tier]; !ok {
		u.byTier[tier] = &usageSlot{}
	}
	u.byTier[tier].addSample(sample)
	if stage != "" {
		if _, ok := u.byStage[stage]; !ok {
			u.byStage[stage] = &usageSlot{}
		}
		u.byStage[stage].addSample(sample)
	}
}

// Summary 返回 {totals, by_tier, by_stage}；totals 只由 tier 汇总。
func (u *UsageTracker) Summary() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	total := &usageSlot{}
	for _, s := range u.byTier {
		total.Calls += s.Calls
		total.PromptTokens += s.PromptTokens
		total.CompletionTokens += s.CompletionTokens
		total.TotalTokens += s.TotalTokens
		total.CacheHitTokens += s.CacheHitTokens
		total.CacheMissTokens += s.CacheMissTokens
	}
	byTier := map[string]any{}
	for name, s := range u.byTier {
		byTier[name] = s.toMapWithRate()
	}
	byStage := map[string]any{}
	for name, s := range u.byStage {
		byStage[name] = s.toMapWithRate()
	}
	return map[string]any{
		"totals":   total.toMapWithRate(),
		"by_tier":  byTier,
		"by_stage": byStage,
	}
}

func slotOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func slotInt(m map[string]any, key string) int64 {
	switch t := m[key].(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	}
	return 0
}

func allZeroSlot(m map[string]any) bool {
	if m == nil {
		return true
	}
	for _, f := range UsageFields {
		if slotInt(m, f) != 0 {
			return false
		}
	}
	return true
}

// UsageDelta 按 slot 逐字段 max(0, 新-旧)，去掉全零 slot（用于增量落盘）。
func UsageDelta(current, previous map[string]any) map[string]any {
	diffSlot := func(cur, prev map[string]any) map[string]any {
		out := map[string]any{}
		for _, f := range UsageFields {
			d := slotInt(cur, f) - slotInt(prev, f)
			if d < 0 {
				d = 0
			}
			out[f] = d
		}
		return out
	}
	diffView := func(cur, prev map[string]any) map[string]any {
		out := map[string]any{}
		for name, c := range cur {
			cm := slotOf(c)
			pm := slotOf(prev[name])
			d := diffSlot(cm, pm)
			if !allZeroSlot(d) {
				out[name] = d
			}
		}
		return out
	}
	increment := map[string]any{
		"totals":   diffSlot(slotOf(current["totals"]), slotOf(previous["totals"])),
		"by_tier":  diffView(slotOf(current["by_tier"]), slotOf(previous["by_tier"])),
		"by_stage": diffView(slotOf(current["by_stage"]), slotOf(previous["by_stage"])),
	}
	if allZeroSlot(slotOf(increment["totals"])) {
		delete(increment, "totals")
	}
	return increment
}

// MergeUsageSummaries 按 slot 逐字段相加；cache_hit_rate 按合并后的 hit/miss 重算。
func MergeUsageSummaries(accumulated, increment map[string]any) map[string]any {
	addSlot := func(a, b map[string]any) map[string]any {
		out := map[string]any{}
		for _, f := range UsageFields {
			out[f] = slotInt(a, f) + slotInt(b, f)
		}
		hit := slotInt(out, "cache_hit_tokens")
		miss := slotInt(out, "cache_miss_tokens")
		if hit+miss > 0 {
			out["cache_hit_rate"] = round4(float64(hit) / float64(hit+miss))
		} else {
			out["cache_hit_rate"] = 0.0
		}
		return out
	}
	addView := func(a, b map[string]any) map[string]any {
		out := map[string]any{}
		for name, s := range a {
			if bs, ok := b[name]; ok {
				out[name] = addSlot(slotOf(s), slotOf(bs))
			} else {
				out[name] = addSlot(slotOf(s), nil)
			}
		}
		for name, s := range b {
			if _, ok := out[name]; !ok {
				out[name] = addSlot(nil, slotOf(s))
			}
		}
		return out
	}
	merged := map[string]any{
		"totals":   addSlot(slotOf(accumulated["totals"]), slotOf(increment["totals"])),
		"by_tier":  addView(slotOf(accumulated["by_tier"]), slotOf(increment["by_tier"])),
		"by_stage": addView(slotOf(accumulated["by_stage"]), slotOf(increment["by_stage"])),
	}
	return merged
}
