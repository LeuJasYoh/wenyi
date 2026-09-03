package review

// review_run.go 对应 pipeline/review_run.py（主规格 §12.1 + 分册 07 §5.7）。
// Review 运行目录 <book_run_dir>/reviews/review-{YYYYmmdd-HHMMSS-ffffff}[-NN]/ 的持久化。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"wenyi/internal/jsonx"
)

// ReviewCandidateID 对应 review_candidate_id：
// "{r{round}-}ch{chapter}-base{chunk_base}-candidate{ordinal}"；ordinal 是块内枚举位置。
func ReviewCandidateID(chapter, chunkBase, ordinal int, reviewRound *int) string {
	prefix := ""
	if reviewRound != nil {
		prefix = fmt.Sprintf("r%d-", *reviewRound)
	}
	return fmt.Sprintf("%sch%d-base%d-candidate%d", prefix, chapter, chunkBase, ordinal)
}

// ReviewRunStore 审校运行目录。
type ReviewRunStore struct {
	RunDir  string // reviews/review-… 目录
	ReviewID string // 目录名

	mu              sync.Mutex
	seq             int64
	round           int // 0 = 不在 round_scope 内
	startedAt       time.Time
	contentDigest   string

	initialRows []map[string]any
	dismissedRows []map[string]any
}

// NewReviewRunStore 创建运行目录；同名时间戳目录追加 -01、-02…（绝不覆盖）。
func NewReviewRunStore(bookRunDir string, now *time.Time) (*ReviewRunStore, error) {
	moment := time.Now()
	if now != nil {
		moment = *now
	}
	stamp := moment.Format("20060102-150405-000000")
	base := filepath.Join(bookRunDir, "reviews")
	name := "review-" + stamp
	dir := filepath.Join(base, name)
	suffix := 0
	for {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		// MkdirAll 不报已存在错误，用独占哨兵文件防并发覆盖
		sentinel := filepath.Join(dir, ".created")
		f, err := os.OpenFile(sentinel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			break
		}
		suffix++
		name = fmt.Sprintf("review-%s-%02d", stamp, suffix)
		dir = filepath.Join(base, name)
	}
	return &ReviewRunStore{
		RunDir:     dir,
		ReviewID:   name,
		startedAt:  moment,
		contentDigest: "",
	}, nil
}

// Path 返回运行目录内相对路径（round_scope 内自动加 rounds/{NNN:03d}/ 前缀）。
func (s *ReviewRunStore) Path(relative string) string {
	s.mu.Lock()
	round := s.round
	s.mu.Unlock()
	if round > 0 {
		relative = filepath.ToSlash(filepath.Join(fmt.Sprintf("rounds/%03d", round), relative))
	}
	return filepath.Join(s.RunDir, filepath.FromSlash(relative))
}

// RoundScope 进入轮次目录作用域；嵌套报错（主规格 §12.1）。
func (s *ReviewRunStore) RoundScope(round int) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.round != 0 {
		return nil, fmt.Errorf("round_scope 不可嵌套（当前已在第 %d 轮）", s.round)
	}
	s.round = round
	return func() {
		s.mu.Lock()
		s.round = 0
		s.mu.Unlock()
	}, nil
}

// WriteJSON 原子写 JSON（.tmp + rename；indent=2、键排序）。
func (s *ReviewRunStore) WriteJSON(relative string, data any) error {
	return writeReviewJSON(s.Path(relative), data)
}

func writeReviewJSON(path string, data any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// map 键排序 + indent 2 + 不转义 HTML（等价 json.dump(..., sort_keys=True, indent=2, ensure_ascii=False)）
	out, err := marshalSortedIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func marshalSortedIndent(v any, prefix, indent string) ([]byte, error) {
	var buf strings.Builder
	if err := encodeSorted(&buf, v, prefix, indent); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

func encodeSorted(b *strings.Builder, v any, prefix, indent string) error {
	inner := prefix + indent
	switch t := v.(type) {
	case *jsonx.OMap, map[string]any:
		keys, m, _ := sortedKeysOf(t)
		if len(keys) == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteString("{\n")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(inner)
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			b.Write(unescapeHTML(kb))
			b.WriteString(": ")
			if err := encodeSorted(b, m[k], inner, indent); err != nil {
				return err
			}
		}
		b.WriteString("\n" + prefix + "}")
	case []map[string]any:
		return encodeSorted(b, toAnySlice(t), prefix, indent)
	case []any, []string:
		items, _ := sliceOf(t)
		if len(items) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteString("[\n")
		for i, item := range items {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(inner)
			if err := encodeSorted(b, item, inner, indent); err != nil {
				return err
			}
		}
		b.WriteString("\n" + prefix + "]")
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		b.Write(unescapeHTML(raw))
	}
	return nil
}

func unescapeHTML(b []byte) []byte {
	s := string(b)
	s = strings.ReplaceAll(s, `\u003c`, "<")
	s = strings.ReplaceAll(s, `\u003e`, ">")
	s = strings.ReplaceAll(s, `\u0026`, "&")
	return []byte(s)
}

func toAnySlice(in any) []any {
	items, _ := sliceOf(in)
	return items
}

// LogEvent 追加事件行：{"seq", "ts"(微秒 ISO), "event", ...data}；
// round_scope 内且未显式给 review_round 时自动补当前轮。
func (s *ReviewRunStore) LogEvent(event string, data map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := map[string]any{}
	for k, v := range data {
		row[k] = v
	}
	s.seq++
	row["seq"] = s.seq
	row["ts"] = time.Now().Format("2006-01-02T15:04:05.000000")
	row["event"] = event
	if _, given := row["review_round"]; !given && s.round > 0 {
		row["review_round"] = s.round
	}
	line, err := jsonx.Marshal(row)
	if err != nil {
		return err
	}
	path := filepath.Join(s.RunDir, "events.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// RecordInitialIssues 记录初审问题快照：加 candidate_id/chapter/index(章内)/review_round；
// 非 int 或 bool 的 index 跳过（主规格 §12.1）。
func (s *ReviewRunStore) RecordInitialIssues(chapter, chunkBase int, issues []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var round *int
	if s.round > 0 {
		r := s.round
		round = &r
	}
	for ordinal, issue := range issues {
		idx, ok := StrictInt(issue["index"])
		if !ok {
			continue
		}
		row := CopyIssue(issue)
		row["candidate_id"] = ReviewCandidateID(chapter, chunkBase, ordinal, round)
		row["chapter"] = chapter
		row["index"] = chunkBase + idx
		if round != nil {
			row["review_round"] = *round
		} else {
			row["review_round"] = 0
		}
		s.initialRows = append(s.initialRows, row)
	}
}

// RecordDismissed 记录驳回快照（无 candidate_id 注入，issue 自带字段保留）。
func (s *ReviewRunStore) RecordDismissed(chapter, chunkBase int, issues []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var round int
	if s.round > 0 {
		round = s.round
	}
	for _, issue := range issues {
		idx, ok := StrictInt(issue["index"])
		if !ok {
			continue
		}
		row := CopyIssue(issue)
		row["chapter"] = chapter
		row["index"] = chunkBase + idx
		row["review_round"] = round
		s.dismissedRows = append(s.dismissedRows, row)
	}
}

// ResultSnapshots 按 (review_round, chapter, index) 排序返回 (initial, dismissed)；
// round 非 nil 时只返回该轮。
func (s *ReviewRunStore) ResultSnapshots(round *int) ([]map[string]any, []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pick := func(rows []map[string]any) []map[string]any {
		out := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			if round != nil {
				r, ok := StrictInt(row["review_round"])
				if !ok || r != *round {
					continue
				}
			}
			out = append(out, row)
		}
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i], out[j]
			ra, _ := StrictInt(a["review_round"])
			rb, _ := StrictInt(b["review_round"])
			if ra != rb {
				return ra < rb
			}
			ca, _ := StrictInt(a["chapter"])
			cb, _ := StrictInt(b["chapter"])
			if ca != cb {
				return ca < cb
			}
			ia, _ := StrictInt(a["index"])
			ib, _ := StrictInt(b["index"])
			return ia < ib
		})
		return out
	}
	return pick(s.initialRows), pick(s.dismissedRows)
}

// Start 写 rounds/metadata.json + 运行中占位 result.json + review_started 事件。
func (s *ReviewRunStore) Start(reviewedContentDigest string, metadata map[string]any) error {
	s.mu.Lock()
	s.contentDigest = reviewedContentDigest
	s.mu.Unlock()
	meta := map[string]any{
		"review_id":              s.ReviewID,
		"reviewed_content_digest": reviewedContentDigest,
	}
	for k, v := range metadata {
		meta[k] = v
	}
	if err := s.WriteJSON("rounds/metadata.json", meta); err != nil {
		return err
	}
	if err := writeReviewJSON(filepath.Join(s.RunDir, "result.json"), map[string]any{
		"review_id":              s.ReviewID,
		"status":                "running",
		"termination":           "not_started",
		"reviewed_content_digest": reviewedContentDigest,
		"started_at":            s.startedAt.Format("2006-01-02T15:04:05.000000"),
	}); err != nil {
		return err
	}
	return s.LogEvent("review_started", map[string]any{
		"review_id": s.ReviewID, "reviewed_content_digest": reviewedContentDigest,
	})
}

// Finish 写终稿 result.json + review_finished 事件，返回 result 内容。
func (s *ReviewRunStore) Finish(status, termination string, summary map[string]any,
	issues, changes []map[string]any, errInfo map[string]any) (map[string]any, error) {
	if issues == nil {
		issues = []map[string]any{}
	}
	if changes == nil {
		changes = []map[string]any{}
	}
	result := map[string]any{
		"review_id":              s.ReviewID,
		"status":                status,
		"termination":           termination,
		"reviewed_content_digest": s.contentDigest,
		"started_at":            s.startedAt.Format("2006-01-02T15:04:05.000000"),
		"finished_at":           time.Now().Format("2006-01-02T15:04:05.000000"),
		"summary":               summary,
		"issues":                issues,
		"changes":               changes,
	}
	if errInfo != nil {
		result["error"] = errInfo
	}
	if err := writeReviewJSON(filepath.Join(s.RunDir, "result.json"), result); err != nil {
		return nil, err
	}
	eventData := map[string]any{
		"review_id": s.ReviewID, "status": status, "termination": termination,
		"issue_count": len(issues), "change_count": len(changes),
	}
	if errInfo != nil {
		eventData["error_type"] = errInfo["type"]
		eventData["error"] = errInfo["message"]
	}
	if err := s.LogEvent("review_finished", eventData); err != nil {
		return nil, err
	}
	return result, nil
}

// SaveUsage 写 usage.json（要求含 totals）+ review_usage_recorded 事件（展开 totals）。
func (s *ReviewRunStore) SaveUsage(usage map[string]any) error {
	if err := writeReviewJSON(filepath.Join(s.RunDir, "usage.json"), usage); err != nil {
		return err
	}
	eventData := map[string]any{"review_id": s.ReviewID}
	if totals, ok := usage["totals"].(map[string]any); ok {
		for k, v := range totals {
			eventData[k] = v
		}
	}
	return s.LogEvent("review_usage_recorded", eventData)
}
