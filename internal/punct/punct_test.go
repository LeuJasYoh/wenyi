package punct

import (
	"strings"
	"testing"

	"wenyi/internal/agents"
)

// 迁移自 tests/test_newfeatures.py::TestPunct 与 TestLanguageProfile。

func TestJapaneseQuotes(t *testing.T) {
	if got := NormalizeZH("「你好」"); got != "“你好”" {
		t.Errorf("got %q", got)
	}
	if got := NormalizeZH("『书名』"); got != "‘书名’" {
		t.Errorf("got %q", got)
	}
}

func TestHalfwidthToFullInCJK(t *testing.T) {
	if got := NormalizeZH("他说,真的吗?"); got != "他说，真的吗？" {
		t.Errorf("got %q", got)
	}
}

func TestNoHarmToEnglishNumbers(t *testing.T) {
	if got := NormalizeZH("9.11 vs 9.8"); got != "9.11 vs 9.8" {
		t.Errorf("got %q", got)
	}
	if got := NormalizeZH("Mr.王"); got != "Mr.王" {
		t.Errorf("got %q", got)
	}
}

func TestEllipsisAndDash(t *testing.T) {
	if got := NormalizeZH("等等...走了--他笑了"); got != "等等……走了——他笑了" {
		t.Errorf("got %q", got)
	}
}

func TestWordFinalApostropheIsRightApostrophe(t *testing.T) {
	if got := NormalizeZH("James' book"); got != "James’ book" {
		t.Errorf("got %q", got)
	}
}

func TestQuotesArePairedAcrossSplitContinuations(t *testing.T) {
	got, err := NormalizeZHSegments(
		[]string{"\"第一段", "第二段\"", "\"下一句\""},
		[]bool{false, true, false},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"“第一段", "第二段”", "“下一句”"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestUnmatchedQuoteDoesNotLeakIntoNextParagraph(t *testing.T) {
	got, err := NormalizeZHSegments(
		[]string{"\"缺少右引号", "\"新的完整对话\""},
		[]bool{false, false},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"“缺少右引号", "“新的完整对话”"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestContinuationFlagsMustAlignWithTexts(t *testing.T) {
	_, err := NormalizeZHSegments([]string{"第一段"}, []bool{})
	if err == nil || !strings.Contains(err.Error(), "数量必须一致") {
		t.Errorf("err = %v", err)
	}
}

// ---- TestLanguageProfile ----

func TestKeepStyleRequiresStableHonorificChoice(t *testing.T) {
	rule := agents.HonorificRule("keep_style")
	if !strings.Contains(rule, "确定后同一关系全书沿用") {
		t.Errorf("rule = %q", rule)
	}
	if strings.Contains(rule, "可酌情保留") {
		t.Errorf("rule = %q", rule)
	}
}
