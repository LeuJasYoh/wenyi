package cli

// progress.go --progress-line 进度通道（分册 10 §1.8）：
// 开启后把 orchestrator 的 cb(done,total,label) 回调以 JSON Lines 打到 stdout。

import (
	"encoding/json"
	"fmt"
	"os"
)

// EmitProgressLine 输出 {"type":"progress","done":…,"total":…,"label":"…"}。
func EmitProgressLine(done, total int, label string) {
	if !progressLine {
		return
	}
	row := map[string]any{"type": "progress", "done": done, "total": total, "label": label}
	b, err := json.Marshal(row)
	if err != nil {
		return
	}
	fmt.Fprintln(os.Stdout, string(b))
}

// WrapProgress 把进度回调包一层：人读输出关闭时透传原回调。
func WrapProgress(inner func(done, total int, label string)) func(done, total int, label string) {
	if inner == nil {
		return func(done, total int, label string) { EmitProgressLine(done, total, label) }
	}
	return func(done, total int, label string) {
		EmitProgressLine(done, total, label)
		inner(done, total, label)
	}
}
