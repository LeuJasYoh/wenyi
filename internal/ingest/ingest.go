// Package ingest 是 Go 侧文档加载入口：spawn Node 组件执行解析（架构分册 §3.2）。
// Node 产出 source/doc.json（Document 全量序列化）+ chapters/ch{N}.json（Go 由 doc.json 重建）。
package ingest

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	_ "modernc.org/sqlite"
	"path/filepath"
	"strings"
)

// Segment 最小可对齐/可回填翻译单元（字段顺序 = JSON 序列化顺序，主规格 §6）。
type Segment struct {
	Index        int            `json:"index"`
	Source       string         `json:"source"`
	Kind         string         `json:"kind"`
	Target       *string        `json:"target"`
	Anchor       *string        `json:"anchor"`
	ResourceHref *string        `json:"resource_href"`
	Cont         bool           `json:"cont"`
	Meta         map[string]any `json:"meta"`
}

// Chapter 逻辑章。
type Chapter struct {
	Index    int        `json:"index"`
	Title    string     `json:"title"`
	Segments []*Segment `json:"segments"`
	Href     *string    `json:"href"`
	Template *string    `json:"template"`
	Meta     map[string]any `json:"meta"`
}

// TextSegments 需要送翻译的非空 Segment。
func (c *Chapter) TextSegments() []*Segment {
	var out []*Segment
	for _, s := range c.Segments {
		if strings.TrimSpace(s.Source) != "" {
			out = append(out, s)
		}
	}
	return out
}

// Document 输入文档。
type Document struct {
	Title      string           `json:"title"`
	SourceLang string           `json:"source_lang"`
	TargetLang string           `json:"target_lang"`
	Fmt        string           `json:"fmt"`
	SourcePath string           `json:"source_path"`
	Chapters   []*Chapter       `json:"chapters"`
	Meta       map[string]any   `json:"meta"`
}

// NodeCLIPath 返回 node cli.js 路径（可注入/环境变量/自动发现）。
var NodeCLIPath = discoverNodeCLI()

// NodeCommand node 可执行文件（默认 "node"）。
var NodeCommand = "node"

func discoverNodeCLI() string {
	if p := os.Getenv("WENYI_NODE_CLI"); p != "" {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, cand := range []string{
			filepath.Join(dir, "..", "wenyi-node", "cli.js"),
			filepath.Join(dir, "wenyi-node", "cli.js"),
		} {
			if fileExists(cand) {
				return filepath.Clean(cand)
			}
		}
	}
	cwd, _ := os.Getwd()
	dir := cwd
	for i := 0; i < 8; i++ {
		cand := filepath.Join(dir, "node", "cli.js")
		if fileExists(cand) {
			return cand
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join("node", "cli.js")
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// LoadDocument spawn Node 解析输入。stateDir 为目标状态目录（PDF 的 converted.html
// 缓存在其中 source/ 下）；splitSegments>0 时按句拆超长段。
// 返回从 doc.json 解析出的 Document（meta 数字已归一为 int64）。
func LoadDocument(inputPath, sourceLang, targetLang string, splitSegments int, stateDir string) (*Document, error) {
	args := []string{
		NodeCLIPath, "doc", "parse",
		"--input", inputPath,
		"--state-dir", stateDir,
		"--lang", sourceLang + "," + targetLang,
	}
	if splitSegments > 0 {
		args = append(args, "--split", fmt.Sprintf("%d", splitSegments))
	}
	cmd := exec.Command(NodeCommand, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("输入解析失败（退出码 %d）", ee.ExitCode())
		}
		return nil, fmt.Errorf("无法启动 Node 解析组件（%s）：%w", NodeCommand, err)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "source", "doc.json"))
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var doc Document
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	NormalizeMetaNumbers(&doc)
	return &doc, nil
}

// NormalizeMetaNumbers 把 JSON 数字（json.Number/float64 整数）归一为 int64，
// 避免 meta 中偏移量变成浮点破坏注释对齐等消费方。
func NormalizeMetaNumbers(doc *Document) {
	for _, ch := range doc.Chapters {
		ch.Meta = normMap(ch.Meta)
		for _, s := range ch.Segments {
			s.Meta = normMap(s.Meta)
		}
	}
	doc.Meta = normMap(doc.Meta)
}

func normMap(m map[string]any) map[string]any {
	if m == nil {
		return m
	}
	for k, v := range m {
		m[k] = normValue(v)
	}
	return m
}

func normValue(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case float64:
		if t == float64(int64(t)) {
			return int64(t)
		}
		return t
	case map[string]any:
		return normMap(t)
	case []any:
		for i, item := range t {
			t[i] = normValue(item)
		}
		return t
	default:
		return v
	}
}

// TargetText 译文（无则空串）。
func (s *Segment) TargetText() string {
	if s.Target == nil {
		return ""
	}
	return *s.Target
}

// SetTarget 写译文。
func (s *Segment) SetTarget(t string) { s.Target = &t }
