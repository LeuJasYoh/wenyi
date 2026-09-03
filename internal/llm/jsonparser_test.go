package llm

import (
	"strings"
	"testing"

	"wenyi/internal/config"
	"wenyi/internal/jsonx"
)

// 迁移自 tests/test_llm.py::TestParseJsonLoose / TestParseJsonLooseRepairs /
// TestResolveTier / TestFakeClient，以及 test_usage.py::TestAgentStageAttribution。

func mustParse(t *testing.T, text string) any {
	t.Helper()
	v, err := ParseJSONLoose(text)
	if err != nil {
		t.Fatalf("ParseJSONLoose(%q): %v", text, err)
	}
	return v
}

func TestParseJSONLoosePlain(t *testing.T) {
	v := mustParse(t, `{"a":1}`).(*jsonx.OMap)
	got, _ := v.Get("a")
	if got != int64(1) {
		t.Errorf("a = %v", got)
	}
}

func TestParseJSONLooseFenced(t *testing.T) {
	v := mustParse(t, "```json\n[1,2,3]\n```").([]any)
	if len(v) != 3 || v[0] != int64(1) || v[2] != int64(3) {
		t.Errorf("v = %v", v)
	}
}

func TestParseJSONLooseSurroundedByProse(t *testing.T) {
	v := mustParse(t, `思考结束。结果如下：["译文1","译文2"] 完了。`).([]any)
	if len(v) != 2 || v[0] != "译文1" || v[1] != "译文2" {
		t.Errorf("v = %v", v)
	}
}

func TestParseJSONLooseFailure(t *testing.T) {
	if _, err := ParseJSONLoose("没有任何 JSON 内容"); err == nil {
		t.Errorf("期望报错")
	}
}

func TestParseResultReportsWhetherRepairWasUsed(t *testing.T) {
	_, repaired, err := ParseJSONResult(`{"a": 1}`)
	if err != nil || repaired {
		t.Errorf("严格路径应 repaired=false")
	}
	v, repaired, err := ParseJSONResult(`{"a": 1`)
	if err != nil || !repaired {
		t.Fatalf("修复路径应 repaired=true, err=%v", err)
	}
	om := v.(*jsonx.OMap)
	got, _ := om.Get("a")
	if got != int64(1) {
		t.Errorf("a = %v", got)
	}
}

func TestInnerASCIIQuotesRepaired(t *testing.T) {
	raw := `{"translations":["磨到那份锱铢必较里暗含的"小气"二字无声地烫上面颊。"]}`
	v := mustParse(t, raw).(*jsonx.OMap)
	trans, _ := v.Get("translations")
	items := trans.([]any)
	if items[0] != `磨到那份锱铢必较里暗含的"小气"二字无声地烫上面颊。` {
		t.Errorf("items[0] = %q", items[0])
	}
}

func TestTrailingExtraBrace(t *testing.T) {
	v := mustParse(t, "{\"a\": 1}\n}").(*jsonx.OMap)
	got, _ := v.Get("a")
	if got != int64(1) {
		t.Errorf("a = %v", got)
	}
}

func TestUnescapedQuotesWithTrailingExtraBraceKeepsObject(t *testing.T) {
	raw := "{\"translations\":[\"他说\"好\"。\"]}\n}"
	v := mustParse(t, raw).(*jsonx.OMap)
	trans, _ := v.Get("translations")
	if trans.([]any)[0] != "他说\"好\"。" {
		t.Errorf("v = %v", trans)
	}
}

func TestValidJSONUntouched(t *testing.T) {
	v := mustParse(t, `{"a": "b, c: d"}`).(*jsonx.OMap)
	got, _ := v.Get("a")
	if got != "b, c: d" {
		t.Errorf("a = %q", got)
	}
}

func TestEscapedQuotesStillWork(t *testing.T) {
	v := mustParse(t, `{"a": "he said \"hi\""}`).(*jsonx.OMap)
	got, _ := v.Get("a")
	if got != `he said "hi"` {
		t.Errorf("a = %q", got)
	}
}

// ---- TestResolveTier ----

func TestResolveTierFallbackChain(t *testing.T) {
	strong := ResolvedTier[int]{Model: "pro", Options: 1}
	cheap := ResolvedTier[int]{Model: "flash", Options: 2}
	fast := ResolvedTier[int]{Model: "flash", Options: 3}

	tiers := map[string]ResolvedTier[int]{"strong": strong, "cheap": cheap, "fast": fast}
	if ResolveTier(tiers, "fast") != fast || ResolveTier(tiers, "cheap") != cheap || ResolveTier(tiers, "strong") != strong {
		t.Errorf("三档全有应各归各")
	}
	tiers2 := map[string]ResolvedTier[int]{"strong": strong, "cheap": cheap}
	if ResolveTier(tiers2, "fast") != cheap {
		t.Errorf("无 fast 应落 cheap")
	}
	tiers3 := map[string]ResolvedTier[int]{"strong": strong}
	if ResolveTier(tiers3, "fast") != strong || ResolveTier(tiers3, "cheap") != strong {
		t.Errorf("只有 strong 都应落 strong")
	}
	if ResolveTier(tiers, "unknown") != strong {
		t.Errorf("未知档应落 strong")
	}
}

// ---- TestFakeClient ----

func TestFakeClientDefault(t *testing.T) {
	c := NewFakeClient(nil)
	out, err := c.Complete([]Message{{Role: "user", Content: "x"}}, "strong", false, nil, "")
	if err != nil || out != "" {
		t.Errorf("complete = %q, %v", out, err)
	}
	v, err := c.CompleteJSON([]Message{{Role: "user", Content: "x"}}, "strong", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := v.([]any)
	if !ok || len(arr) != 0 {
		t.Errorf("complete_json = %v", v)
	}
}

func TestFakeClientHandler(t *testing.T) {
	c := NewFakeClient(func(messages []Message, tier string, jsonMode bool) (string, error) {
		if jsonMode {
			return `["A","B"]`, nil
		}
		return "hello", nil
	})
	out, _ := c.Complete([]Message{{Role: "user", Content: "x"}}, "strong", false, nil, "")
	if out != "hello" {
		t.Errorf("complete = %q", out)
	}
	v, _ := c.CompleteJSON([]Message{{Role: "user", Content: "x"}}, "strong", nil, "")
	arr := v.([]any)
	if len(arr) != 2 || arr[0] != "A" || arr[1] != "B" {
		t.Errorf("complete_json = %v", v)
	}
	if len(c.Calls) != 2 {
		t.Errorf("calls = %d", len(c.Calls))
	}
}

// ---- test_usage.py::TestAgentStageAttribution ----

func TestAgentStageAttribution(t *testing.T) {
	// Agent 基类 stage=type(self).__name__ —— 直接验证 FakeClient 快照中的 stage 字段
	client := NewFakeClient(nil)
	cfg, _ := config.FromDict(map[string]any{"llm": map[string]any{"provider": "fake"}})
	_ = cfg
	client.Complete([]Message{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}}, "strong", false, nil, "Agent")
	client.Complete([]Message{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}}, "cheap", true, nil, "Agent")
	for _, call := range client.Calls {
		if call["stage"] != "Agent" {
			t.Errorf("stage = %v", call["stage"])
		}
	}
	_ = strings.TrimSpace
}
