// Package webserver 内嵌 WebUI 后端（分册 10 契约的 Go 实现，替代 Node serve.js）。
// 本文件：Job 注册表 + 引擎子进程管理（分册 10 §1.7/§1.8/§5 spawn 映射）。
package webserver

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const logTailLimit = 8 * 1024

// Job 内存态任务模型（JSON 字段与分册 10 §1.7 对齐；finished 保留至进程退出）。
type Job struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Book       string          `json:"book"`
	Status     string          `json:"status"` // running/succeeded/failed/cancelled
	Progress   json.RawMessage `json:"progress"`
	ExitCode   *int            `json:"exitCode"`
	Result     any             `json:"result"`
	CreatedAt  string          `json:"createdAt"`
	FinishedAt *string         `json:"finishedAt"`
	LogTail    string          `json:"logTail,omitempty"`

	progressView    map[string]any // SSE job.progress 用（Progress 原样透传）
	cancelRequested bool
	proc            *exec.Cmd
}

// SSEEvent 注册表广播事件（§2.1）。
type SSEEvent struct {
	JobID string
	Type  string // job.status / job.progress / job.log
	Data  map[string]any
}

type registrySub struct {
	id  int64
	ch  chan SSEEvent
	job string // 空 = 全部
}

// JobRegistry 并发安全的任务注册表 + 订阅广播。
type JobRegistry struct {
	mu        sync.Mutex
	jobs      map[string]*Job
	active    map[string]string // slug → jobID
	subs      map[int64]*registrySub
	nextSubID int64
}

func NewJobRegistry() *JobRegistry {
	return &JobRegistry{jobs: map[string]*Job{}, active: map[string]string{}, subs: map[int64]*registrySub{}}
}

func (r *JobRegistry) newJob(kind, book string) *Job {
	id := "job-" + randomID()
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	job := &Job{ID: id, Kind: kind, Book: book, Status: "running", CreatedAt: now}
	r.mu.Lock()
	r.jobs[id] = job
	r.active[book] = id
	r.mu.Unlock()
	r.broadcast(job, "job.status", map[string]any{"status": "running"})
	return job
}

func randomID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	s := base64.RawURLEncoding.EncodeToString(b)
	if s == "" {
		return hex.EncodeToString(b)
	}
	return strings.ToLower(s)
}

func (r *JobRegistry) get(id string) *Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j, ok := r.jobs[id]; ok {
		cp := *j
		return &cp
	}
	return nil
}

// list 返回按创建时间倒序的分页快照（logTail 不外发）。
func (r *JobRegistry) list(offset, limit int, book string) map[string]any {
	r.mu.Lock()
	all := make([]*Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		if book == "" || j.Book == book {
			cp := *j
			cp.LogTail = ""
			all = append(all, &cp)
		}
	}
	r.mu.Unlock()
	for i := 0; i < len(all); i++ {
		for k := i + 1; k < len(all); k++ {
			if all[k].CreatedAt > all[i].CreatedAt {
				all[i], all[k] = all[k], all[i]
			}
		}
	}
	total := len(all)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return map[string]any{"items": all[offset:end], "total": total, "offset": offset, "limit": limit}
}

// activeJob 返回书级运行中任务（顺带清理陈旧索引）。
func (r *JobRegistry) activeJob(book string) *Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.active[book]
	if !ok {
		return nil
	}
	j, ok := r.jobs[id]
	if !ok || j.Status != "running" {
		delete(r.active, book)
		return nil
	}
	cp := *j
	return &cp
}

func (r *JobRegistry) update(jobID string, patch func(*Job), sseType string, sseData map[string]any) {
	r.mu.Lock()
	j, ok := r.jobs[jobID]
	if !ok {
		r.mu.Unlock()
		return
	}
	patch(j)
	cp := *j
	if j.Status != "running" {
		delete(r.active, j.Book)
	}
	r.mu.Unlock()
	if sseType != "" {
		r.broadcast(&cp, sseType, sseData)
	}
}

func (r *JobRegistry) appendLog(jobID, text string) {
	r.mu.Lock()
	if j, ok := r.jobs[jobID]; ok {
		tail := j.LogTail + text
		if len(tail) > logTailLimit {
			tail = tail[len(tail)-logTailLimit:]
		}
		j.LogTail = tail
	}
	r.mu.Unlock()
}

// cancel 幂等取消：运行中 → kill 进程树并置 cancelled；已结束原样返回。
func (r *JobRegistry) cancel(jobID string) *Job {
	r.mu.Lock()
	j, ok := r.jobs[jobID]
	if !ok {
		r.mu.Unlock()
		return nil
	}
	if j.Status != "running" {
		cp := *j
		r.mu.Unlock()
		return &cp
	}
	cmd := j.proc
	j.cancelRequested = true
	r.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		killTree(cmd)
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	r.update(jobID, func(x *Job) { x.Status = "cancelled"; x.FinishedAt = &now }, "job.status", map[string]any{"status": "cancelled"})
	return r.get(jobID)
}

func (r *JobRegistry) attach(jobID string, cmd *exec.Cmd) {
	r.mu.Lock()
	if j, ok := r.jobs[jobID]; ok {
		j.proc = cmd
	}
	r.mu.Unlock()
}

// subscribe 注册订阅；返回事件通道与取消函数。jobFilter 为空订阅全部任务。
func (r *JobRegistry) subscribe(jobFilter string) (<-chan SSEEvent, func()) {
	r.mu.Lock()
	r.nextSubID++
	sub := &registrySub{id: r.nextSubID, ch: make(chan SSEEvent, 64), job: jobFilter}
	r.subs[sub.id] = sub
	r.mu.Unlock()
	cancel := func() {
		r.mu.Lock()
		delete(r.subs, sub.id)
		r.mu.Unlock()
	}
	return sub.ch, cancel
}

func (r *JobRegistry) broadcast(jobSnapshot *Job, typ string, data map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sub := range r.subs {
		if sub.job != "" && sub.job != jobSnapshot.ID {
			continue
		}
		select {
		case sub.ch <- SSEEvent{JobID: jobSnapshot.ID, Type: typ, Data: data}:
		default: // 慢消费者丢弃，SSE 端有全量兜底
		}
	}
}

// killTree Windows taskkill /T /F；POSIX 先 TERM 后兜底 KILL（对应 spawn.js killTree）。
func killTree(cmd *exec.Cmd) {
	pid := cmd.Process.Pid
	if isWindows() {
		_ = exec.Command("taskkill", "/PID", itoa(pid), "/T", "/F").Run()
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	go func() {
		time.Sleep(5 * time.Second)
		_ = cmd.Process.Kill()
	}()
}
// spawnEngine spawn 引擎并解析 --progress-line 进度行（§1.8）。
// env 为 nil 时继承当前环境。onExit 在进程退出后回调（exitCode 为 nil 表示 spawn 失败）。
func (r *JobRegistry) spawnEngine(enginePath string, argv []string, dir string, job *Job, env []string, onExit func(exitCode *int)) error {
	fullArgv := append([]string{"--progress-line"}, argv...)
	cmd := exec.Command(enginePath, fullArgv...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r.attach(job.ID, cmd)

	processLine := func(line string, isStderr bool) {
		trimmed := strings.TrimSpace(line)
		if !isStderr && strings.HasPrefix(trimmed, "{") {
			var parsed map[string]any
			if json.Unmarshal([]byte(trimmed), &parsed) == nil {
				if t, _ := parsed["type"].(string); t == "progress" {
					progress := map[string]any{
						"done":  numberOf(parsed["done"]),
						"total": numberOf(parsed["total"]),
						"label": strOrDefault(parsed["label"], ""),
					}
					raw, _ := json.Marshal(progress)
					r.update(job.ID, func(x *Job) {
						x.Progress = raw
						x.progressView = progress
					}, "job.progress", progress)
					return
				}
			}
		}
		r.appendLog(job.ID, line+"\n")
		r.broadcast(job, "job.log", map[string]any{"chunk": line + "\n"})
	}

	scan := func(rc interface{ Read([]byte) (int, error) }, isStderr bool, done chan<- struct{}) {
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			processLine(sc.Text(), isStderr)
		}
		done <- struct{}{}
	}
	doneOut, doneErr := make(chan struct{}, 1), make(chan struct{}, 1)
	go scan(stdout, false, doneOut)
	go scan(stderr, true, doneErr)

	go func() {
		<-doneOut
		<-doneErr
		waitErr := cmd.Wait()
		exitCode := parseExitCode(waitErr)
		r.mu.Lock()
		j := r.jobs[job.ID]
		cancelled := j != nil && j.cancelRequested
		r.mu.Unlock()
		now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		status := "failed"
		if exitCode != nil && *exitCode == 0 {
			status = "succeeded"
		} else if cancelled {
			status = "cancelled"
		}
		sseData := map[string]any{"status": status, "exitCode": nil}
		if exitCode != nil {
			sseData["exitCode"] = *exitCode
		}
		r.update(job.ID, func(x *Job) { x.ExitCode = exitCode; x.Status = status; x.FinishedAt = &now }, "job.status", sseData)
		if onExit != nil {
			onExit(exitCode)
		}
	}()
	return nil
}

// ---- §5 spawn 参数映射 ----

type argvError struct {
	status int
	code   string
	msg    string
}

func (e *argvError) Error() string { return e.msg }

func buildTranslateArgv(body map[string]any) ([]string, error) {
	argv := []string{}
	hasSwitch := body["polish"] != nil || body["review"] != nil || body["qa"] != nil ||
		body["mono"] != nil || body["bilingual"] != nil
	if body["chapter"] != nil {
		if hasSwitch {
			return nil, &argvError{status: 400, code: "CHAPTER_EXCLUSIVE", msg: "--chapter 只翻译并保存指定章节，不能同时使用收尾选项"}
		}
		ch, ok := numberOfE(body["chapter"])
		if !ok {
			return nil, &argvError{status: 400, code: "VALIDATION_FAILED", msg: "chapter 必须是数字"}
		}
		argv = append(argv, "--chapter", itoa(int(ch)))
		return argv, nil
	}
	var err error
	if argv, err = appendBoolSwitch(argv, body, "polish"); err != nil {
		return nil, err
	}
	if argv, err = appendBoolSwitch(argv, body, "review"); err != nil {
		return nil, err
	}
	if argv, err = appendBoolSwitch(argv, body, "qa"); err != nil {
		return nil, err
	}
	if argv, err = appendBoolSwitch(argv, body, "mono"); err != nil {
		return nil, err
	}
	if argv, err = appendBoolSwitch(argv, body, "bilingual"); err != nil {
		return nil, err
	}
	return argv, nil
}

func appendBoolSwitch(argv []string, body map[string]any, key string) ([]string, error) {
	v, ok := body[key]
	if !ok || v == nil {
		return argv, nil
	}
	b, isBool := v.(bool)
	if !isBool {
		return nil, &argvError{status: 400, code: "VALIDATION_FAILED", msg: key + " 必须是布尔值"}
	}
	if b {
		return append(argv, "--"+key), nil
	}
	return append(argv, "--no-"+key), nil
}

func buildAssembleArgv(body map[string]any) ([]string, error) {
	argv := []string{}
	if v := body["format"]; v != nil {
		s, _ := v.(string)
		switch s {
		case "epub", "txt", "html", "markdown", "pdf":
		default:
			return nil, &argvError{status: 400, code: "VALIDATION_FAILED", msg: "不支持的输出格式：" + s}
		}
		argv = append(argv, "--format", s)
	}
	if v := body["out"]; v != nil {
		s, _ := v.(string)
		argv = append(argv, "--out", s)
	}
	if v := body["pdfEngine"]; v != nil {
		s, _ := v.(string)
		switch s {
		case "weasyprint", "fpdf2", "questpdf":
		default:
			return nil, &argvError{status: 400, code: "VALIDATION_FAILED", msg: "不支持的 PDF 引擎：" + s}
		}
		argv = append(argv, "--pdf-engine", s)
	}
	var err error
	if argv, err = appendBoolSwitch(argv, body, "mono"); err != nil {
		return nil, err
	}
	if argv, err = appendBoolSwitch(argv, body, "bilingual"); err != nil {
		return nil, err
	}
	return argv, nil
}

func numberOfE(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	}
	return 0, false
}

func numberOf(v any) float64 {
	f, _ := numberOfE(v)
	return f
}

func strOrDefault(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func parseExitCode(waitErr error) *int {
	if waitErr == nil {
		zero := 0
		return &zero
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		code := ee.ExitCode()
		return &code
	}
	return nil
}
