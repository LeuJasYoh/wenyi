// Package punct 中文标点规范化（主规格 §14）。
// 确定性兜底：只在不误伤英文/数字串内部标点的前提下转换。
package punct

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

// isCJK：CJK 统一汉字 + 假名 + 全角符号区 + CJK 符号标点区 + 中文标点集合。
func isCJK(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK 统一汉字
		return true
	case r >= 0x3040 && r <= 0x30FF: // 假名
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // 全角形式
		return true
	case r >= 0x3000 && r <= 0x303F: // CJK 符号和标点（。、《》）
		return true
	}
	return false
}

func isASCIILetterOrDigit(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// 连续点号/破折号（顺序敏感；完整正则原文未随包抄录，按主规格 §14 语义顺序实现）。
var dotPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`。{3,}`), "……"},
	{regexp.MustCompile(`・{2,}`), "……"},
	{regexp.MustCompile(`\.{3,}`), "……"},
	{regexp.MustCompile(`…+`), "……"},
	{regexp.MustCompile(`-{2,}`), "——"},
	{regexp.MustCompile(`—{1,}`), "——"},
}

var trailingSpaceAfterCJK = regexp.MustCompile(`([，。！？：；、])[ \t\r\n\f\v]+`)
var spaceAfterClosingQuote = regexp.MustCompile(`([”’》】])[ \t\r\n\f\v]+`)

// NormalizeZH 单段规范化（引号状态重置）。
func NormalizeZH(text string) string {
	out, _, _ := normalizeWithState(text, false, false)
	return out
}

// normalizeWithState 处理单段并返回更新后的引号状态。
// dqOpen/sqOpen 为进入本段时的双/单引号开闭状态。
func normalizeWithState(text string, dqOpen, sqOpen bool) (string, bool, bool) {
	// ① 日式引号直映射
	text = strings.Map(func(r rune) rune {
		switch r {
		case '「':
			return '“'
		case '」':
			return '”'
		case '『':
			return '‘'
		case '』':
			return '’'
		}
		return r
	}, text)

	runes := []rune(text)
	var out []rune
	// ②③ 直双引号交替配对 + 直单引号状态机（一次扫描）
	for i, r := range runes {
		switch r {
		case '"':
			if dqOpen {
				out = append(out, '”')
				dqOpen = false
			} else {
				out = append(out, '“')
				dqOpen = true
			}
		case '\'':
			prevLetter := i > 0 && isASCIILetter(runes[i-1])
			prev := rune(0)
			if i > 0 {
				prev = runes[i-1]
			}
			next := rune(0)
			if i+1 < len(runes) {
				next = runes[i+1]
			}
			nextLetter := i+1 < len(runes) && isASCIILetter(runes[i+1])
			if prevLetter && nextLetter {
				// 字母内撇号：don't
				out = append(out, '’')
			} else if prevLetter && !nextLetter {
				// 词尾撇号：James'
				out = append(out, '’')
			} else {
				_ = prev
				_ = next
				// 引语内配对
				if sqOpen {
					out = append(out, '’')
					sqOpen = false
				} else {
					out = append(out, '‘')
					sqOpen = true
				}
			}
		default:
			out = append(out, r)
		}
	}
	text = string(out)

	// ④ 连续点号/破折号
	for _, p := range dotPatterns {
		text = p.re.ReplaceAllString(text, p.repl)
	}

	// ⑤ 半角 , . ! ? : ; 紧邻 CJK → 全角（左侧 ASCII 字母/数字且右侧 CJK 不转：Mr.王、v2.版本）
	runes = []rune(text)
	out = nil
	fullwidth := map[rune]rune{',': '，', '.': '。', '!': '！', '?': '？', ':': '：', ';': '；'}
	for i, r := range runes {
		if repl, isPunct := fullwidth[r]; isPunct {
			leftCJK := i > 0 && isCJK(runes[i-1])
			rightCJK := i+1 < len(runes) && isCJK(runes[i+1])
			leftASCII := i > 0 && isASCIILetterOrDigit(runes[i-1])
			if leftCJK || (rightCJK && !leftASCII) {
				out = append(out, repl)
				continue
			}
		}
		out = append(out, r)
	}
	text = string(out)

	// ⑥ 去标点后空白
	text = trailingSpaceAfterCJK.ReplaceAllString(text, "$1")
	// ⑦ 引号后接 CJK 时去空白（需要前瞻；手工扫描）
	text = removeSpaceBeforeCJK(text)
	return text, dqOpen, sqOpen
}

func removeSpaceBeforeCJK(text string) string {
	runes := []rune(text)
	var out []rune
	closers := map[rune]bool{'”': true, '’': true, '》': true, '】': true}
	for i, r := range runes {
		if (r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v') &&
			i > 0 && closers[runes[i-1]] && i+1 < len(runes) && isCJK(runes[i+1]) {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// ErrLengthMismatch 长度不符。
var ErrLengthMismatch = errors.New("texts 与 continuations 数量必须一致")

// NormalizeZHSegments 整组规范化：只在 cont=True 的续段间传递引号开闭状态；
// 普通段落即使缺引号也不级联污染后文。
func NormalizeZHSegments(texts []string, continuations []bool) ([]string, error) {
	if len(texts) != len(continuations) {
		return nil, ErrLengthMismatch
	}
	out := make([]string, len(texts))
	dqOpen, sqOpen := false, false
	for i, text := range texts {
		if !continuations[i] {
			// 非续段：新逻辑段，引号状态重置（不级联污染后文）
			dqOpen, sqOpen = false, false
		}
		var normalized string
		normalized, dqOpen, sqOpen = normalizeWithState(text, dqOpen, sqOpen)
		out[i] = normalized
	}
	return out, nil
}

var _ = unicode.IsLetter
