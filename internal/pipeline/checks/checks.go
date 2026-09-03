// Package checks 对应 trans_novel/pipeline/checks.py（分册 07 §3）。
package checks

import (
	"strings"
	"unicode/utf8"
)

// LengthFlag 长度异常标记。Reason ∈ {"too_short","too_long","empty"}。
type LengthFlag struct {
	Index int
	Ratio float64
	Reason string
}

// LengthFlags 廉价长度校验（默认 too_short=0.30 / too_long=3.0；等于阈值不标记）。
// Python len(str) = Unicode 码点数 → 此处用 RuneCount。
func LengthFlags(sources, targets []string, tooShort, tooLong float64) []LengthFlag {
	var flags []LengthFlag
	for i := range sources {
		t := ""
		if i < len(targets) {
			t = targets[i]
		}
		sLen := utf8.RuneCountInString(strings.TrimSpace(sources[i]))
		tLen := utf8.RuneCountInString(strings.TrimSpace(t))
		if sLen == 0 {
			continue
		}
		if tLen == 0 {
			flags = append(flags, LengthFlag{Index: i, Ratio: 0.0, Reason: "empty"})
			continue
		}
		ratio := float64(tLen) / float64(sLen)
		if ratio < tooShort {
			flags = append(flags, LengthFlag{Index: i, Ratio: ratio, Reason: "too_short"})
		} else if ratio > tooLong {
			flags = append(flags, LengthFlag{Index: i, Ratio: ratio, Reason: "too_long"})
		}
	}
	return flags
}
