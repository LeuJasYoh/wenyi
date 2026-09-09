// Package webserver 内嵌 WebUI 后端（分册 10 契约的 Go 实现，替代 Node serve.js）。
// 本文件：HTTP 服务与全部 REST/SSE 端点、配置端点（YAML→JSON 迁移）。
package webserver

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	_ "modernc.org/sqlite"

	"wenyi/internal/config"
)

var (
	slugRe     = regexp.MustCompile(`^[A-Za-z0-9_\x{4e00}-\x{9fff}\x{3040}-\x{30ff}\x{ac00}-\x{d7af}-]{1,64}$`)
	filenameRe = regexp.MustCompile(`^[^\x00-\x1f/\\:*?"<>|]{1,200}$`)
	unsafeName = regexp.MustCompile(`[^\w\x{4e00}-\x{9fff}\x{3040}-\x{30ff}.-]+`)
)

func isWindows() bool { return filepath.Separator == '\\' && os.PathListSeparator == ';' }

func isoNow() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// Options Serve 入参。
type Options struct {
	Port             int
	NoOpen           bool
	Version          string // 引擎/后端版本（cli.Version）
	ConfigPathFlag   string // 显式 -c（空 = 自动解析）
	StateDirFlag     string // 显式 --state-dir
	EnginePath       string // 引擎可执行（空 = os.Executable/WENYI_ENGINE）
	UIDirOverride    string // WENYI_UI_DIR 开发态磁盘 UI 目录
	Stdout           io.Writer
	DisableOpenCheck bool
}

// Server WebUI 后端实例。
type Server struct {
	opt      Options
	enginePath string
	version    string

	cfgMu       sync.Mutex
	configPath  string // 活动配置文件（迁移后指向 config.json）
	stateDir    string // 绝对路径
	engineEnv   []string
	uploadsBySlug map[string]string

	registry  *JobRegistry
	startTime time.Time

	handler http.Handler
}

// NewServer 构造后端（解析配置路径/状态目录/引擎路径/伪路由环境）。
func NewServer(opt Options) (*Server, error) {
	if opt.Version == "" {
		opt.Version = "dev"
	}
	s := &Server{opt: opt, version: opt.Version, registry: NewJobRegistry(), startTime: time.Now(), uploadsBySlug: map[string]string{}}

	// 引擎路径：env > 传入 > 自身可执行
	s.enginePath = os.Getenv("WENYI_ENGINE")
	if s.enginePath == "" {
		s.enginePath = opt.EnginePath
	}
	if s.enginePath == "" {
		if exe, err := os.Executable(); err == nil {
			s.enginePath = exe
		}
	}
	if s.enginePath == "" {
		return nil, fmt.Errorf("无法确定引擎可执行路径")
	}

	// 配置路径：显式 -c > exe 同目录 config.json > exe 同目录 config.yaml > CWD config.yaml
	cfgPath := opt.ConfigPathFlag
	if cfgPath == "" {
		cfgPath = config.ResolveDefaultPath()
		if cfgPath == "config.yaml" && !fileExists(cfgPath) {
			// 首次运行：在 exe 同目录生成默认 config.json
			if exe, err := os.Executable(); err == nil {
				p := filepath.Join(filepath.Dir(exe), "config.json")
				if _, err := config.CreateDefaultFile(p); err != nil {
					return nil, fmt.Errorf("生成默认配置失败：%w", err)
				}
				cfgPath = p
			}
		}
	}
	s.configPath = cfgPath

	// 状态目录：--state-dir > config.paths.state_dir > exe 同目录 state（相对路径锚定 exe 目录）
	cfg, err := config.Load(s.configPath)
	if err != nil {
		cfg = config.DefaultConfig() // 配置损坏时降级默认，由设置页修复
	}
	stateDir := opt.StateDirFlag
	if stateDir == "" {
		stateDir = cfg.StateDir
		if stateDir == "" {
			stateDir = "state"
		}
	}
	if !filepath.IsAbs(stateDir) {
		if exe, err := os.Executable(); err == nil {
			stateDir = filepath.Join(filepath.Dir(exe), stateDir)
		} else {
			abs, _ := filepath.Abs(stateDir)
			stateDir = abs
		}
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("状态目录不可用：%w", err)
	}
	s.stateDir = stateDir

	// 基础环境（伪路由开关改为每次 spawn 时按当前配置计算，见 engineEnvForSpawn）
	s.engineEnv = os.Environ()

	s.handler = s.buildHandler()
	return s, nil
}

// engineEnvForSpawn 按「当前活动配置」派生 spawn 环境：
// provider=fake 时注入 WENYI_FAKE_ROUTING=1（离线体验模式路由开关，界面切换后即时生效）。
func (s *Server) engineEnvForSpawn() []string {
	s.cfgMu.Lock()
	path := s.configPath
	base := s.engineEnv
	s.cfgMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return base
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return base
	}
	if llm, _ := raw["llm"].(map[string]any); llm != nil {
		if p, _ := llm["provider"].(string); strings.EqualFold(p, "fake") {
			for _, kv := range base {
				if kv == "WENYI_FAKE_ROUTING=1" {
					return base
				}
			}
			return append(append([]string{}, base...), "WENYI_FAKE_ROUTING=1")
		}
	}
	return base
}

// Handler 暴露 http.Handler（测试用）。
func (s *Server) Handler() http.Handler { return s.handler }

// ConfigPath 当前活动配置文件绝对路径。
func (s *Server) ConfigPath() string {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	if abs, err := filepath.Abs(s.configPath); err == nil {
		return abs
	}
	return s.configPath
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// ---- 错误信封与通用助手 ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errReply(w http.ResponseWriter, status int, code, message string, extra map[string]any) {
	envelope := map[string]any{"code": code, "message": message, "exitCode": nil, "engineStderr": nil}
	for k, v := range extra {
		envelope[k] = v
	}
	writeJSON(w, status, map[string]any{"error": envelope})
}

func readJSONBody(r *http.Request) (map[string]any, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func (s *Server) bookDir(slug string) string    { return filepath.Join(s.stateDir, slug) }
func (s *Server) manifestPath(slug string) string { return filepath.Join(s.bookDir(slug), "manifest.json") }

func readJSONFile(p string) (map[string]any, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// requireBook 校验 slug 与 manifest 存在性；失败时已写响应，返回 false。
func (s *Server) requireBook(w http.ResponseWriter, r *http.Request) (string, bool) {
	slug := r.PathValue("slug")
	if !slugRe.MatchString(slug) {
		errReply(w, 400, "INVALID_SLUG", "非法书籍标识", nil)
		return "", false
	}
	if !fileExists(s.manifestPath(slug)) {
		errReply(w, 404, "BOOK_NOT_FOUND", "书籍不存在", nil)
		return "", false
	}
	return slug, true
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// ---- 路由构建 ----

func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/meta", s.handleMeta)

	mux.HandleFunc("GET /api/v1/books", s.handleBooks)
	mux.HandleFunc("GET /api/v1/books/{slug}", s.handleBook)
	mux.HandleFunc("GET /api/v1/books/{slug}/status", s.handleBookStatus)

	mux.HandleFunc("GET /api/v1/books/{slug}/chapters", s.handleChapters)
	mux.HandleFunc("GET /api/v1/books/{slug}/chapters/{n}", s.handleChapter)

	mux.HandleFunc("POST /api/v1/books/{slug}/prepare", s.jobStarter("prepare", nil))
	mux.HandleFunc("POST /api/v1/books/{slug}/translate", s.jobStarter("translate", buildTranslateArgv))
	mux.HandleFunc("POST /api/v1/books/{slug}/review", s.jobStarter("review", nil))
	mux.HandleFunc("POST /api/v1/books/{slug}/qa", s.jobStarter("qa", nil))
	mux.HandleFunc("POST /api/v1/books/{slug}/report", s.jobStarter("report", nil))
	mux.HandleFunc("POST /api/v1/books/{slug}/assemble", s.jobStarter("assemble", buildAssembleArgv))

	mux.HandleFunc("GET /api/v1/jobs", s.handleJobs)
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.handleJob)
	mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", s.handleJobCancel)
	mux.HandleFunc("GET /api/v1/jobs/{id}/events", s.handleJobSSE)

	mux.HandleFunc("GET /api/v1/books/{slug}/glossary", s.handleGlossary)
	mux.HandleFunc("GET /api/v1/books/{slug}/glossary/conflicts", s.handleGlossaryConflicts)
	mux.HandleFunc("POST /api/v1/books/{slug}/glossary/conflicts/resolve", s.handleGlossaryResolve)

	mux.HandleFunc("GET /api/v1/books/{slug}/reviews", s.handleReviews)
	mux.HandleFunc("GET /api/v1/books/{slug}/reviews/{reviewId}", s.handleReviewResult)

	mux.HandleFunc("GET /api/v1/books/{slug}/report", s.handleReport)
	mux.HandleFunc("GET /api/v1/books/{slug}/usage", s.handleUsage)

	mux.HandleFunc("GET /api/v1/books/{slug}/exports", s.handleExports)
	mux.HandleFunc("GET /api/v1/books/{slug}/exports/{name}", s.handleExportDownload)

	mux.HandleFunc("GET /api/v1/books/{slug}/events", s.handleEvents)
	mux.HandleFunc("GET /api/v1/books/{slug}/events/stream", s.handleEventsSSE)

	mux.HandleFunc("GET /api/v1/config", s.handleConfigGet)
	mux.HandleFunc("PUT /api/v1/config", s.handleConfigPut)
	mux.HandleFunc("DELETE /api/v1/config", s.handleConfigDelete)

	mux.HandleFunc("POST /api/v1/uploads", s.handleUpload)

	mux.Handle("/", s.staticHandler())

	return s.hostGuard(mux)
}

// hostGuard §1.3 第二道闸：仅允许本地 Host。
func (s *Server) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.opt.DisableOpenCheck {
			host := strings.ToLower(r.Host)
			hostname, _, err := net.SplitHostPort(host)
			if err != nil {
				hostname = host
			}
			if hostname != "127.0.0.1" && hostname != "localhost" && hostname != "" && hostname != "::1" {
				errReply(w, 403, "FORBIDDEN_HOST", "仅允许本地访问", nil)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---- 系统（§4.1）----

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "uptimeSec": int(time.Since(s.startTime).Seconds())})
}

func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	pdfAvailable := fileExists(filepath.Join(filepath.Dir(s.enginePath), "wenyi-pdf", "Wenyi.Pdf.dll"))
	writeJSON(w, 200, map[string]any{
		"backendVersion":  s.version,
		"enginePath":      s.enginePath,
		"engineVersion":   s.version,
		"engineAvailable": fileExists(s.enginePath),
		"nodeRuntime":     "go-embedded",
		"stateDir":        s.stateDir,
		"configPath":      s.ConfigPath(),
		"pdfAvailable":    pdfAvailable,
		"capabilities": map[string]any{
			"translate": true, "review": true, "assemble": true, "pdf": pdfAvailable,
		},
	})
}

// ---- 书架（§4.2）----

func (s *Server) handleBooks(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	offset := queryInt(r, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	limit := queryInt(r, "limit", 50)
	if limit > 200 {
		limit = 200
	}
	if limit < 0 {
		limit = 0
	}
	entries, err := os.ReadDir(s.stateDir)
	if err != nil {
		entries = nil
	}
	items := []map[string]any{}
	skipped := 0
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "uploads" {
			continue
		}
		mp := s.manifestPath(entry.Name())
		manifest, err := readJSONFile(mp)
		if err != nil {
			skipped++
			items = append(items, map[string]any{"slug": entry.Name(), "title": entry.Name(), "fmt": nil,
				"sourceLang": nil, "targetLang": nil, "chaptersTotal": 0, "chaptersDone": 0, "status": "idle", "updatedAt": nil})
			continue
		}
		chapters, _ := manifest["chapters"].([]any)
		done := 0
		for _, c := range chapters {
			if cm, ok := c.(map[string]any); ok && cm["status"] == "done" {
				done++
			}
		}
		status := "idle"
		if s.registry.activeJob(entry.Name()) != nil {
			status = "translating"
		} else if len(chapters) > 0 && done == len(chapters) {
			status = "done"
		} else if len(chapters) > 0 {
			status = "prepared"
		}
		updatedAt := ""
		if st, err := os.Stat(mp); err == nil {
			updatedAt = st.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")
		}
		items = append(items, map[string]any{
			"slug": entry.Name(), "title": strOrDefault(manifest["title"], entry.Name()),
			"fmt": manifest["fmt"], "sourceLang": manifest["source_lang"], "targetLang": manifest["target_lang"],
			"chaptersTotal": len(chapters), "chaptersDone": done, "status": status, "updatedAt": nilIfEmpty(updatedAt),
		})
	}
	filtered := items
	if q != "" {
		filtered = nil
		for _, it := range items {
			title := strings.ToLower(strOrDefault(it["title"], ""))
			slug := strOrDefault(it["slug"], "")
			if strings.Contains(title, q) || strings.Contains(slug, q) {
				filtered = append(filtered, it)
			}
		}
	}
	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSON(w, 200, map[string]any{
		"items": filtered[offset:end], "total": total, "offset": offset, "limit": limit,
		"details": map[string]any{"skipped": skipped},
	})
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Server) handleBook(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	raw, err := os.ReadFile(s.manifestPath(slug))
	if err != nil {
		errReply(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	updatedAt := ""
	if st, err := os.Stat(s.manifestPath(slug)); err == nil {
		updatedAt = st.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")
	}
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	fmt.Fprintf(w, `{"slug":%s,"manifest":%s,"updatedAt":%s}`, mustJSON(slug), raw, mustJSON(updatedAt))
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// openGlossaryRO 只读打开书级术语库（不存在返回 nil）。
func (s *Server) openGlossaryRO(slug string) (*sql.DB, error) {
	p := filepath.Join(s.bookDir(slug), "glossary.db")
	if !fileExists(p) {
		return nil, os.ErrNotExist
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(p)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	return db, nil
}

func (s *Server) handleBookStatus(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	manifest, err := readJSONFile(s.manifestPath(slug))
	if err != nil {
		errReply(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	glossary := map[string]any{"terms": 0, "open_conflicts": 0}
	if db, err := s.openGlossaryRO(slug); err == nil {
		glossary["terms"] = countRow(db, "SELECT COUNT(*) FROM glossary")
		glossary["open_conflicts"] = countRow(db, "SELECT COUNT(*) FROM term_conflicts WHERE resolved=0")
		db.Close()
	}
	chapters := []map[string]any{}
	for _, c := range asAnyList(manifest["chapters"]) {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		chapters = append(chapters, map[string]any{"index": cm["index"], "title": cm["title"], "status": cm["status"]})
	}
	writeJSON(w, 200, map[string]any{"chapters": chapters, "glossary": glossary})
}

func countRow(db *sql.DB, query string) int {
	n := 0
	_ = db.QueryRow(query).Scan(&n)
	return n
}

func asAnyList(v any) []any {
	l, _ := v.([]any)
	return l
}

// ---- 章节（§4.3）----

func (s *Server) handleChapters(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	offset := queryInt(r, "offset", 0)
	limit := queryInt(r, "limit", 50)
	if limit > 200 {
		limit = 200
	}
	manifest, err := readJSONFile(s.manifestPath(slug))
	if err != nil {
		errReply(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	items := []map[string]any{}
	for _, c := range asAnyList(manifest["chapters"]) {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		idx := numberOf(cm["index"])
		item := map[string]any{"index": cm["index"], "title": cm["title"], "status": cm["status"]}
		chPath := filepath.Join(s.bookDir(slug), "chapters", "ch"+strconv.Itoa(int(idx))+".json")
		if ch, err := readJSONFile(chPath); err == nil {
			segments := asAnyList(ch["segments"])
			item["titleTranslated"] = ch["title_translated"]
			item["segmentCount"] = len(segments)
			if st, err := os.Stat(chPath); err == nil {
				item["updatedAt"] = st.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")
			}
		}
		items = append(items, item)
	}
	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSON(w, 200, map[string]any{"items": items[offset:end], "total": total, "offset": offset, "limit": limit})
}

func (s *Server) handleChapter(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 {
		errReply(w, 400, "VALIDATION_FAILED", "非法章节号", nil)
		return
	}
	chPath := filepath.Join(s.bookDir(slug), "chapters", "ch"+strconv.Itoa(n)+".json")
	raw, err := os.ReadFile(chPath)
	if err != nil {
		errReply(w, 404, "CHAPTER_NOT_FOUND", "章节不存在", nil)
		return
	}
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write(raw)
}

// ---- 任务（§4.4）----

func (s *Server) jobStarter(kind string, argvBuilder func(map[string]any) ([]string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if !slugRe.MatchString(slug) {
			errReply(w, 400, "INVALID_SLUG", "非法书籍标识", nil)
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			errReply(w, 400, "VALIDATION_FAILED", "请求体必须是 JSON 对象", nil)
			return
		}
		var sourcePath string
		if fileExists(s.manifestPath(slug)) {
			if manifest, err := readJSONFile(s.manifestPath(slug)); err == nil {
				sourcePath = strOrDefault(manifest["source_path"], "")
			}
		} else if kind == "prepare" {
			// 新书豁免（§4.11）：sourcePath ← uploads 返回值
			sourcePath = strOrDefault(body["sourcePath"], "")
			if sourcePath == "" {
				s.cfgMu.Lock()
				sourcePath = s.uploadsBySlug[slug]
				s.cfgMu.Unlock()
			}
			if sourcePath == "" || !fileExists(sourcePath) {
				errReply(w, 400, "VALIDATION_FAILED", "新书需要先上传文件（sourcePath 缺失或无效）", nil)
				return
			}
			_ = os.MkdirAll(s.bookDir(slug), 0o755)
		}
		if sourcePath == "" {
			errReply(w, 409, "MANIFEST_INCOMPLETE", "manifest 缺少 source_path", nil)
			return
		}
		if active := s.registry.activeJob(slug); active != nil {
			errReply(w, 409, "BUSY", fmt.Sprintf("本书已有运行中的任务 %s（kind=%s）", active.ID, active.Kind),
				map[string]any{"details": map[string]any{"activeJobId": active.ID}})
			return
		}
		var extraArgv []string
		if argvBuilder != nil {
			extraArgv, err = argvBuilder(body)
			if err != nil {
				status, code := 400, "VALIDATION_FAILED"
				if ae, ok := err.(*argvError); ok {
					status, code = ae.status, ae.code
				}
				errReply(w, status, code, err.Error(), nil)
				return
			}
		}

		s.cfgMu.Lock()
		cfgPath := s.configPath
		s.cfgMu.Unlock()
		job := s.registry.newJob(kind, slug)
		argv := []string{"-c", cfgPath, "--state-dir", s.stateDir, kind, sourcePath}
		argv = append(argv, extraArgv...)
		spawnErr := s.registry.spawnEngine(s.enginePath, argv, workDir(), job, s.engineEnvForSpawn(), func(exitCode *int) {
			if exitCode != nil && *exitCode == 0 && (kind == "prepare" || kind == "translate") {
				if manifest, err := readJSONFile(s.manifestPath(slug)); err == nil {
					chapters := asAnyList(manifest["chapters"])
					done := 0
					for _, c := range chapters {
						if cm, ok := c.(map[string]any); ok && cm["status"] == "done" {
							done++
						}
					}
					var result any
					if kind == "prepare" {
						result = map[string]any{"chapters": len(chapters)}
					} else {
						result = map[string]any{"chaptersDone": done, "chaptersTotal": len(chapters)}
					}
					s.registry.update(job.ID, func(x *Job) { x.Result = result }, "", nil)
				}
			}
		})
		if spawnErr != nil {
			now := isoNow()
			s.registry.update(job.ID, func(x *Job) { x.Status = "failed"; x.FinishedAt = &now },
				"job.status", map[string]any{"status": "failed", "error": spawnErr.Error()})
			errReply(w, 500, "ENGINE_SPAWN_FAILED", "引擎启动失败："+spawnErr.Error(), nil)
			return
		}
		writeJSON(w, 202, map[string]any{"job": job})
	}
}

func workDir() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	offset := queryInt(r, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	limit := queryInt(r, "limit", 50)
	if limit > 200 {
		limit = 200
	}
	writeJSON(w, 200, s.registry.list(offset, limit, r.URL.Query().Get("book")))
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	job := s.registry.get(r.PathValue("id"))
	if job == nil {
		errReply(w, 404, "JOB_NOT_FOUND", "任务不存在", nil)
		return
	}
	if r.URL.Query().Get("includeLog") != "1" {
		job.LogTail = ""
	}
	writeJSON(w, 200, map[string]any{"job": job})
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	job := s.registry.cancel(r.PathValue("id"))
	if job == nil {
		errReply(w, 404, "JOB_NOT_FOUND", "任务不存在", nil)
		return
	}
	job.LogTail = ""
	writeJSON(w, 200, map[string]any{"job": job})
}

// ---- Job SSE（§2.1）----

func (s *Server) handleJobSSE(w http.ResponseWriter, r *http.Request) {
	job := s.registry.get(r.PathValue("id"))
	if job == nil {
		errReply(w, 404, "JOB_NOT_FOUND", "任务不存在", nil)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		errReply(w, 500, "INTERNAL", "流式响应不可用", nil)
		return
	}
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")
	w.WriteHeader(200)

	seq := 0
	send := func(event string, data any) {
		seq++
		fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", seq, event, mustJSON(data))
		flusher.Flush()
	}
	send("job.status", map[string]any{"status": job.Status})
	if job.progressView != nil {
		send("job.progress", job.progressView)
	}
	events, unsub := s.registry.subscribe(job.ID)
	defer unsub()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-events:
			send(ev.Type, ev.Data)
		case <-heartbeat.C:
			send("heartbeat", map[string]any{"t": isoNow()})
		}
	}
}

// ---- 术语（§4.5 直连 glossary.db）----

// rowsToMaps 通用行扫描（列名 → 值；[]byte → string）。
func rowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		dest := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := map[string]any{}
		for i, c := range cols {
			if b, ok := dest[i].([]byte); ok {
				m[c] = string(b)
			} else {
				m[c] = dest[i]
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// glossary.aliases 为 JSON 数组字符串；NULL/空/坏值容错为 []。
func safeParseList(v any) []string {
	s, ok := v.(string)
	if !ok || s == "" {
		return []string{}
	}
	var parsed []string
	if json.Unmarshal([]byte(s), &parsed) == nil {
		return parsed
	}
	return []string{}
}

func (s *Server) handleGlossary(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	q := strings.ToLower(r.URL.Query().Get("q"))
	typ := r.URL.Query().Get("type")
	status := r.URL.Query().Get("status")
	offset := queryInt(r, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	limit := queryInt(r, "limit", 50)
	if limit > 200 {
		limit = 200
	}
	db, err := s.openGlossaryRO(slug)
	if err != nil {
		writeJSON(w, 200, map[string]any{
			"items": []any{}, "total": 0, "offset": offset, "limit": limit,
			"stats": map[string]any{"terms": 0, "open_conflicts": 0},
		})
		return
	}
	defer db.Close()
	rows, err := db.Query("SELECT * FROM glossary ORDER BY type, source")
	if err != nil {
		errReply(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	all, err := rowsToMaps(rows)
	if err != nil {
		errReply(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	rows.Close()
	conflicts := countRow(db, "SELECT COUNT(*) FROM term_conflicts WHERE resolved=0")

	filtered := []map[string]any{}
	for _, r0 := range all {
		r0["aliases"] = safeParseList(r0["aliases"])
		if q != "" && !strings.Contains(strings.ToLower(strOrDefault(r0["source"], "")), q) &&
			!strings.Contains(strings.ToLower(strOrDefault(r0["target"], "")), q) {
			continue
		}
		if typ != "" && strOrDefault(r0["type"], "") != typ {
			continue
		}
		if status != "" && strOrDefault(r0["status"], "") != status {
			continue
		}
		filtered = append(filtered, r0)
	}
	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSON(w, 200, map[string]any{
		"items": filtered[offset:end], "total": total, "offset": offset, "limit": limit,
		"stats": map[string]any{"terms": len(all), "open_conflicts": conflicts},
	})
}

func (s *Server) handleGlossaryConflicts(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	offset := queryInt(r, "offset", 0)
	limit := queryInt(r, "limit", 50)
	if limit > 200 {
		limit = 200
	}
	db, err := s.openGlossaryRO(slug)
	if err != nil {
		writeJSON(w, 200, map[string]any{"items": []any{}, "total": 0, "offset": offset, "limit": limit})
		return
	}
	defer db.Close()
	rows, err := db.Query("SELECT * FROM term_conflicts WHERE resolved=0 ORDER BY created_at")
	if err != nil {
		errReply(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	items, err := rowsToMaps(rows)
	if err != nil {
		errReply(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSON(w, 200, map[string]any{"items": items[offset:end], "total": total, "offset": offset, "limit": limit})
}

func (s *Server) handleGlossaryResolve(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	body, err := readJSONBody(r)
	if err != nil {
		errReply(w, 400, "VALIDATION_FAILED", "请求体必须是 JSON 对象", nil)
		return
	}
	source := strOrDefault(body["source"], "")
	target := strOrDefault(body["target"], "")
	if source == "" || target == "" {
		errReply(w, 400, "VALIDATION_FAILED", "source/target 不能为空", nil)
		return
	}
	manifest, err := readJSONFile(s.manifestPath(slug))
	if err != nil {
		errReply(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	sourcePath := strOrDefault(manifest["source_path"], "")
	if sourcePath == "" {
		errReply(w, 409, "MANIFEST_INCOMPLETE", "manifest 缺少 source_path", nil)
		return
	}
	s.cfgMu.Lock()
	cfgPath := s.configPath
	s.cfgMu.Unlock()
	cmd := exec.Command(s.enginePath, "-c", cfgPath, "--state-dir", s.stateDir,
		"glossary", "resolve", sourcePath, source, target)
	cmd.Env = s.engineEnvForSpawn()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		errReply(w, 404, "TERM_NOT_FOUND", "术语不存在或裁决失败", map[string]any{"exitCode": nil, "engineStderr": err.Error()})
		return
	}
	stderrTail := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := stderr.Read(buf)
		if n > 0 {
			stderrTail = append(stderrTail, buf[:n]...)
			if len(stderrTail) > 4096 {
				stderrTail = stderrTail[len(stderrTail)-4096:]
			}
		}
		if err != nil {
			break
		}
	}
	waitErr := cmd.Wait()
	if waitErr == nil {
		writeJSON(w, 200, map[string]any{"resolved": true})
		return
	}
	code := -1
	var ee *exec.ExitError
	if ok := errAs(waitErr, &ee); ok {
		code = ee.ExitCode()
	}
	errReply(w, 404, "TERM_NOT_FOUND", "术语不存在或裁决失败",
		map[string]any{"exitCode": code, "engineStderr": string(stderrTail)})
}

func errAs(err error, target **exec.ExitError) bool {
	for err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ---- 审校（§4.6）----

func (s *Server) handleReviews(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	offset := queryInt(r, "offset", 0)
	limit := queryInt(r, "limit", 50)
	if limit > 200 {
		limit = 200
	}
	entries, _ := os.ReadDir(filepath.Join(s.bookDir(slug), "reviews"))
	items := []map[string]any{}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "review-") {
			continue
		}
		result, err := readJSONFile(filepath.Join(s.bookDir(slug), "reviews", entry.Name(), "result.json"))
		if err != nil {
			continue
		}
		startedAt, _ := result["started_at"].(string)
		finishedAt, _ := result["finished_at"].(string)
		if _, ok := result["started_at"]; !ok {
			startedAt = ""
		}
		items = append(items, map[string]any{
			"id": entry.Name(), "status": strOrDefault(result["status"], "unknown"),
			"termination": result["termination"], "issueCount": len(asAnyList(result["issues"])),
			"changeCount": len(asAnyList(result["changes"])), "startedAt": nilIfEmpty(startedAt),
			"finishedAt": nilIfEmpty(finishedAt),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strOrDefault(items[i]["startedAt"], "") > strOrDefault(items[j]["startedAt"], "")
	})
	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSON(w, 200, map[string]any{"items": items[offset:end], "total": total, "offset": offset, "limit": limit})
}

var reviewIDRe = regexp.MustCompile(`^review-[A-Za-z0-9._-]+$`)

func (s *Server) handleReviewResult(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	reviewID := r.PathValue("reviewId")
	if !reviewIDRe.MatchString(reviewID) {
		errReply(w, 400, "INVALID_PATH", "非法审校 ID", nil)
		return
	}
	rp := filepath.Join(s.bookDir(slug), "reviews", reviewID, "result.json")
	raw, err := os.ReadFile(rp)
	if err != nil {
		errReply(w, 404, "REVIEW_NOT_FOUND", "审校结果不存在", nil)
		return
	}
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write(raw)
}

// ---- QA 与用量（§4.7）----

func serveStateFile(w http.ResponseWriter, r *http.Request, path, notFoundCode, notFoundMsg string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		errReply(w, 404, notFoundCode, notFoundMsg, nil)
		return
	}
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write(raw)
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	serveStateFile(w, r, filepath.Join(s.bookDir(slug), "report.json"), "REPORT_NOT_FOUND", "报告不存在")
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	serveStateFile(w, r, filepath.Join(s.bookDir(slug), "usage.json"), "USAGE_NOT_FOUND", "用量数据不存在")
}

// ---- 导出（§4.8）----

func (s *Server) exportDir(slug string) string {
	manifest, err := readJSONFile(s.manifestPath(slug))
	if err != nil {
		return ""
	}
	sourcePath := strOrDefault(manifest["source_path"], "")
	if sourcePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(sourcePath), "output")
}

func (s *Server) handleExports(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	outDir := s.exportDir(slug)
	items := []map[string]any{}
	if outDir != "" {
		if entries, err := os.ReadDir(outDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				full := filepath.Join(outDir, entry.Name())
				st, err := os.Stat(full)
				if err != nil {
					continue
				}
				ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(entry.Name()), "."))
				format := ext
				if ext == "md" {
					format = "markdown"
				}
				items = append(items, map[string]any{
					"name": entry.Name(), "size": st.Size(),
					"mtime":     st.ModTime().UTC().Format("2006-01-02T15:04:05.000Z"),
					"format":    format,
					"bilingual": strings.Contains(entry.Name(), "-bi") || strings.Contains(entry.Name(), ".zh-bi"),
				})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return strOrDefault(items[i]["mtime"], "") > strOrDefault(items[j]["mtime"], "")
	})
	writeJSON(w, 200, map[string]any{"items": items, "total": len(items), "offset": 0, "limit": 50})
}

func (s *Server) handleExportDownload(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if !filenameRe.MatchString(name) {
		errReply(w, 400, "INVALID_PATH", "非法文件名", nil)
		return
	}
	outDir := s.exportDir(slug)
	full := filepath.Join(outDir, name)
	if outDir == "" || !fileExists(full) {
		errReply(w, 404, "EXPORT_NOT_FOUND", "导出文件不存在", nil)
		return
	}
	w.Header().Set("content-disposition", `attachment; filename*=UTF-8''`+url.PathEscape(name))
	http.ServeFile(w, r, full)
}

// ---- 事件（§4.9 + SSE §2.4 游标协议）----

func (s *Server) readEvents(slug string, cursor, limit int) (map[string]any, *httpError) {
	ep := filepath.Join(s.bookDir(slug), "events.jsonl")
	raw, err := os.ReadFile(ep)
	if err != nil {
		return map[string]any{"items": []any{}, "nextCursor": 0, "total": 0}, nil
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if cursor > len(lines) {
		return nil, &httpError{status: 400, code: "INVALID_CURSOR", msg: "游标超界"}
	}
	end := cursor + limit
	if end > len(lines) {
		end = len(lines)
	}
	items := make([]any, 0, end-cursor)
	for i := cursor; i < end; i++ {
		var parsed map[string]any
		if json.Unmarshal([]byte(lines[i]), &parsed) == nil {
			parsed["line"] = i
			items = append(items, parsed)
		} else {
			items = append(items, map[string]any{"line": i, "raw": lines[i]})
		}
	}
	return map[string]any{"items": items, "nextCursor": end, "total": len(lines)}, nil
}

type httpError struct {
	status int
	code   string
	msg    string
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	limit := queryInt(r, "limit", 500)
	if limit > 2000 {
		limit = 2000
	}
	res, herr := s.readEvents(slug, queryInt(r, "cursor", 0), limit)
	if herr != nil {
		errReply(w, herr.status, herr.code, herr.msg, nil)
		return
	}
	writeJSON(w, 200, res)
}

var lineIDRe = regexp.MustCompile(`line=(\d+)`)

func (s *Server) handleEventsSSE(w http.ResponseWriter, r *http.Request) {
	slug, ok := s.requireBook(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		errReply(w, 500, "INTERNAL", "流式响应不可用", nil)
		return
	}
	ep := filepath.Join(s.bookDir(slug), "events.jsonl")
	cursor := 0
	if m := lineIDRe.FindStringSubmatch(r.Header.Get("Last-Event-ID")); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			cursor = n
		}
	}
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")
	w.WriteHeader(200)

	seq := cursor
	lastLineCount := -1
	readLines := func() []string {
		raw, err := os.ReadFile(ep)
		if err != nil {
			return nil
		}
		var lines []string
		for _, l := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, l)
			}
		}
		return lines
	}
	pushLines := func() {
		lines := readLines()
		if lastLineCount > len(lines) {
			seq++
			fmt.Fprintf(w, "id: %d\nevent: reset\ndata: %s\n\n", seq, mustJSON(map[string]any{"reason": "truncated", "line": 0}))
			cursor = 0
			flusher.Flush()
		}
		lastLineCount = len(lines)
		for cursor < len(lines) {
			fmt.Fprintf(w, "id: line=%d\nevent: engine.event\ndata: %s\n\n", cursor, lines[cursor])
			cursor++
		}
		flusher.Flush()
	}
	pushLines()
	watcher := time.NewTicker(1 * time.Second)
	defer watcher.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-watcher.C:
			pushLines()
		case <-heartbeat.C:
			seq++
			fmt.Fprintf(w, "id: %d\nevent: heartbeat\ndata: %s\n\n", seq, mustJSON(map[string]any{"t": isoNow()}))
			flusher.Flush()
		}
	}
}

// ---- 配置（§4.10：JSON 优先 + 旧 YAML 一次性迁移）----

func (s *Server) handleConfigGet(w http.ResponseWriter, _ *http.Request) {
	s.cfgMu.Lock()
	path := s.configPath
	s.cfgMu.Unlock()
	exists := fileExists(path)
	raw := ""
	if exists {
		if b, err := os.ReadFile(path); err == nil {
			raw = string(b)
		} else {
			exists = false
		}
	}
	var parsed any
	errors := []map[string]any{}
	if exists && strings.TrimSpace(raw) != "" {
		var v any
		if err := yaml.Unmarshal([]byte(raw), &v); err != nil {
			errors = append(errors, map[string]any{"line": nil, "message": err.Error()})
		} else {
			parsed = v
		}
	}
	abs, _ := filepath.Abs(path)
	writeJSON(w, 200, map[string]any{"path": abs, "exists": exists, "raw": raw, "parsed": parsed, "errors": errors})
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONBody(r)
	if err != nil {
		errReply(w, 400, "VALIDATION_FAILED", "请求体必须是 JSON 对象", nil)
		return
	}
	content, _ := body["content"].(string)
	if _, present := body["content"]; !present || !isString(body["content"]) {
		errReply(w, 400, "VALIDATION_FAILED", "content 必须为字符串", nil)
		return
	}
	// 语法校验（yaml.Unmarshal 兼容 JSON 子集）
	var v any
	if err := yaml.Unmarshal([]byte(content), &v); err != nil {
		errReply(w, 422, "CONFIG_PARSE_ERROR", "配置语法错误", map[string]any{
			"details": map[string]any{"errors": []map[string]any{{"line": nil, "message": err.Error()}}},
		})
		return
	}
	// 语义校验（比 Node 版更强：直接跑引擎 FromDict）
	if m, ok := v.(map[string]any); ok && v != nil {
		if _, err := config.FromDict(m); err != nil {
			errReply(w, 422, "CONFIG_PARSE_ERROR", "配置语义错误", map[string]any{
				"details": map[string]any{"errors": []map[string]any{{"line": nil, "message": err.Error()}}},
			})
			return
		}
	}

	s.cfgMu.Lock()
	target := s.configPath
	// 迁移：旧 config.yaml → 写 config.json，旧文件改名 .bak（一次性）
	if strings.HasSuffix(strings.ToLower(target), ".yaml") || strings.HasSuffix(strings.ToLower(target), ".yml") {
		jsonPath := strings.TrimSuffix(target, filepath.Ext(target)) + ".json"
		tmp := jsonPath + ".tmp"
		if err := writeAtomic(tmp, jsonPath, content); err != nil {
			s.cfgMu.Unlock()
			errReply(w, 500, "INTERNAL", "写入配置失败："+err.Error(), nil)
			return
		}
		if fileExists(target) {
			_ = os.Rename(target, target+".bak")
		}
		s.configPath = jsonPath
		s.cfgMu.Unlock()
		abs, _ := filepath.Abs(jsonPath)
		writeJSON(w, 200, map[string]any{"path": abs, "errors": []any{}})
		return
	}
	tmp := target + ".tmp"
	if err := writeAtomic(tmp, target, content); err != nil {
		s.cfgMu.Unlock()
		errReply(w, 500, "INTERNAL", "写入配置失败："+err.Error(), nil)
		return
	}
	s.cfgMu.Unlock()
	abs, _ := filepath.Abs(target)
	writeJSON(w, 200, map[string]any{"path": abs, "errors": []any{}})
}

func isString(v any) bool {
	_, ok := v.(string)
	return ok
}

func writeAtomic(tmp, target, content string) error {
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func (s *Server) handleConfigDelete(w http.ResponseWriter, _ *http.Request) {
	s.cfgMu.Lock()
	path := s.configPath
	s.cfgMu.Unlock()
	if fileExists(path) {
		_ = os.Remove(path)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- 上传（§4.11）----

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 210<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		errReply(w, 400, "VALIDATION_FAILED", "multipart 解析失败："+err.Error(), nil)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		errReply(w, 400, "VALIDATION_FAILED", "缺少 file 字段", nil)
		return
	}
	defer file.Close()
	originalName := filepath.Base(header.Filename)
	if !filenameRe.MatchString(originalName) {
		errReply(w, 400, "INVALID_PATH", "非法文件名", nil)
		return
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(originalName), "."))
	switch ext {
	case "epub", "txt", "md", "html", "fb2", "pdf":
	default:
		errReply(w, 400, "VALIDATION_FAILED", "不支持的扩展名："+ext, nil)
		return
	}
	uploadsDir := filepath.Join(s.stateDir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		errReply(w, 500, "INTERNAL", "创建上传目录失败", nil)
		return
	}
	safeName := unsafeName.ReplaceAllString(originalName, "_")
	target := filepath.Join(uploadsDir, safeName)
	buf, err := io.ReadAll(file)
	if err != nil {
		errReply(w, 500, "INTERNAL", "读取上传内容失败", nil)
		return
	}
	if st, err := os.Stat(target); err != nil || st.Size() != int64(len(buf)) {
		if err := os.WriteFile(target, buf, 0o644); err != nil {
			errReply(w, 500, "INTERNAL", "保存上传文件失败", nil)
			return
		}
	}
	stem := strings.TrimSuffix(safeName, filepath.Ext(safeName))
	slug := Slugify(stem)
	abs, _ := filepath.Abs(target)
	s.cfgMu.Lock()
	s.uploadsBySlug[slug] = abs
	s.cfgMu.Unlock()
	writeJSON(w, 201, map[string]any{"sourcePath": abs, "slug": slug, "size": len(buf), "ext": ext})
}
