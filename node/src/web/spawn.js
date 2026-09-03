// web/spawn.js 引擎子进程管理（分册 10 §1.7/§1.8/§5）：Job 模型 + spawn 映射。
import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import path from "node:path";
import { EventEmitter } from "node:events";

const LOG_TAIL_LIMIT = 8 * 1024;

/** Job 注册表（内存态；finished 保留 24h）。 */
export class JobRegistry extends EventEmitter {
  constructor() {
    super();
    this.jobs = new Map(); // id → job
    this.activeByBook = new Map(); // slug → jobId
    this.procs = new Map(); // jobId → child
  }

  newJob(kind, book) {
    const id = "job-" + randomBytes(4).toString("base64url").replace(/[^a-z0-9]/gi, "").padEnd(8, "0").slice(0, 8).toLowerCase() ||
      "job-" + Date.now().toString(36);
    const job = {
      id, kind, book,
      status: "running",
      progress: null,
      exitCode: null,
      result: null,
      createdAt: new Date().toISOString(),
      finishedAt: null,
      logTail: "",
    };
    this.jobs.set(id, job);
    this.activeByBook.set(book, id);
    this.emit("update", job, { type: "job.status", data: { status: "running" } });
    return job;
  }

  get(id) { return this.jobs.get(id) ?? null; }

  list({ offset = 0, limit = 50, book = null } = {}) {
    let all = [...this.jobs.values()];
    if (book) all = all.filter((j) => j.book === book);
    all.sort((a, b) => b.createdAt.localeCompare(a.createdAt));
    const total = all.length;
    const items = all.slice(offset, offset + limit).map((j) => ({ ...j, logTail: undefined }));
    return { items, total, offset, limit };
  }

  activeJob(book) {
    const id = this.activeByBook.get(book);
    if (!id) return null;
    const job = this.jobs.get(id);
    if (!job || job.status !== "running") {
      this.activeByBook.delete(book);
      return null;
    }
    return job;
  }

  attach(jobId, child) { this.procs.set(jobId, child); }

  update(jobId, patch, sseEvent = null) {
    const job = this.jobs.get(jobId);
    if (!job) return;
    Object.assign(job, patch);
    if (sseEvent) this.emit("update", job, sseEvent);
    if (job.status !== "running") {
      this.activeByBook.delete(job.book);
      this.procs.delete(jobId);
    }
  }

  appendLog(jobId, text) {
    const job = this.jobs.get(jobId);
    if (!job) return;
    job.logTail = (job.logTail + text).slice(-LOG_TAIL_LIMIT);
  }

  cancel(jobId) {
    const job = this.jobs.get(jobId);
    if (!job) return null;
    if (job.status !== "running") return job;
    const child = this.procs.get(jobId);
    if (child && child.exitCode == null) {
      killTree(child.pid);
    }
    this.update(jobId, { status: "cancelled", finishedAt: new Date().toISOString() },
      { type: "job.status", data: { status: "cancelled" } });
    return this.jobs.get(jobId);
  }

  /** 服务器重启语义：所有 running → failed(server_restarted)。 */
  markAllInterrupted() {
    for (const job of this.jobs.values()) {
      if (job.status === "running") {
        this.update(job.id, { status: "failed", exitCode: null, finishedAt: new Date().toISOString() },
          { type: "job.status", data: { status: "failed", reason: "server_restarted" } });
      }
    }
  }
}

function killTree(pid) {
  if (process.platform === "win32") {
    spawn("taskkill", ["/PID", String(pid), "/T", "/F"], { stdio: "ignore" });
  } else {
    try { process.kill(-pid, "SIGTERM"); } catch { try { process.kill(pid, "SIGTERM"); } catch {} }
    setTimeout(() => {
      try { process.kill(pid, "SIGKILL"); } catch {}
    }, 5000).unref();
  }
}

/** spawn 引擎并解析 --progress-line 进度行（分册 10 §1.8）。 */
export function spawnEngine({ enginePath, argv, cwd, job, registry, onExit, env = process.env }) {
  const fullArgv = ["--progress-line", ...argv];
  const child = spawn(enginePath, fullArgv, { cwd, stdio: ["ignore", "pipe", "pipe"], env });
  registry.attach(job.id, child);
  const handleStream = (stream, isStderr) => {
    let buffer = "";
    stream.setEncoding("utf-8");
    stream.on("data", (chunk) => {
      buffer += chunk;
      let idx;
      while ((idx = buffer.indexOf("\n")) >= 0) {
        const line = buffer.slice(0, idx);
        buffer = buffer.slice(idx + 1);
        processLine(line, isStderr);
      }
    });
  };
  const processLine = (line, isStderr) => {
    const trimmed = line.trim();
    if (!isStderr && trimmed.startsWith("{")) {
      try {
        const parsed = JSON.parse(trimmed);
        if (parsed && parsed.type === "progress") {
          const progress = { done: parsed.done ?? 0, total: parsed.total ?? 0, label: parsed.label ?? "" };
          registry.update(job.id, { progress }, { type: "job.progress", data: progress });
          return;
        }
      } catch {
        // 非 JSON 行按日志
      }
    }
    registry.appendLog(job.id, line + "\n");
    registry.emit("log", job, { chunk: line + "\n" });
  };
  handleStream(child.stdout, false);
  handleStream(child.stderr, true);
  child.on("exit", (code) => {
    const exitCode = code == null ? null : code;
    const patch = { exitCode };
    if (exitCode === 0) {
      patch.status = "succeeded";
      patch.finishedAt = new Date().toISOString();
    } else {
      patch.status = job.status === "cancelled" ? "cancelled" : "failed";
      patch.finishedAt = new Date().toISOString();
    }
    registry.update(job.id, patch, { type: "job.status", data: { status: patch.status, exitCode } });
    if (onExit) onExit(exitCode, registry.get(job.id));
  });
  child.on("error", (err) => {
    registry.appendLog(job.id, String(err));
    registry.update(job.id, { status: "failed", exitCode: null, finishedAt: new Date().toISOString() },
      { type: "job.status", data: { status: "failed", error: String(err) } });
    if (onExit) onExit(null, registry.get(job.id));
  });
  return child;
}

// ---- §5 spawn 参数映射总表 ----

export function buildTranslateArgv(body = {}) {
  const argv = [];
  const hasSwitch = body.polish != null || body.review != null || body.qa != null ||
    body.mono != null || body.bilingual != null;
  if (body.chapter != null) {
    if (hasSwitch) {
      const err = new Error("--chapter 只翻译并保存指定章节，不能同时使用收尾选项");
      err.statusCode = 400;
      err.code = "CHAPTER_EXCLUSIVE";
      throw err;
    }
    argv.push("--chapter", String(body.chapter));
  } else {
    if (body.polish != null) argv.push(body.polish ? "--polish" : "--no-polish");
    if (body.review != null) argv.push(body.review ? "--review" : "--no-review");
    if (body.qa != null) argv.push(body.qa ? "--qa" : "--no-qa");
    if (body.mono != null) argv.push(body.mono ? "--mono" : "--no-mono");
    if (body.bilingual != null) argv.push(body.bilingual ? "--bilingual" : "--no-bilingual");
  }
  return argv;
}

export function buildAssembleArgv(body = {}) {
  const argv = [];
  if (body.format != null) {
    if (!["epub", "txt", "html", "markdown", "pdf"].includes(body.format)) {
      const err = new Error(`不支持的输出格式：${body.format}`);
      err.statusCode = 400;
      err.code = "VALIDATION_FAILED";
      throw err;
    }
    argv.push("--format", body.format);
  }
  if (body.out != null) argv.push("--out", body.out);
  if (body.pdfEngine != null) {
    if (!["weasyprint", "fpdf2", "questpdf"].includes(body.pdfEngine)) {
      const err = new Error(`不支持的 PDF 引擎：${body.pdfEngine}`);
      err.statusCode = 400;
      err.code = "VALIDATION_FAILED";
      throw err;
    }
    argv.push("--pdf-engine", body.pdfEngine);
  }
  if (body.mono != null) argv.push(body.mono ? "--mono" : "--no-mono");
  if (body.bilingual != null) argv.push(body.bilingual ? "--bilingual" : "--no-bilingual");
  return argv;
}
