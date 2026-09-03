package llm

import (
	"sync"
	"testing"
)

// 迁移自 tests/test_usage.py（RunStore/Orchestrator 相关用例归阶段 3）。

func intOf(m map[string]any, key string) int64 { return slotIntT(m, key) }

func slotIntT(m map[string]any, key string) int64 {
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

func TestUsageTrackerByTierAndStage(t *testing.T) {
	tracker := NewUsageTracker()
	tracker.Record("strong", &UsageSample{PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200, CacheHitTokens: 800, CacheMissTokens: 200}, "Translator")
	tracker.Record("cheap", &UsageSample{PromptTokens: 500, CompletionTokens: 100, TotalTokens: 600, CacheHitTokens: 100, CacheMissTokens: 400}, "")
	summary := tracker.Summary()
	totals := summary["totals"].(map[string]any)
	if intOf(totals, "prompt_tokens") != 1500 || intOf(totals, "completion_tokens") != 300 ||
		intOf(totals, "total_tokens") != 1800 || intOf(totals, "cache_hit_tokens") != 900 ||
		intOf(totals, "cache_miss_tokens") != 600 || intOf(totals, "calls") != 2 {
		t.Errorf("totals = %v", totals)
	}
	if totals["cache_hit_rate"] != 0.6 {
		t.Errorf("cache_hit_rate = %v", totals["cache_hit_rate"])
	}
	byTier := summary["by_tier"].(map[string]any)
	if byTier["strong"].(map[string]any)["cache_hit_rate"] != 0.8 {
		t.Errorf("strong rate = %v", byTier["strong"])
	}
	if byTier["cheap"].(map[string]any)["cache_hit_rate"] != 0.2 {
		t.Errorf("cheap rate = %v", byTier["cheap"])
	}
	if intOf(byTier["strong"].(map[string]any), "calls") != 1 || intOf(byTier["cheap"].(map[string]any), "calls") != 1 {
		t.Errorf("by_tier calls 不符")
	}
	if intOf(byTier["strong"].(map[string]any), "prompt_tokens") != 1000 || intOf(byTier["cheap"].(map[string]any), "prompt_tokens") != 500 {
		t.Errorf("by_tier prompt 不符")
	}
	byStage := summary["by_stage"].(map[string]any)
	if _, ok := byStage["Translator"]; !ok {
		t.Errorf("by_stage 应含 Translator")
	}
	if intOf(byStage["Translator"].(map[string]any), "total_tokens") != 1200 {
		t.Errorf("stage total = %v", byStage["Translator"])
	}
	if byStage["Translator"].(map[string]any)["cache_hit_rate"] != 0.8 {
		t.Errorf("stage rate = %v", byStage["Translator"])
	}
}

func TestNoneUsageSilentlySkipped(t *testing.T) {
	tracker := NewUsageTracker()
	tracker.Record("strong", nil, "")
	summary := tracker.Summary()
	totals := summary["totals"].(map[string]any)
	if intOf(totals, "calls") != 0 || intOf(totals, "total_tokens") != 0 {
		t.Errorf("totals = %v", totals)
	}
	if len(summary["by_tier"].(map[string]any)) != 0 || len(summary["by_stage"].(map[string]any)) != 0 {
		t.Errorf("by_tier/by_stage 应为空")
	}
}

func TestMissingTotalTokensFallsBackToPromptPlusCompletion(t *testing.T) {
	tracker := NewUsageTracker()
	sample := MakeUsageSample(map[string]any{"prompt_tokens": 40, "completion_tokens": 10}, 0, 0)
	if sample.TotalTokens != 50 {
		t.Errorf("total = %d", sample.TotalTokens)
	}
	tracker.Record("cheap", sample, "")
	slot := tracker.Summary()["by_tier"].(map[string]any)["cheap"].(map[string]any)
	if intOf(slot, "prompt_tokens") != 40 || intOf(slot, "completion_tokens") != 10 || intOf(slot, "total_tokens") != 50 || intOf(slot, "calls") != 1 {
		t.Errorf("slot = %v", slot)
	}
}

func TestFreshClientZeroHitRateAndFullKeys(t *testing.T) {
	c := NewFakeClient(nil)
	totals := c.UsageSummary()["totals"].(map[string]any)
	if totals["cache_hit_rate"] != 0.0 || intOf(totals, "total_tokens") != 0 || intOf(totals, "calls") != 0 {
		t.Errorf("totals = %v", totals)
	}
	for _, key := range []string{"calls", "prompt_tokens", "completion_tokens", "total_tokens", "cache_hit_tokens", "cache_miss_tokens", "cache_hit_rate"} {
		if _, ok := totals[key]; !ok {
			t.Errorf("缺少键 %q", key)
		}
	}
	if len(c.UsageSummary()["by_tier"].(map[string]any)) != 0 || len(c.UsageSummary()["by_stage"].(map[string]any)) != 0 {
		t.Errorf("by_tier/by_stage 应为空")
	}
}

func TestUsageThreadSafety(t *testing.T) {
	client := NewFakeClient(nil)
	nWorkers, perWorker := 8, 25
	sample := &UsageSample{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CacheHitTokens: 3, CacheMissTokens: 7}
	var wg sync.WaitGroup
	for w := 0; w < nWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				client.Usage.Record("strong", sample, "")
			}
		}()
	}
	wg.Wait()
	totalCalls := int64(nWorkers * perWorker)
	totals := client.UsageSummary()["totals"].(map[string]any)
	if intOf(totals, "calls") != totalCalls ||
		intOf(totals, "prompt_tokens") != 10*totalCalls ||
		intOf(totals, "completion_tokens") != 5*totalCalls ||
		intOf(totals, "total_tokens") != 15*totalCalls ||
		intOf(totals, "cache_hit_tokens") != 3*totalCalls ||
		intOf(totals, "cache_miss_tokens") != 7*totalCalls {
		t.Errorf("totals = %v", totals)
	}
	if totals["cache_hit_rate"] != 0.3 {
		t.Errorf("rate = %v", totals["cache_hit_rate"])
	}
	strong := client.UsageSummary()["by_tier"].(map[string]any)["strong"].(map[string]any)
	if intOf(strong, "calls") != totalCalls {
		t.Errorf("strong calls = %d", intOf(strong, "calls"))
	}
}

func TestDeltaAndMergeDoNotDoubleCount(t *testing.T) {
	client := NewFakeClient(nil)
	record := func(tier string, prompt, completion int64, stage string) {
		client.Usage.Record(tier, &UsageSample{
			PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion,
			CacheHitTokens: prompt / 2, CacheMissTokens: prompt - prompt/2,
		}, stage)
	}
	record("strong", 100, 20, "Translator")
	first := client.UsageSummary()
	record("strong", 50, 10, "Translator")
	record("fast", 30, 5, "Synopsizer")
	second := client.UsageSummary()

	increment := UsageDelta(second, first)
	if got := intOf(increment["totals"].(map[string]any), "total_tokens"); got != 95 {
		t.Errorf("increment totals = %d, want 95", got)
	}
	if got := intOf(increment["by_stage"].(map[string]any)["Translator"].(map[string]any), "total_tokens"); got != 60 {
		t.Errorf("increment Translator = %d, want 60", got)
	}
	if got := intOf(increment["by_stage"].(map[string]any)["Synopsizer"].(map[string]any), "total_tokens"); got != 35 {
		t.Errorf("increment Synopsizer = %d, want 35", got)
	}
	merged := MergeUsageSummaries(first, increment)
	deepEqualS := func(a, b map[string]any) bool { return deepEqualSlots(a, b) }
	if !deepEqualS(merged, second) {
		t.Errorf("merged != second\nmerged=%v\nsecond=%v", merged, second)
	}
}

// deepEqualSlots 比较 usage summary（忽略 cache_hit_rate 的 float 表示差异按值比较）。
func deepEqualSlots(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		switch t := av.(type) {
		case map[string]any:
			bm, ok := bv.(map[string]any)
			if !ok || !deepEqualSlots(t, bm) {
				return false
			}
		case float64:
			bf, ok := bv.(float64)
			if !ok || t != bf {
				return false
			}
		case int64:
			if intOf(map[string]any{"x": bv}, "x") != t && bv != intOf(map[string]any{"x": av}, "x") {
				bi := slotIntT(map[string]any{"x": bv}, "x")
				if bi != t {
					return false
				}
			}
		default:
			if av != bv {
				return false
			}
		}
	}
	return true
}
