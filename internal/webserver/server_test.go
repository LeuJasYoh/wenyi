// webserver 验收测试：移植 node/test/web-api.test.js（14 例）+ web-e2e.test.js（3 例）。
// 运行前提：go 工具链可用（TestMain 现场构建引擎）、node 在 PATH（assemble 组装用）。
package webserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	engineBinPath string // TestMain 构建的引擎
	repoRoot      string
)

func TestMain(m *testing.M) {
	var err error
	repoRoot, err = filepath.Abs(filepath.Join(".", "..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tmp, err := os.MkdirTemp("", "wenyi-engine-")
	if err != nil {
		os.Exit(1)
	}
	engineBinPath = filepath.Join(tmp, "wenyi-test-engine.exe")
	build := exec.Command("go", "build", "-o", engineBinPath, "./cmd/wenyi")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "构建测试引擎失败：%v\n%s\n", err, out)
		os.Exit(1)
	}
	_ = os.Setenv("WENYI_ENGINE", engineBinPath)
	_ = os.Setenv("WENYI_NODE_CLI", filepath.Join(repoRoot, "node", "cli.js"))
	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

// ---- 测试夹具 ----

const testConfigYAML = `language:
  source: ja
  target: zh
llm:
  provider: fake
  tiers:
    strong: {model: p}
    cheap: {model: f}
pipeline:
  review: %s
  polish: false
  consistency_qa: %s
  book_understanding: false
paths:
  state_dir: state
`

type testServer struct {
	t          *testing.T
	srv        *httptest.Server
	server     *Server
	dir        string
	stateDir   string
	configPath string
}

func makeServer(t *testing.T, withBook bool) *testServer {
	t.Helper()
	return makeServerCfg(t, withBook, "false", "false")
}

func makeServerCfg(t *testing.T, withBook bool, review, qa string) *testServer {
	t.Helper()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(testConfigYAML, review, qa)), 0o644); err != nil {
		t.Fatal(err)
	}
	if withBook {
		txt := filepath.Join(dir, "novel.txt")
		if err := copyFile(filepath.Join(repoRoot, "testdata", "sample.txt"), txt); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(engineBinPath, "-c", configPath, "translate", txt,
			"--review=false", "--qa=false", "--polish=false")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "WENYI_FAKE_ROUTING=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("预置书籍失败：%v\n%s", err, out)
		}
	}
	server, err := NewServer(Options{Version: "test", ConfigPathFlag: configPath, StateDirFlag: stateDir})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv := httptest.NewServer(server.Handler())
	t.Cleanup(srv.Close)
	return &testServer{t: t, srv: srv, server: server, dir: dir, stateDir: stateDir, configPath: configPath}
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// do 发送 JSON 请求并解析响应。返回状态码与 body map（非 JSON 时为 nil）。
func (ts *testServer) do(method, path string, body any, host string) (int, map[string]any) {
	ts.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, ts.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	if host != "" {
		req.Host = host
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		ts.t.Fatalf("请求失败 %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return res.StatusCode, m
}

func (ts *testServer) upload(name string, content []byte) (int, map[string]any) {
	ts.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(content)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", ts.srv.URL+"/api/v1/uploads", &buf)
	req.Header.Set("content-type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		ts.t.Fatalf("上传失败: %v", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return res.StatusCode, m
}

func (ts *testServer) waitJob(jobID string, timeout time.Duration) map[string]any {
	ts.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, m := ts.do("GET", "/api/v1/jobs/"+jobID, nil, "")
		if job, ok := m["job"].(map[string]any); ok && job["status"] != "running" {
			return job
		}
		time.Sleep(400 * time.Millisecond)
	}
	ts.t.Fatalf("任务超时：%s", jobID)
	return nil
}

func num(t *testing.T, v any) float64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("期望数字，实际 %T %v", v, v)
	}
	return f
}

func str(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("期望字符串，实际 %T %v", v, v)
	}
	return s
}

// ---- §5 映射表逐行 argv 断言 ----

func TestTranslateArgvNullFields(t *testing.T) {
	if argv, _ := buildTranslateArgv(map[string]any{}); len(argv) != 0 {
		t.Errorf("空 body 应无开关，实际 %v", argv)
	}
	argv, _ := buildTranslateArgv(map[string]any{"polish": true, "review": false})
	if fmt.Sprint(argv) != "[--polish --no-review]" {
		t.Errorf("polish/review 映射错误：%v", argv)
	}
	argv, _ = buildTranslateArgv(map[string]any{"mono": false, "bilingual": true})
	if fmt.Sprint(argv) != "[--no-mono --bilingual]" {
		t.Errorf("mono/bilingual 映射错误：%v", argv)
	}
}

func TestTranslateArgvChapterExclusive(t *testing.T) {
	_, err := buildTranslateArgv(map[string]any{"chapter": float64(0), "polish": true})
	if err == nil || !strings.Contains(err.Error(), "chapter") {
		t.Fatalf("应报 CHAPTER_EXCLUSIVE，实际 %v", err)
	}
	argv, _ := buildTranslateArgv(map[string]any{"chapter": float64(3)})
	if fmt.Sprint(argv) != "[--chapter 3]" {
		t.Errorf("chapter 映射错误：%v", argv)
	}
}

func TestAssembleArgvValidation(t *testing.T) {
	argv, _ := buildAssembleArgv(map[string]any{"format": "txt"})
	if fmt.Sprint(argv) != "[--format txt]" {
		t.Errorf("format 映射错误：%v", argv)
	}
	argv, _ = buildAssembleArgv(map[string]any{"pdfEngine": "questpdf", "bilingual": true})
	if fmt.Sprint(argv) != "[--pdf-engine questpdf --bilingual]" {
		t.Errorf("pdfEngine 映射错误：%v", argv)
	}
	if _, err := buildAssembleArgv(map[string]any{"format": "docx"}); err == nil {
		t.Error("非法 format 应报错")
	}
	if _, err := buildAssembleArgv(map[string]any{"pdfEngine": "bogus"}); err == nil {
		t.Error("非法 pdfEngine 应报错")
	}
}

func TestSlugifyMatchesEngine(t *testing.T) {
	if Slugify("novel") != "novel" {
		t.Error("novel")
	}
	if Slugify("サンプル小説") != "サンプル小説" {
		t.Error("日文应保留")
	}
	if Slugify("///") != "book" {
		t.Error("全非法应回退 book")
	}
}

// ---- 安全：slug / Host ----

func TestInvalidSlugAndForbiddenHost(t *testing.T) {
	ts := makeServer(t, true)
	// httptest 会把 /../ 规整；直接构造非法 slug
	status, m := ts.do("GET", "/api/v1/books/" + strings.Repeat("a", 100), nil, "127.0.0.1")
	_ = m
	if status != 400 && status != 404 {
		t.Fatalf("超长 slug 应 400/404，实际 %d", status)
	}
	status, m = ts.do("GET", "/api/v1/health", nil, "evil.example.com")
	if status != 403 {
		t.Fatalf("非本地 Host 应 403，实际 %d", status)
	}
	if errCode(t, m) != "FORBIDDEN_HOST" {
		t.Fatalf("错误码应 FORBIDDEN_HOST，实际 %v", m)
	}
}

func errCode(t *testing.T, m map[string]any) string {
	t.Helper()
	e, _ := m["error"].(map[string]any)
	if e == nil {
		t.Fatalf("缺少 error 信封：%v", m)
	}
	return str(t, e["code"])
}

// ---- 书籍端点投影 ----

func TestBooksListStatusChapters(t *testing.T) {
	ts := makeServer(t, true)
	status, m := ts.do("GET", "/api/v1/books", nil, "")
	if status != 200 {
		t.Fatalf("books 应 200，实际 %d", status)
	}
	items, _ := m["items"].([]any)
	var found map[string]any
	for _, it := range items {
		if b, _ := it.(map[string]any); b != nil && b["slug"] == "novel" {
			found = b
		}
	}
	if found == nil {
		t.Fatal("应有 novel")
	}
	if num(t, found["chaptersDone"]) != 2 || num(t, found["chaptersTotal"]) != 2 {
		t.Fatalf("章节数错误：%v", found)
	}
	if found["status"] != "done" {
		t.Fatalf("状态应为 done，实际 %v", found["status"])
	}
	// 404
	status, m = ts.do("GET", "/api/v1/books/nope", nil, "")
	if status != 404 || errCode(t, m) != "BOOK_NOT_FOUND" {
		t.Fatalf("应 404 BOOK_NOT_FOUND，实际 %d %v", status, m)
	}
	// status（术语计数）
	status, m = ts.do("GET", "/api/v1/books/novel/status", nil, "")
	if status != 200 {
		t.Fatalf("status 应 200，实际 %d", status)
	}
	gl, _ := m["glossary"].(map[string]any)
	if gl == nil || num(t, gl["terms"]) < 2 {
		t.Fatalf("glossary.terms 应 ≥2，实际 %v", gl)
	}
	// chapters 透传
	status, _ = ts.do("GET", "/api/v1/books/novel/chapters/0", nil, "")
	if status != 200 {
		t.Fatalf("chapters/0 应 200，实际 %d", status)
	}
}

// ---- 事件 + 游标 ----

func TestEventsPaginationAndCursorBounds(t *testing.T) {
	ts := makeServer(t, true)
	status, m := ts.do("GET", "/api/v1/books/novel/events?cursor=0&limit=5", nil, "")
	if status != 200 {
		t.Fatalf("events 应 200，实际 %d", status)
	}
	total := int(num(t, m["total"]))
	if total <= 0 {
		t.Fatal("应有事件")
	}
	items, _ := m["items"].([]any)
	if len(items) > 5 {
		t.Fatalf("limit=5 应最多 5 条，实际 %d", len(items))
	}
	first, _ := items[0].(map[string]any)
	if num(t, first["line"]) != 0 {
		t.Fatalf("首条 line 应为 0，实际 %v", first["line"])
	}
	status, m = ts.do("GET", fmt.Sprintf("/api/v1/books/novel/events?cursor=%d", total+100), nil, "")
	if status != 400 || errCode(t, m) != "INVALID_CURSOR" {
		t.Fatalf("游标超界应 400 INVALID_CURSOR，实际 %d %v", status, m)
	}
}

// ---- 术语直连 ----

func TestGlossaryDirectReadWithFilters(t *testing.T) {
	ts := makeServer(t, true)
	status, m := ts.do("GET", "/api/v1/books/novel/glossary?q=%E5%A0%80%E5%8C%97", nil, "") // q=堀北
	if status != 200 {
		t.Fatalf("glossary 应 200，实际 %d", status)
	}
	stats, _ := m["stats"].(map[string]any)
	if stats == nil || num(t, stats["terms"]) < 2 {
		t.Fatalf("stats.terms 应 ≥2，实际 %v", stats)
	}
	items, _ := m["items"].([]any)
	for _, it := range items {
		tm, _ := it.(map[string]any)
		if tm != nil {
			if _, ok := tm["aliases"].([]any); !ok {
				t.Fatal("aliases 应为数组（safeParse）")
			}
		}
	}
	status, _ = ts.do("GET", "/api/v1/books/novel/glossary/conflicts", nil, "")
	if status != 200 {
		t.Fatalf("conflicts 应 200，实际 %d", status)
	}
}

// ---- 任务：受理 / BUSY / 取消幂等 ----

func TestTranslateJobBusyAndCancel(t *testing.T) {
	ts := makeServer(t, true)
	status, m := ts.do("POST", "/api/v1/books/novel/translate", map[string]any{}, "")
	if status != 202 {
		t.Fatalf("translate 应 202，实际 %d %v", status, m)
	}
	job1, _ := m["job"].(map[string]any)
	if !strings.HasPrefix(str(t, job1["id"]), "job-") {
		t.Fatalf("job id 前缀错误：%v", job1["id"])
	}
	// 并发 → 409 BUSY
	status, m = ts.do("POST", "/api/v1/books/novel/translate", map[string]any{}, "")
	if status != 409 || errCode(t, m) != "BUSY" {
		t.Fatalf("并发应 409 BUSY，实际 %d %v", status, m)
	}
	e, _ := m["error"].(map[string]any)
	details, _ := e["details"].(map[string]any)
	if details == nil || details["activeJobId"] != job1["id"] {
		t.Fatalf("BUSY details.activeJobId 应为 %v，实际 %v", job1["id"], details)
	}
	// 等待结束（fake 快速）
	job := ts.waitJob(str(t, job1["id"]), 60*time.Second)
	if job["status"] == "running" {
		t.Fatal("任务应已结束")
	}
	// 结束后取消幂等
	status, _ = ts.do("POST", "/api/v1/jobs/"+str(t, job1["id"])+"/cancel", nil, "")
	if status != 200 {
		t.Fatalf("取消应 200，实际 %d", status)
	}
	// chapter 互斥
	status, m = ts.do("POST", "/api/v1/books/novel/translate", map[string]any{"chapter": 0, "polish": true}, "")
	if status != 400 || errCode(t, m) != "CHAPTER_EXCLUSIVE" {
		t.Fatalf("chapter 互斥应 400 CHAPTER_EXCLUSIVE，实际 %d %v", status, m)
	}
}

// ---- report / usage 透传 ----

func TestReportUsagePassthrough(t *testing.T) {
	ts := makeServer(t, true)
	status, _ := ts.do("GET", "/api/v1/books/novel/report", nil, "")
	if status != 200 {
		t.Fatalf("report 应 200，实际 %d", status)
	}
	status, _ = ts.do("GET", "/api/v1/books/novel/usage", nil, "")
	if status != 200 && status != 404 {
		t.Fatalf("usage 应 200/404，实际 %d", status)
	}
	status, m := ts.do("GET", "/api/v1/books/novel2/report", nil, "")
	if status != 404 {
		t.Fatalf("不存在书籍应 404，实际 %d %v", status, m)
	}
}

// ---- config：get/put/delete + JSON 迁移 ----

func TestConfigEndpointsAndJSONMigration(t *testing.T) {
	ts := makeServer(t, true)
	status, m := ts.do("GET", "/api/v1/config", nil, "")
	if status != 200 {
		t.Fatalf("config get 应 200，实际 %d", status)
	}
	if !strings.Contains(str(t, m["raw"]), "provider: fake") {
		t.Fatalf("raw 应含 provider: fake，实际 %v", m["raw"])
	}
	// 语法错误 → 422
	status, m = ts.do("PUT", "/api/v1/config", map[string]any{"content": "a:\n  - b\n c:\n"}, "")
	if status == 422 && errCode(t, m) != "CONFIG_PARSE_ERROR" {
		t.Fatalf("422 时错误码应 CONFIG_PARSE_ERROR，实际 %v", m)
	}
	if status >= 500 {
		t.Fatalf("语法错误应 4xx，实际 %d", status)
	}
	// 合法写入 → YAML 迁移为 config.json，旧文件 .bak
	status, m = ts.do("PUT", "/api/v1/config", map[string]any{"content": "language:\n  source: ja\n  target: zh\nllm:\n  provider: fake\n"}, "")
	if status != 200 {
		t.Fatalf("put 应 200，实际 %d %v", status, m)
	}
	newPath := str(t, m["path"])
	if !strings.HasSuffix(newPath, "config.json") {
		t.Fatalf("迁移后应写 config.json，实际 %s", newPath)
	}
	if !fileExists(newPath) {
		t.Fatal("config.json 应存在")
	}
	if !fileExists(ts.configPath + ".bak") {
		t.Fatal("旧 config.yaml 应改名 .bak")
	}
	if fileExists(ts.configPath) {
		t.Fatal("旧 config.yaml 原路径不应再存在")
	}
	// GET 走新路径
	_, m = ts.do("GET", "/api/v1/config", nil, "")
	if str(t, m["path"]) != newPath {
		t.Fatalf("GET 应返回新路径 %s，实际 %s", newPath, m["path"])
	}
	parsed, _ := m["parsed"].(map[string]any)
	if parsed == nil || parsed["llm"] == nil {
		t.Fatal("parsed 应可解析")
	}
	// 语义错误 → 422（比 Node 版更强）
	status, m = ts.do("PUT", "/api/v1/config", map[string]any{"content": "llm:\n  provider: fake\npipeline:\n  stage_tiers:\n    translator: mega\n"}, "")
	if status != 422 {
		t.Fatalf("非法档位应 422，实际 %d %v", status, m)
	}
	// delete
	status, _ = ts.do("DELETE", "/api/v1/config", nil, "")
	if status != 200 {
		t.Fatalf("delete 应 200，实际 %d", status)
	}
}

// ---- 上传闭环 ----

func TestUploadPrepareClosedLoop(t *testing.T) {
	ts := makeServer(t, false)
	content, _ := os.ReadFile(filepath.Join(repoRoot, "testdata", "sample.txt"))
	status, m := ts.upload("novel.txt", content)
	if status != 201 {
		t.Fatalf("上传应 201，实际 %d %v", status, m)
	}
	sourcePath := str(t, m["sourcePath"])
	if !strings.Contains(filepath.ToSlash(sourcePath), "uploads") {
		t.Fatalf("sourcePath 应在 uploads 下，实际 %s", sourcePath)
	}
	if m["slug"] != "novel" {
		t.Fatalf("slug 应 novel，实际 %v", m["slug"])
	}
	// 重复上传幂等
	status, m = ts.upload("novel.txt", content)
	if status != 201 || str(t, m["sourcePath"]) != sourcePath {
		t.Fatalf("重复上传应幂等，实际 %d %v", status, m)
	}
	// prepare（新书豁免）
	status, m = ts.do("POST", "/api/v1/books/novel/prepare", map[string]any{"sourcePath": sourcePath}, "")
	if status != 202 {
		t.Fatalf("prepare 应 202，实际 %d %v", status, m)
	}
	job := ts.waitJob(str(t, m["job"].(map[string]any)["id"]), 120*time.Second)
	if job["status"] != "succeeded" {
		t.Fatalf("prepare 应成功，实际 %v（log=%v）", job["status"], job["logTail"])
	}
	// manifest 落盘 → 书架出现
	_, m = ts.do("GET", "/api/v1/books", nil, "")
	items, _ := m["items"].([]any)
	var found map[string]any
	for _, it := range items {
		if b, _ := it.(map[string]any); b != nil && b["slug"] == "novel" {
			found = b
		}
	}
	if found == nil || num(t, found["chaptersTotal"]) < 2 {
		t.Fatalf("书架应出现新书且章节数 ≥2，实际 %v", found)
	}
}

func TestUploadRejectsUnsupportedExt(t *testing.T) {
	ts := makeServer(t, false)
	status, m := ts.upload("evil.exe", []byte{1})
	if status != 400 || errCode(t, m) != "VALIDATION_FAILED" {
		t.Fatalf("应 400 VALIDATION_FAILED，实际 %d %v", status, m)
	}
}

// ---- 分页边界 ----

func TestPaginationBounds(t *testing.T) {
	ts := makeServer(t, true)
	status, _ := ts.do("GET", "/api/v1/books?limit=9999", nil, "")
	if status != 200 {
		t.Fatalf("limit 超界应 200（cap 200），实际 %d", status)
	}
	_, m := ts.do("GET", "/api/v1/books?offset=99999", nil, "")
	items, _ := m["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("offset 超界应为空，实际 %v", items)
	}
}

// ---- e2e 1：全流程闭环 ----

func TestE2EFullLoop(t *testing.T) {
	ts := makeServerCfg(t, false, "true", "true")
	content, _ := os.ReadFile(filepath.Join(repoRoot, "testdata", "sample.txt"))
	status, m := ts.upload("novel.txt", content)
	if status != 201 {
		t.Fatalf("上传应 201，实际 %d", status)
	}
	sourcePath := str(t, m["sourcePath"])
	status, m = ts.do("POST", "/api/v1/books/novel/prepare", map[string]any{"sourcePath": sourcePath}, "")
	if status != 202 {
		t.Fatalf("prepare 应 202，实际 %d", status)
	}
	if job := ts.waitJob(str(t, m["job"].(map[string]any)["id"]), 120*time.Second); job["status"] != "succeeded" {
		t.Fatalf("prepare 失败：%v", job)
	}
	status, m = ts.do("POST", "/api/v1/books/novel/translate", map[string]any{}, "")
	if status != 202 {
		t.Fatalf("translate 应 202，实际 %d", status)
	}
	if job := ts.waitJob(str(t, m["job"].(map[string]any)["id"]), 120*time.Second); job["status"] != "succeeded" {
		t.Fatalf("translate 失败：%v", job)
	}
	// 全部 done
	_, m = ts.do("GET", "/api/v1/books/novel/status", nil, "")
	done := 0
	for _, c := range m["chapters"].([]any) {
		if cm, _ := c.(map[string]any); cm != nil && cm["status"] == "done" {
			done++
		}
	}
	if done != 2 {
		t.Fatalf("完成章节数应 2，实际 %d", done)
	}
	// 术语 resolve（200/404 皆合法）
	status, _ = ts.do("POST", "/api/v1/books/novel/glossary/conflicts/resolve",
		map[string]any{"source": "堀北", "target": "堀北"}, "")
	if status != 200 && status != 404 {
		t.Fatalf("resolve 应 200/404，实际 %d", status)
	}
	// review → qa → report
	for _, step := range []string{"review", "qa", "report"} {
		status, m = ts.do("POST", "/api/v1/books/novel/"+step, map[string]any{}, "")
		if status != 202 {
			t.Fatalf("%s 应 202，实际 %d", step, status)
		}
		if job := ts.waitJob(str(t, m["job"].(map[string]any)["id"]), 120*time.Second); job["status"] != "succeeded" {
			t.Fatalf("%s 失败：%v", step, job)
		}
	}
	_, m = ts.do("GET", "/api/v1/books/novel/reviews", nil, "")
	if n := len(m["items"].([]any)); n < 1 {
		t.Fatalf("应有审校记录，实际 %d", n)
	}
	// assemble
	status, m = ts.do("POST", "/api/v1/books/novel/assemble", map[string]any{"format": "epub"}, "")
	if status != 202 {
		t.Fatalf("assemble 应 202，实际 %d", status)
	}
	if job := ts.waitJob(str(t, m["job"].(map[string]any)["id"]), 120*time.Second); job["status"] != "succeeded" {
		t.Fatalf("assemble 失败：%v（log=%v）", job["status"], job["logTail"])
	}
	_, m = ts.do("GET", "/api/v1/books/novel/exports", nil, "")
	hasEPUB := false
	for _, e := range m["items"].([]any) {
		if em, _ := e.(map[string]any); em != nil && strings.HasSuffix(str(t, em["name"]), ".epub") {
			hasEPUB = true
		}
	}
	if !hasEPUB {
		t.Fatalf("导出应含 .epub，实际 %v", m["items"])
	}
	// 事件覆盖全集
	_, m = ts.do("GET", "/api/v1/books/novel/events?cursor=0&limit=2000", nil, "")
	names := map[string]bool{}
	for _, it := range m["items"].([]any) {
		if em, _ := it.(map[string]any); em != nil {
			if n, ok := em["event"].(string); ok {
				names[n] = true
			}
		}
	}
	for _, want := range []string{"run_initialized", "batch_translated", "chapter_done", "report_saved", "assembled"} {
		if !names[want] {
			t.Errorf("缺事件 %s", want)
		}
	}
}

// ---- e2e 2：取消幂等 + 续跑 ----

func TestE2ECancelIdempotent(t *testing.T) {
	ts := makeServer(t, false)
	content, _ := os.ReadFile(filepath.Join(repoRoot, "testdata", "sample.txt"))
	status, m := ts.upload("novel.txt", content)
	if status != 201 {
		t.Fatalf("上传应 201，实际 %d %v", status, m)
	}
	status, m = ts.do("POST", "/api/v1/books/novel/prepare", map[string]any{"sourcePath": str(t, m["sourcePath"])}, "")
	if status != 202 {
		t.Fatalf("prepare 应 202，实际 %d %v", status, m)
	}
	ts.waitJob(str(t, m["job"].(map[string]any)["id"]), 120*time.Second)

	status, m = ts.do("POST", "/api/v1/books/novel/translate", map[string]any{}, "")
	if status != 202 {
		t.Fatalf("translate 应 202，实际 %d %v", status, m)
	}
	jobID := str(t, m["job"].(map[string]any)["id"])
	status, m = ts.do("POST", "/api/v1/jobs/"+jobID+"/cancel", nil, "")
	if status != 200 {
		t.Fatalf("取消应 200，实际 %d", status)
	}
	switch m["job"].(map[string]any)["status"] {
	case "cancelled", "succeeded", "failed":
	default:
		t.Fatalf("取消后状态非法：%v", m)
	}
	// 再次取消幂等
	status, _ = ts.do("POST", "/api/v1/jobs/"+jobID+"/cancel", nil, "")
	if status != 200 {
		t.Fatalf("重复取消应 200，实际 %d", status)
	}
	// 取消后可续跑
	time.Sleep(time.Second)
	status, _ = ts.do("POST", "/api/v1/books/novel/translate", map[string]any{}, "")
	if status != 202 && status != 409 {
		t.Fatalf("续跑应 202/409，实际 %d", status)
	}
}

// ---- e2e 3：事件透传结构 ----

func TestE2EEventStructure(t *testing.T) {
	ts := makeServer(t, true)
	_, m := ts.do("GET", "/api/v1/books/novel/events?cursor=0&limit=100", nil, "")
	items, _ := m["items"].([]any)
	if len(items) == 0 {
		t.Fatal("应有事件")
	}
	for _, it := range items {
		em, _ := it.(map[string]any)
		if em == nil {
			continue
		}
		if em["ts"] == nil || em["event"] == nil {
			t.Fatalf("事件应有 ts/event 字段：%v", em)
		}
		if _, ok := em["line"].(float64); !ok {
			t.Fatalf("应有 line 注入：%v", em)
		}
	}
}

// ---- SSE 线格式冒烟 ----

func TestJobSSEWireFormat(t *testing.T) {
	ts := makeServer(t, true)
	status, m := ts.do("POST", "/api/v1/books/novel/qa", map[string]any{}, "")
	if status != 202 {
		t.Fatalf("qa 应 202，实际 %d", status)
	}
	jobID := str(t, m["job"].(map[string]any)["id"])
	req, _ := http.NewRequest("GET", ts.srv.URL+"/api/v1/jobs/"+jobID+"/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE 连接失败：%v", err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("content-type"); ct != "text/event-stream" {
		t.Fatalf("content-type 应 text/event-stream，实际 %s", ct)
	}
	buf := make([]byte, 4096)
	var acc string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		n, err := res.Body.Read(buf)
		if n > 0 {
			acc += string(buf[:n])
			if strings.Contains(acc, "event: job.status") && strings.Contains(acc, "data: ") {
				break
			}
		}
		if err != nil {
			break
		}
	}
	if !strings.Contains(acc, "event: job.status") {
		t.Fatalf("SSE 应先发 job.status，实际收到：%q", acc)
	}
}

// ---- 并发安全冒烟：同一 Server 并发读 books ----

func TestConcurrentBooksReads(t *testing.T) {
	ts := makeServer(t, true)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _ := ts.do("GET", "/api/v1/books", nil, "")
			if status != 200 {
				t.Errorf("并发 books 应 200，实际 %d", status)
			}
		}()
	}
	wg.Wait()
}
