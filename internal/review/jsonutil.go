// Package review 对应 Python pipeline/review_run.py + review_evidence.py +
// agents/reviewer.py + review_fixer.py + review_loop.py + orchestrator._run_review_session
// （主规格 §12 + 分册 06/07 §4.3）。全书只读 Agent Review 域。
package review

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"wenyi/internal/jsonx"
)

// ---- Python json.dumps 等价序列化 ----
// 哈希载荷必须与 Python json.dumps(..., ensure_ascii=False, separators=(",", ":")) 字节一致，
// 因此手写紧凑/缩进两种序列化器（jsonx.Marshal 的分隔符带空格，不适合哈希）。

func pythonJSONString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func pyNumber(v any) (string, bool) {
	switch t := v.(type) {
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		// Python float 的 repr；本包哈希载荷不含浮点，此处兜底
		return strconv.FormatFloat(t, 'g', -1, 64), true
	default:
		return "", false
	}
}

// sortedKeysOf 提取 map/OMap 的键并排序（等价 sort_keys=True）。
func sortedKeysOf(v any) ([]string, map[string]any, bool) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys, t, true
	case *jsonx.OMap:
		keys := append([]string(nil), t.Keys()...)
		sort.Strings(keys)
		m := map[string]any{}
		for _, k := range t.Keys() {
			val, _ := t.Get(k)
			m[k] = val
		}
		return keys, m, true
	}
	return nil, nil, false
}

func sliceOf(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out, true
	case []map[string]any:
		out := make([]any, len(t))
		for i, m := range t {
			out[i] = m
		}
		return out, true
	}
	return nil, false
}

// CompactJSON 等价 json.dumps(v, ensure_ascii=False, sort_keys=True, separators=(",", ":"))。
func CompactJSON(v any) string {
	var b strings.Builder
	writeCompactValue(&b, v)
	return b.String()
}

func writeCompactValue(b *strings.Builder, v any) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		b.WriteString(pythonJSONString(t))
	case *jsonx.OMap, map[string]any:
		keys, m, _ := sortedKeysOf(t)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(pythonJSONString(k))
			b.WriteByte(':')
			writeCompactValue(b, m[k])
		}
		b.WriteByte('}')
	default:
		if items, ok := sliceOf(t); ok {
			b.WriteByte('[')
			for i, item := range items {
				if i > 0 {
					b.WriteByte(',')
				}
				writeCompactValue(b, item)
			}
			b.WriteByte(']')
			return
		}
		if n, ok := pyNumber(t); ok {
			b.WriteString(n)
			return
		}
		b.WriteString("null")
	}
}

// Indent2JSON 等价 json.dumps(v, ensure_ascii=False, sort_keys=True, indent=2)。
func Indent2JSON(v any) string {
	var b strings.Builder
	writeIndentValue(&b, v, 0)
	return b.String()
}

func writeIndentValue(b *strings.Builder, v any, depth int) {
	indent := strings.Repeat("  ", depth)
	inner := strings.Repeat("  ", depth+1)
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		b.WriteString(pythonJSONString(t))
	case *jsonx.OMap, map[string]any:
		keys, m, _ := sortedKeysOf(t)
		if len(keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(inner)
			b.WriteString(pythonJSONString(k))
			b.WriteString(": ")
			writeIndentValue(b, m[k], depth+1)
		}
		b.WriteString("\n" + indent + "}")
	default:
		if items, ok := sliceOf(t); ok {
			if len(items) == 0 {
				b.WriteString("[]")
				return
			}
			b.WriteString("[\n")
			for i, item := range items {
				if i > 0 {
					b.WriteString(",\n")
				}
				b.WriteString(inner)
				writeIndentValue(b, item, depth+1)
			}
			b.WriteString("\n" + indent + "]")
			return
		}
		if n, ok := pyNumber(t); ok {
			b.WriteString(n)
			return
		}
		b.WriteString("null")
	}
}

// ---- 通用小工具 ----

// PyText 对应 review_loop._text：str 则 strip，否则空串。
func PyText(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

var caseFolder = cases.Fold()

// NormalizedText 对应 _normalized：NFKC + casefold + strip。
func NormalizedText(s string) string {
	return strings.TrimSpace(caseFolder.String(norm.NFKC.String(s)))
}

// IdentityText 对应 _identity_text：normalized 后把所有空白折叠为单空格。
func IdentityText(v any) string {
	return collapseWhitespace(NormalizedText(PyText(v)))
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// StrictInt 严格 int（排除 bool）；支持 int64/int；字符串数字不算（调用方自行处理宽容转换）。
func StrictInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	}
	return 0, false
}

// LooseInt 严格 int 或字符串数字（排除 bool）；失败返回 false。
func LooseInt(v any) (int, bool) {
	if s, ok := v.(string); ok {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return StrictInt(v)
}

// SortedStrings 返回排序副本。
func SortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// CopyIssue 浅拷贝 issue dict。
func CopyIssue(issue map[string]any) map[string]any {
	out := make(map[string]any, len(issue))
	for k, v := range issue {
		out[k] = v
	}
	return out
}

// OMapToMap 把 *jsonx.OMap 转普通 map（键序丢失）。
func OMapToMap(om *jsonx.OMap) map[string]any {
	m := map[string]any{}
	if om == nil {
		return m
	}
	for _, k := range om.Keys() {
		v, _ := om.Get(k)
		m[k] = deepPlain(v)
	}
	return m
}

// deepPlain 递归把嵌套 *OMap / []any 转为普通 map/slice（保留标量）。
func deepPlain(v any) any {
	switch t := v.(type) {
	case *jsonx.OMap:
		return OMapToMap(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = deepPlain(item)
		}
		return out
	default:
		return v
	}
}

// OMapArgs 把 arguments（*OMap / map）规整为普通 map。
func OMapArgs(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case *jsonx.OMap:
		return OMapToMap(t), true
	case map[string]any:
		return t, true
	}
	return nil, false
}
