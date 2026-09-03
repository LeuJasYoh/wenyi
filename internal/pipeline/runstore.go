package pipeline

// runstore.go 状态存储（主规格 §11 + 分册 07 §2）。

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"wenyi/internal/ingest"
	"wenyi/internal/jsonx"
)

const StatusPending = "pending"
const StatusDone = "done"

var slugNonWord = regexp.MustCompile(`[^\w一-鿿぀-ヿ-]+`)

// Slugify 书名 → 目录名（主规格 §11.1）。
func Slugify(name string) string {
	s := slugNonWord.ReplaceAllString(name, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "book"
	}
	return s
}

// RunStore 书级状态目录。
type RunStore struct {
	RunDir     string
	ChaptersDir string

	mu                 sync.Mutex
	batchGlossaryCache map[int]map[string]bool
	cacheLoaded        bool
}

// NewRunStore 打开/创建状态目录。
func NewRunStore(runDir string, create bool) (*RunStore, error) {
	s := &RunStore{
		RunDir:             runDir,
		ChaptersDir:        filepath.Join(runDir, "chapters"),
		batchGlossaryCache: map[int]map[string]bool{},
	}
	if create {
		if err := s.EnsureDirs(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *RunStore) EnsureDirs() error {
	return os.MkdirAll(s.ChaptersDir, 0o755)
}

// ---- 路径 ----

func (s *RunStore) ManifestPath() string { return filepath.Join(s.RunDir, "manifest.json") }
func (s *RunStore) ContextPath() string  { return filepath.Join(s.RunDir, "context.json") }
func (s *RunStore) AnalysisPath() string { return filepath.Join(s.RunDir, "analysis.json") }
func (s *RunStore) GlossaryPath() string { return filepath.Join(s.RunDir, "glossary.db") }
func (s *RunStore) ReportPath() string   { return filepath.Join(s.RunDir, "report.json") }
func (s *RunStore) UsagePath() string    { return filepath.Join(s.RunDir, "usage.json") }
func (s *RunStore) EventLogPath() string { return filepath.Join(s.RunDir, "events.jsonl") }
func (s *RunStore) SourceDir() string    { return filepath.Join(s.RunDir, "source") }
func (s *RunStore) ReviewsDir() string   { return filepath.Join(s.RunDir, "reviews") }
func (s *RunStore) ChapterPath(ci int) string {
	return filepath.Join(s.ChaptersDir, fmt.Sprintf("ch%d.json", ci))
}

// Exists manifest 存在即视为已初始化。
func (s *RunStore) Exists() bool {
	fi, err := os.Stat(s.ManifestPath())
	return err == nil && !fi.IsDir()
}

// ---- 书级锁（跨平台阻塞排他）----

type bookLock struct {
	f *os.File
}

func (l *bookLock) release() {
	if l.f == nil {
		return
	}
	unlockFile(l.f)
	l.f.Close()
	l.f = nil
}

// Lock 书级进程锁（Windows LockFileEx / POSIX flock 语义，阻塞）。
// 返回释放函数。同一进程内多 goroutine 互斥由内部 mutex 保证（test_second_store_waits…）。
var globalLockMu sync.Mutex

func (s *RunStore) Lock() (func(), error) {
	globalLockMu.Lock()
	if err := s.EnsureDirs(); err != nil {
		globalLockMu.Unlock()
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.RunDir, ".run.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		globalLockMu.Unlock()
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		globalLockMu.Unlock()
		return nil, err
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		unlockFile(f)
		f.Close()
		globalLockMu.Unlock()
	}, nil
}

// ---- JSON 原子读写 ----

// writeJSONAtomic 写 path.tmp 后原子替换（indent=2，ensure_ascii=False 语义）。
func writeJSONAtomic(path string, data any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(buf.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSONFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	return dec.Decode(out)
}

// ---- manifest ----

// StageDocument 写各章 ch{N}.json 并返回 manifest 内容（不写 manifest）。
func (s *RunStore) StageDocument(doc *ingest.Document) (map[string]any, error) {
	chapters := make([]any, 0, len(doc.Chapters))
	for _, c := range doc.Chapters {
		var tocEntryID any
		if v, ok := c.Meta["toc_entry_id"]; ok {
			tocEntryID = v
		}
		chapters = append(chapters, map[string]any{
			"index":        c.Index,
			"title":        c.Title,
			"href":         c.Href,
			"toc_entry_id": tocEntryID,
			"status":       StatusPending,
		})
		if err := s.SaveChapter(c); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"title":       doc.Title,
		"fmt":         doc.Fmt,
		"source_path": doc.SourcePath,
		"source_lang": doc.SourceLang,
		"target_lang": doc.TargetLang,
		"meta":        doc.Meta,
		"chapters":    chapters,
	}, nil
}

// SaveManifest 原子写 manifest。
func (s *RunStore) SaveManifest(manifest map[string]any) error {
	return writeJSONAtomic(s.ManifestPath(), manifest)
}

// LoadManifest 读 manifest。
func (s *RunStore) LoadManifest() (map[string]any, error) {
	var m map[string]any
	if err := readJSONFile(s.ManifestPath(), &m); err != nil {
		return nil, err
	}
	return normalizeJSONMap(m), nil
}

// normalizeJSONMap 把 json.Number 归一为 int64。
func normalizeJSONMap(m map[string]any) map[string]any {
	for k, v := range m {
		m[k] = normalizeJSONValue(v)
	}
	return m
}

func normalizeJSONValue(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case map[string]any:
		return normalizeJSONMap(t)
	case []any:
		for i, item := range t {
			t[i] = normalizeJSONValue(item)
		}
		return t
	default:
		return v
	}
}

// SetChapterStatus 更新章节状态并整份重写。
func (s *RunStore) SetChapterStatus(ci int, status string) error {
	m, err := s.LoadManifest()
	if err != nil {
		return err
	}
	chapters, _ := m["chapters"].([]any)
	for _, item := range chapters {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if idx, ok := cm["index"].(int64); ok && int(idx) == ci {
			cm["status"] = status
		}
	}
	return s.SaveManifest(m)
}

// PendingChapters status != done 的章索引（manifest 顺序）。
func (s *RunStore) PendingChapters() ([]int, error) {
	m, err := s.LoadManifest()
	if err != nil {
		return nil, err
	}
	var out []int
	chapters, _ := m["chapters"].([]any)
	for _, item := range chapters {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if status, _ := cm["status"].(string); status != StatusDone {
			if idx, ok := cm["index"].(int64); ok {
				out = append(out, int(idx))
			}
		}
	}
	return out, nil
}

// ---- 章节 ----

// SaveChapter 原子写章 JSON。
func (s *RunStore) SaveChapter(ch *ingest.Chapter) error {
	return writeJSONAtomic(s.ChapterPath(ch.Index), ch)
}

// LoadChapter 读章。
func (s *RunStore) LoadChapter(ci int) (*ingest.Chapter, error) {
	var ch ingest.Chapter
	if err := readJSONFile(s.ChapterPath(ci), &ch); err != nil {
		return nil, err
	}
	ch.Meta = normalizeJSONMap(ch.Meta)
	for _, seg := range ch.Segments {
		seg.Meta = normalizeJSONMap(seg.Meta)
	}
	return &ch, nil
}

// ---- 上下文 / 分析 / 报告 / 用量 ----

func (s *RunStore) SaveContext(data map[string]any) error {
	return writeJSONAtomic(s.ContextPath(), data)
}

func (s *RunStore) LoadContext() (map[string]any, error) {
	data, err := os.ReadFile(s.ContextPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return ParseContextJSON(data)
}

func (s *RunStore) SaveAnalysis(data map[string]any) error {
	return writeJSONAtomic(s.AnalysisPath(), data)
}

func (s *RunStore) LoadAnalysis() (map[string]any, error) {
	var m map[string]any
	if err := readJSONFile(s.AnalysisPath(), &m); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return normalizeJSONMap(m), nil
}

func (s *RunStore) SaveReport(data map[string]any) error {
	return writeJSONAtomic(s.ReportPath(), data)
}

func (s *RunStore) SaveUsage(data map[string]any) error {
	return writeJSONAtomic(s.UsagePath(), data)
}

func (s *RunStore) LoadUsage() (map[string]any, error) {
	var m map[string]any
	if err := readJSONFile(s.UsagePath(), &m); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	// 用量数字字段归一为 int64 / float64
	return normalizeUsageSummary(m), nil
}

func normalizeUsageSummary(m map[string]any) map[string]any {
	return normalizeJSONMap(m)
}

// LoadLatestReviewResult 最近一次 Review 的 result.json（目录名字典序倒序）。
func (s *RunStore) LoadLatestReviewResult() (map[string]any, error) {
	entries, err := os.ReadDir(s.ReviewsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		if !strings.HasPrefix(name, "review-") {
			continue
		}
		path := filepath.Join(s.ReviewsDir(), name, "result.json")
		var m map[string]any
		if err := readJSONFile(path, &m); err == nil && m != nil {
			return normalizeJSONMap(m), nil
		}
	}
	return nil, nil
}

// ---- 批次术语检查点 ----

// BatchGlossaryKey 检查点键 "{start_index}:{count}"。
func BatchGlossaryKey(startIndex, count int) string {
	return fmt.Sprintf("%d:%d", startIndex, count)
}

// CompletedBatchGlossaryKeys 从 events.jsonl 恢复该章已完成批次术语抽取的检查点键。
func (s *RunStore) CompletedBatchGlossaryKeys(chapter int) map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cacheLoaded {
		s.loadEventCacheLocked()
	}
	set := map[string]bool{}
	for k := range s.batchGlossaryCache[chapter] {
		set[k] = true
	}
	return set
}

func (s *RunStore) loadEventCacheLocked() {
	s.cacheLoaded = true
	f, err := os.Open(s.EventLogPath())
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		v, err := jsonx.Parse(line)
		if err != nil {
			continue // 容忍尾行截断
		}
		om, ok := v.(*jsonx.OMap)
		if !ok {
			continue
		}
		event, _ := om.Get("event")
		if event != "batch_glossary_extracted" {
			continue
		}
		ch, _ := om.Get("chapter")
		st, _ := om.Get("start_index")
		ct, _ := om.Get("count")
		chI, ok1 := ch.(int64)
		stI, ok2 := st.(int64)
		ctI, ok3 := ct.(int64)
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		if s.batchGlossaryCache[int(chI)] == nil {
			s.batchGlossaryCache[int(chI)] = map[string]bool{}
		}
		s.batchGlossaryCache[int(chI)][BatchGlossaryKey(int(stI), int(ctI))] = true
	}
}

// ---- 事件日志 ----

// LogEvent 追加一行事件（ts 秒级 ISO + event + data，sort_keys，ensure_ascii=False）。
func (s *RunStore) LogEvent(event string, data map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.EnsureDirs(); err != nil {
		return err
	}
	row := map[string]any{}
	for k, v := range data {
		row[k] = v
	}
	row["ts"] = time.Now().Format("2006-01-02T15:04:05Z07:00")
	row["event"] = event
	line, err := jsonx.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.EventLogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	if event == "batch_glossary_extracted" {
		ch, ok1 := toInt(row["chapter"])
		st, ok2 := toInt(row["start_index"])
		ct, ok3 := toInt(row["count"])
		if ok1 && ok2 && ok3 {
			if s.batchGlossaryCache[ch] == nil {
				s.batchGlossaryCache[ch] = map[string]bool{}
			}
			s.batchGlossaryCache[ch][BatchGlossaryKey(st, ct)] = true
		}
	}
	return nil
}

func toInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	}
	return 0, false
}
