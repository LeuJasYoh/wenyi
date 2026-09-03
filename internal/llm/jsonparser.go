package llm

import (
	"fmt"
	"strings"

	"wenyi/internal/jsonx"
)

// JsonParseError 对应 Python 的 trans_novel.llm.json_parser.JsonParseError。
type JsonParseError struct{ Msg string }

func (e *JsonParseError) Error() string { return e.Msg }

// RepairJSON 是 json-repair 的最小手写等价（架构分册 §4.1）：
// ① 剥离 ```json 围栏；② 提取首个 {/[ 起的 JSON 值（忽略前后说明文字与尾随多余括号）；
// ③ 字符级修复：字符串内未转义引号按"后续 token 判定"转义、EOF 处补全闭合括号。
// 返回空串表示修复失败。
func RepairJSON(raw string) string {
	s := strings.TrimSpace(raw)
	// 剥离 ```json ... ``` 围栏
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		} else {
			s = strings.TrimPrefix(s, "```json")
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	}
	// 定位首个容器起点
	start := -1
	for i, r := range s {
		if r == '{' || r == '[' {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	src := []rune(s[start:])

	var out strings.Builder
	var stack []rune
	inStr, esc := false, false
	closed := false
	for i := 0; i < len(src); i++ {
		ch := src[i]
		if closed {
			break // 顶层容器已闭合，忽略尾随内容（多余 }、说明文字等）
		}
		if inStr {
			if esc {
				esc = false
				out.WriteRune(ch)
				continue
			}
			switch ch {
			case '\\':
				esc = true
				out.WriteRune(ch)
			case '"':
				// 引号是否真正结束字符串：看下一个非空白字符是否为合法后继 token
				j := i + 1
				for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\r' || src[j] == '\n') {
					j++
				}
				if j >= len(src) || src[j] == ',' || src[j] == '}' || src[j] == ']' || src[j] == ':' {
					inStr = false
					out.WriteRune(ch)
				} else {
					out.WriteString("\\\"") // 未转义内引号 → 转义
				}
			default:
				out.WriteRune(ch)
			}
			continue
		}
		switch ch {
		case '"':
			inStr = true
			out.WriteRune(ch)
		case '{', '[':
			stack = append(stack, ch)
			out.WriteRune(ch)
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			out.WriteRune(ch)
			if len(stack) == 0 {
				closed = true
			}
		default:
			out.WriteRune(ch)
		}
	}
	if !closed && len(stack) == 0 && !inStr && out.Len() == 0 {
		return ""
	}
	// EOF 处补全
	if inStr {
		out.WriteString("\"")
	}
	for len(stack) > 0 {
		op := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if op == '{' {
			out.WriteString("}")
		} else {
			out.WriteString("]")
		}
	}
	result := strings.TrimSpace(out.String())
	if result == "" {
		return ""
	}
	return result
}

// ParseJSONResult 对应 Python parse_json_result：严格解析成功 repaired=false；
// 失败走最小修复；修复结果为空串 → JsonParseError（消息含原文前 200 字符）。
func ParseJSONResult(text string) (value any, repaired bool, err error) {
	v, perr := jsonx.Parse(text)
	if perr == nil {
		return v, false, nil
	}
	fixed := RepairJSON(text)
	if fixed == "" {
		return nil, false, &JsonParseError{Msg: fmt.Sprintf("LLM 返回的 JSON 无法解析：%s", truncate200(text))}
	}
	v, perr = jsonx.Parse(fixed)
	if perr != nil {
		return nil, false, &JsonParseError{Msg: fmt.Sprintf("LLM 返回的 JSON 无法解析：%s", truncate200(text))}
	}
	return v, true, nil
}

// ParseJSONLoose 只返回值（对应 Python parse_json_loose）。
func ParseJSONLoose(text string) (any, error) {
	v, _, err := ParseJSONResult(text)
	return v, err
}

func truncate200(s string) string {
	r := []rune(s)
	if len(r) > 200 {
		r = r[:200]
	}
	return string(r)
}
