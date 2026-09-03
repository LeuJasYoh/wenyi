// Package fakellm 是 fake_llm.py::routing_handler 的逐语言等价实现（测试驱动源）。
package fakellm

import (
	"fmt"
	"regexp"
	"strings"

	"wenyi/internal/llm"
)

var numberedRe = regexp.MustCompile(`(?m)^\[(\d+)\]`)

func countNumbered(text string) int {
	return len(numberedRe.FindAllString(text, -1))
}

// RoutingHandler 按 system 提示词特征路由的确定性输出（主规格 §16）。
func RoutingHandler(messages []llm.Message, tier string, jsonMode bool) (string, error) {
	system := messages[0].Content
	user := messages[len(messages)-1].Content

	switch {
	case strings.Contains(system, "语言识别器"):
		return `{"language": "ja"}`, nil
	case strings.Contains(system, "前期分析师"):
		return `{"genre":"校园","tone":"冷峻","style_guide":"克制","characters":[{"source":"綾小路","target":"绫小路","gender":"男"}],"terms":[]}`, nil
	case strings.Contains(system, "标题翻译"):
		n := countNumbered(user)
		return titlesJSON(n), nil
	case strings.Contains(system, "文学翻译"):
		n := countNumbered(user)
		return translationsJSON(n), nil
	case strings.Contains(system, "中文润色编辑"):
		n := countNumbered(user)
		return polishedJSON(n), nil
	case strings.Contains(system, "译文审校"):
		n := countNumbered(user)
		return fmt.Sprintf(`{"issues":[],"reviewed_segments":%d,"complete":true}`, n), nil
	case strings.Contains(system, "术语") && strings.Contains(system, "抽取器"):
		return `{"terms":[{"source":"堀北","target":"堀北","type":"人物","gender":"女"}]}`, nil
	case strings.Contains(system, "回译译者"):
		n := countNumbered(user)
		return backtranslationsJSON(n), nil
	case strings.Contains(system, "保真度"):
		return `{"issues":[]}`, nil
	case strings.Contains(system, "章节梗概员"):
		return "本章梗概：人物登场，情节推进。", nil
	case strings.Contains(system, "全书概览员"):
		return "全书概览：主线与人物关系，整体基调。", nil
	default:
		if jsonMode {
			return "{}", nil
		}
		return "", nil
	}
}

func titlesJSON(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`"标题%d"`, i)
	}
	return `{"titles":[` + strings.Join(items, ",") + `]}`
}

func translationsJSON(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`"译%d"`, i)
	}
	return `{"translations":[` + strings.Join(items, ",") + `]}`
}

func polishedJSON(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`"润%d"`, i)
	}
	return `{"polished":[` + strings.Join(items, ",") + `]}`
}

func backtranslationsJSON(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`"逆%d"`, i)
	}
	return `{"backtranslations":[` + strings.Join(items, ",") + `]}`
}
