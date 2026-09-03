// Package glossary 术语库（主规格 §8 + 分册 04）。
package glossary

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// 类型常量（主规格 §8.2）。
const (
	TypePerson     = "人物"
	TypeTerm       = "术语"
	TypeAppellation = "称谓"
	TypeHonorific  = "敬称"
	TypeSpeech     = "口癖"
	TypeFixedExpr  = "固定表达"
)

// SourceOnlyTypes 这些类型只按完整 source 匹配（避免裸名 alias 注入普通称呼）。
var SourceOnlyTypes = map[string]bool{
	TypeAppellation: true, TypeHonorific: true, TypeSpeech: true, TypeFixedExpr: true,
}

// Term 术语条目。
type Term struct {
	Source       string
	Target       string
	Reading      string
	Type         string
	Gender       string
	Aliases      []string
	FirstChapter *int
	Note         string
	Status       string
}

// Lite 转为 prompt 渲染用的精简结构。
func (t *Term) ToLite() (source, target, reading, typ, gender string, aliases []string) {
	return t.Source, t.Target, t.Reading, t.Type, t.Gender, t.Aliases
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS glossary (
    source        TEXT PRIMARY KEY,
    target        TEXT NOT NULL,
    reading       TEXT, type TEXT, gender TEXT,
    aliases       TEXT,
    first_chapter INTEGER, note TEXT,
    status        TEXT DEFAULT 'ok',
    updated_at    REAL
);
CREATE TABLE IF NOT EXISTS term_conflicts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL, existing_target TEXT, proposed_target TEXT,
    chapter INTEGER, note TEXT, resolved INTEGER DEFAULT 0, created_at REAL
);
DROP TABLE IF EXISTS translation_memory;
`

// Store SQLite 术语库（WAL + busy_timeout=5000）。
type Store struct {
	DB     *sql.DB
	DBPath string
}

// OpenStore 打开/创建术语库。
func OpenStore(path string) (*Store, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db, DBPath: path}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

// ---- 匹配算法（主规格 §8.3）----

var caseFolder = cases.Fold()

// MatchText 规范化全文：NFKC + casefold。
func MatchText(text string) string {
	return caseFolder.String(norm.NFKC.String(text))
}

func isASCIIRune(r rune) bool { return r < 0x80 }

func isASCIIWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

func isUnicodeWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// isLatinGreekCyrillicLetters：非 ASCII 但全为拉丁/希腊/西里尔字母。
func isLatinGreekCyrillicLetters(key string) bool {
	for _, r := range key {
		if isASCIIRune(r) {
			continue
		}
		if (r >= 0x0370 && r <= 0x03FF) || (r >= 0x0400 && r <= 0x04FF) ||
			(r >= 0x00C0 && r <= 0x024F) {
			continue
		}
		return false
	}
	return true
}

// patternKind：0 = ASCII 单词边界；1 = Unicode \w 边界；2 = 子串匹配（None）。
func patternKind(key string) int {
	allASCII := true
	for _, r := range key {
		if !isASCIIRune(r) {
			allASCII = false
			break
		}
	}
	if allASCII && key != "" {
		return 0
	}
	if isLatinGreekCyrillicLetters(key) && key != "" {
		return 1
	}
	return 2
}

// matchKeyInText 判定 key 是否在规范化文本中出现（按 patternKind 施加边界）。
func matchKeyInText(key, normalizedText string) bool {
	switch patternKind(key) {
	case 0:
		return containsWithBoundary(normalizedText, key, isASCIIWordRune)
	case 1:
		return containsWithBoundary(normalizedText, key, isUnicodeWordRune)
	default:
		return strings.Contains(normalizedText, key)
	}
}

func containsWithBoundary(text, key string, isWord func(rune) bool) bool {
	keyLen := len([]rune(key))
	runes := []rune(text)
	for i := 0; i+keyLen <= len(runes); i++ {
		if string(runes[i:i+keyLen]) != key {
			continue
		}
		if i > 0 && isWord(runes[i-1]) {
			continue
		}
		if i+keyLen < len(runes) && isWord(runes[i+keyLen]) {
			continue
		}
		return true
	}
	return false
}

// SourceMatchesText 判定术语 source 是否命中文本。
func SourceMatchesText(source, text string) bool {
	return matchKeyInText(MatchText(source), MatchText(text))
}

// MatchSources 该术语参与匹配的键（称谓类只按 source）。
func MatchSources(t *Term) []string {
	if SourceOnlyTypes[t.Type] {
		return []string{t.Source}
	}
	return append([]string{t.Source}, t.Aliases...)
}

// TermsIn 筛出在文本中出现的术语（供注入翻译 prompt）。
func TermsIn(terms []*Term, text string) []*Term {
	normalized := MatchText(text)
	var out []*Term
	for _, t := range terms {
		for _, key := range MatchSources(t) {
			if key == "" {
				continue
			}
			if matchKeyInText(MatchText(key), normalized) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// TermsInText 便捷方法：全表筛选。
func (s *Store) TermsInText(text string) ([]*Term, error) {
	terms, err := s.AllTerms()
	if err != nil {
		return nil, err
	}
	return TermsIn(terms, text), nil
}

// findOccurrences 返回 key 在文本中的全部区间（码点）。
func findOccurrences(key, normalizedText string) [][2]int {
	var out [][2]int
	keyRunes := []rune(key)
	runes := []rune(normalizedText)
	if len(keyRunes) == 0 {
		return nil
	}
	switch patternKind(key) {
	case 0, 1:
		var isWord func(rune) bool
		if patternKind(key) == 0 {
			isWord = isASCIIWordRune
		} else {
			isWord = isUnicodeWordRune
		}
		for i := 0; i+len(keyRunes) <= len(runes); i++ {
			if string(runes[i:i+len(keyRunes)]) != key {
				continue
			}
			if i > 0 && isWord(runes[i-1]) {
				continue
			}
			if i+len(keyRunes) < len(runes) && isWord(runes[i+len(keyRunes)]) {
				continue
			}
			out = append(out, [2]int{i, i + len(keyRunes)})
		}
	default:
		for i := 0; i+len(keyRunes) <= len(runes); i++ {
			if string(runes[i:i+len(keyRunes)]) == key {
				out = append(out, [2]int{i, i + len(keyRunes)})
			}
		}
	}
	return out
}

// mergedOccurrenceCount 重叠区间合并计数。
func mergedOccurrenceCount(intervals [][2]int) int {
	if len(intervals) == 0 {
		return 0
	}
	sorted := make([][2]int, len(intervals))
	copy(sorted, intervals)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i][0] < sorted[j][0] })
	count := 1
	curEnd := sorted[0][1]
	for _, iv := range sorted[1:] {
		if iv[0] < curEnd {
			if iv[1] > curEnd {
				curEnd = iv[1]
			}
			continue
		}
		count++
		curEnd = iv[1]
	}
	return count
}

// RecurringTerms 全书出现 ≥minOccurrences 次的术语（source+alias 命中合并计数）。
func RecurringTerms(terms []*Term, corpus string, minOccurrences int) []*Term {
	normalized := MatchText(corpus)
	var out []*Term
	for _, t := range terms {
		var intervals [][2]int
		for _, key := range MatchSources(t) {
			if key == "" {
				continue
			}
			intervals = append(intervals, findOccurrences(MatchText(key), normalized)...)
		}
		if mergedOccurrenceCount(intervals) >= minOccurrences {
			out = append(out, t)
		}
	}
	return out
}

// ---- CRUD ----

func scanTerm(row interface{ Scan(...any) error }) (*Term, error) {
	var t Term
	var reading, gender, note sql.NullString
	var aliases sql.NullString
	var firstChapter sql.NullInt64
	var typ sql.NullString
	var status sql.NullString
	if err := row.Scan(&t.Source, &t.Target, &reading, &typ, &gender, &aliases, &firstChapter, &note, &status); err != nil {
		return nil, err
	}
	t.Reading = reading.String
	t.Type = typ.String
	t.Gender = gender.String
	t.Note = note.String
	t.Status = status.String
	if aliases.Valid && aliases.String != "" {
		_ = json.Unmarshal([]byte(aliases.String), &t.Aliases)
	}
	if firstChapter.Valid {
		fc := int(firstChapter.Int64)
		t.FirstChapter = &fc
	}
	if t.Status == "" {
		t.Status = "ok"
	}
	return &t, nil
}

const termColumns = "source, target, reading, type, gender, aliases, first_chapter, note, status"

// GetTerm 按 source 查询。
func (s *Store) GetTerm(source string) (*Term, error) {
	row := s.DB.QueryRow("SELECT "+termColumns+" FROM glossary WHERE source = ?", source)
	t, err := scanTerm(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

// AllTerms 全表（ORDER BY type, source）。
func (s *Store) AllTerms() ([]*Term, error) {
	rows, err := s.DB.Query("SELECT " + termColumns + " FROM glossary ORDER BY type, source")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Term
	for rows.Next() {
		t, err := scanTerm(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func marshalAliases(aliases []string) string {
	if len(aliases) == 0 {
		return ""
	}
	b, _ := json.Marshal(aliases)
	return string(b)
}

func mergeAliases(existing, extra []string) []string {
	seen := map[string]bool{}
	out := append([]string{}, existing...)
	for _, a := range existing {
		seen[a] = true
	}
	for _, a := range extra {
		if a != "" && !seen[a] {
			out = append(out, a)
			seen[a] = true
		}
	}
	return out
}

// UpsertTerm 三态写入（BEGIN IMMEDIATE 事务；DSN 已配置 immediate 锁）：
// inserted / unchanged（合并 aliases、补全 reading/gender/note）/ conflict（保留现译法 + 记冲突）。
func (s *Store) UpsertTerm(term *Term, chapter *int) (string, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := float64(time.Now().UnixNano()) / 1e9

	existing, err := s.getTermTx(tx, term.Source)
	if err != nil {
		return "", err
	}
	if existing == nil {
		fc := any(nil)
		if chapter != nil {
			fc = *chapter
		}
		_, err = tx.Exec(
			"INSERT INTO glossary (source, target, reading, type, gender, aliases, first_chapter, note, status, updated_at) VALUES (?,?,?,?,?,?,?,?, 'ok', ?)",
			term.Source, term.Target, term.Reading, term.Type, term.Gender, marshalAliases(term.Aliases), fc, term.Note, now,
		)
		if err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return "inserted", nil
	}
	if existing.Target == term.Target {
		merged := mergeAliases(existing.Aliases, term.Aliases)
		reading := existing.Reading
		if reading == "" {
			reading = term.Reading
		}
		gender := existing.Gender
		if gender == "" {
			gender = term.Gender
		}
		note := existing.Note
		if note == "" {
			note = term.Note
		}
		_, err = tx.Exec(
			"UPDATE glossary SET aliases = ?, reading = ?, gender = ?, note = ?, updated_at = ? WHERE source = ?",
			marshalAliases(merged), reading, gender, note, now, term.Source,
		)
		if err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return "unchanged", nil
	}
	var chapterVal any
	if chapter != nil {
		chapterVal = *chapter
	}
	_, err = tx.Exec(
		"INSERT INTO term_conflicts (source, existing_target, proposed_target, chapter, note, resolved, created_at) VALUES (?,?,?,?,?,0,?)",
		term.Source, existing.Target, term.Target, chapterVal, term.Note, now,
	)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec("UPDATE glossary SET status = 'conflict', updated_at = ? WHERE source = ?", now, term.Source)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return "conflict", nil
}

func (s *Store) getTermTx(tx *sql.Tx, source string) (*Term, error) {
	row := tx.QueryRow("SELECT "+termColumns+" FROM glossary WHERE source = ?", source)
	t, err := scanTerm(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

// Conflict 未决冲突行。
type Conflict struct {
	ID              int64
	Source          string
	ExistingTarget  string
	ProposedTarget  string
	Chapter         *int
	Note            string
	Resolved        bool
	CreatedAt       float64
}

// OpenConflicts 未决冲突（按 created_at）。
func (s *Store) OpenConflicts() ([]*Conflict, error) {
	rows, err := s.DB.Query("SELECT id, source, existing_target, proposed_target, chapter, note, resolved, created_at FROM term_conflicts WHERE resolved = 0 ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Conflict
	for rows.Next() {
		var c Conflict
		var existing, proposed, note sql.NullString
		var chapter sql.NullInt64
		var resolved int
		if err := rows.Scan(&c.ID, &c.Source, &existing, &proposed, &chapter, &note, &resolved, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.ExistingTarget = existing.String
		c.ProposedTarget = proposed.String
		c.Note = note.String
		c.Resolved = resolved != 0
		if chapter.Valid {
			ch := int(chapter.Int64)
			c.Chapter = &ch
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// ResolveTerm 更新译法并恢复 ok。
func (s *Store) ResolveTerm(source, target string) (bool, error) {
	res, err := s.DB.Exec("UPDATE glossary SET target = ?, status = 'ok', updated_at = ? WHERE source = ?",
		target, float64(time.Now().UnixNano())/1e9, source)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// MarkConflictsResolved 标记全部未决冲突已解决。
func (s *Store) MarkConflictsResolved(source string) error {
	_, err := s.DB.Exec("UPDATE term_conflicts SET resolved = 1 WHERE source = ? AND resolved = 0", source)
	return err
}

// Stats {terms, open_conflicts}。
func (s *Store) Stats() (map[string]int, error) {
	var terms, conflicts int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM glossary").Scan(&terms); err != nil {
		return nil, err
	}
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM term_conflicts WHERE resolved = 0").Scan(&conflicts); err != nil {
		return nil, err
	}
	return map[string]int{"terms": terms, "open_conflicts": conflicts}, nil
}

// Resolve 人工裁决（resolver.py：resolve_term + mark_conflicts_resolved，顺序固定）。
func Resolve(store *Store, source, target string) (bool, error) {
	hit, err := store.ResolveTerm(source, target)
	if err != nil {
		return false, err
	}
	if !hit {
		return false, nil
	}
	if err := store.MarkConflictsResolved(source); err != nil {
		return false, err
	}
	return true, nil
}

// ---- 只读快照（Review 用）----

type fileSignature struct {
	size    int64
	modTime int64
	same    bool // 首次 stat 的 SameFile 结果
}

func statSignature(path string, first os.FileInfo) (fileSignature, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return fileSignature{}, err
	}
	sig := fileSignature{size: fi.Size(), modTime: fi.ModTime().UnixNano()}
	if first != nil {
		sig.same = os.SameFile(fi, first)
	}
	return sig, nil
}

// LoadTermsReadonly 复制 db+wal 到临时目录（复制前后签名一致才接受，≤5 次），
// 在副本上读全表，避免污染正式 WAL/shm。
func LoadTermsReadonly(dbPath string) ([]*Term, func(), error) {
	cleanup := func() {}
	tmpDir, err := os.MkdirTemp("", "wenyi-glossary-ro-")
	if err != nil {
		return nil, cleanup, err
	}
	cleanup = func() { os.RemoveAll(tmpDir) }
	for attempt := 0; attempt < 5; attempt++ {
		firstDB, err1 := os.Stat(dbPath)
		if err1 != nil {
			return nil, cleanup, err1
		}
		dstDB := filepath.Join(tmpDir, "glossary.db")
		if err := copyFile(dbPath, dstDB); err != nil {
			return nil, cleanup, err
		}
		walPath := dbPath + "-wal"
		hasWal := true
		if _, err := os.Stat(walPath); err != nil {
			hasWal = false
		}
		if hasWal {
			if err := copyFile(walPath, dstDB+"-wal"); err != nil {
				return nil, cleanup, err
			}
		}
		sigBeforeDB, _ := statSignature(dbPath, firstDB)
		sigAfterDB, err := statSignature(dbPath, firstDB)
		if err != nil {
			return nil, cleanup, err
		}
		_ = sigBeforeDB
		stable := sigAfterDB.same && sigAfterDB.size == firstDB.Size() && sigAfterDB.modTime == firstDB.ModTime().UnixNano()
		if hasWal {
			firstWAL, err := os.Stat(walPath)
			if err == nil {
				sigWAL, err := statSignature(walPath, firstWAL)
				if err != nil || !sigWAL.same || sigWAL.size != firstWAL.Size() || sigWAL.modTime != firstWAL.ModTime().UnixNano() {
					stable = false
				}
			} else {
				stable = false
			}
		}
		if stable {
			ro, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dstDB)+"?mode=ro&_pragma=busy_timeout(5000)")
			if err != nil {
				return nil, cleanup, err
			}
			rows, err := ro.Query("SELECT " + termColumns + " FROM glossary")
			if err != nil {
				ro.Close()
				return nil, cleanup, err
			}
			var out []*Term
			for rows.Next() {
				t, err := scanTerm(rows)
				if err != nil {
					rows.Close()
					ro.Close()
					return nil, cleanup, err
				}
				out = append(out, t)
			}
			rows.Close()
			ro.Close()
			return out, cleanup, rows.Err()
		}
		time.Sleep(time.Duration(attempt*10) * time.Millisecond)
	}
	return nil, cleanup, fmt.Errorf("glossary 只读快照失败：数据库在复制期间持续变化")
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
