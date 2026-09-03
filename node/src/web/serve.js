// web/serve.js —— WebUI 产品主界面后端（分册 10 全端点）。
import path from "node:path";
import fs, { promises as fsp } from "node:fs";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import Fastify from "fastify";
import multipart from "@fastify/multipart";
import Database from "better-sqlite3";
import { createRequire } from "node:module";
import { JobRegistry, spawnEngine, buildTranslateArgv, buildAssembleArgv } from "./spawn.js";
import { slugifyNode } from "./slugify.js";

const require = createRequire(import.meta.url);
const yaml = require("yaml");

const SLUG_RE = /^[A-Za-z0-9_\u4e00-\u9fff\u3040-\u30ff\uac00-\ud7af-]{1,64}$/;
const FILENAME_RE = /^[^\x00-\x1f/\\:*?"<>|]{1,200}$/;

// glossary.aliases 为 JSON 数组字符串；NULL/空/坏值容错为 []。
function safeParseList(v) {
  if (v == null || v === "") return [];
  try {
    const parsed = JSON.parse(v);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function errReply(reply, statusCode, code, message, extra = {}) {
  return reply.code(statusCode).send({
    error: { code, message, exitCode: null, engineStderr: null, ...extra },
  });
}

/** 查找 wenyi 引擎可执行文件。 */
export function findEnginePath() {
  const exe = process.env.WENYI_ENGINE;
  if (exe && fs.existsSync(exe)) return exe;
  const root = path.resolve(import.meta.dirname, "../.."); // = node/ 组件根
  const candidates = [
    path.join(root, "..", "dist", "wenyi.exe"),
    path.join(root, "..", "dist", "wenyi"),
    path.join(root, "..", "wenyi.exe"),
    path.join(root, "..", "dist", "wenyi-multi", "wenyi.exe"),
  ];
  for (const c of candidates) if (fs.existsSync(c)) return c;
  return null;
}

export async function buildServer({ stateDir, configPath, enginePath, uiDir = null, engineEnv = null }) {
  const app = Fastify({ logger: false, bodyLimit: 210 * 1024 * 1024 });
  await app.register(multipart, { limits: { fileSize: 200 * 1024 * 1024 } });
  const registry = new JobRegistry();
  const startTime = Date.now();
  const uploadsBySlug = new Map(); // slug → sourcePath（§4.11 新书豁免）

  // Host 校验（§1.3 第二道闸）
  app.addHook("onRequest", async (req, reply) => {
    const host = (req.headers.host ?? "").toLowerCase();
    const ok = /^127\.0\.0\.1(:\d+)?$/.test(host) || /^localhost(:\d+)?$/.test(host) || host === "";
    if (!ok) return errReply(reply, 403, "FORBIDDEN_HOST", "仅允许本地访问");
  });

  const bookDir = (slug) => path.join(stateDir, slug);
  const manifestPath = (slug) => path.join(bookDir(slug), "manifest.json");
  const readJSON = async (p) => JSON.parse(await fsp.readFile(p, "utf-8"));
  async function requireBook(req, reply) {
    const { slug } = req.params;
    if (!SLUG_RE.test(slug)) {
      errReply(reply, 400, "INVALID_SLUG", "非法书籍标识");
      return null;
    }
    if (!fs.existsSync(manifestPath(slug))) {
      errReply(reply, 404, "BOOK_NOT_FOUND", "书籍不存在");
      return null;
    }
    return slug;
  }

  // ---- 系统（§4.1）----
  app.get("/api/v1/health", async () => ({ ok: true, uptimeSec: Math.floor((Date.now() - startTime) / 1000) }));

  app.get("/api/v1/meta", async () => {
    let engineVersion = null;
    let engineAvailable = false;
    if (enginePath) {
      engineVersion = await new Promise((resolve) => {
        const p = spawn(enginePath, ["--version"], { stdio: ["ignore", "pipe", "pipe"] });
        let out = "";
        p.stdout.on("data", (c) => (out += c));
        p.on("exit", () => resolve(out.trim() || null));
        p.on("error", () => resolve(null));
      });
      engineAvailable = engineVersion != null;
    }
    return {
      backendVersion: "0.4.1",
      enginePath: enginePath ?? null,
      engineVersion,
      engineAvailable,
      nodeRuntime: process.version,
      stateDir: path.resolve(stateDir),
      configPath: path.resolve(configPath),
      pdfAvailable: false,
      capabilities: { translate: engineAvailable, review: engineAvailable, assemble: true, pdf: false },
    };
  });

  // ---- 书架（§4.2）----
  app.get("/api/v1/books", async (req) => {
    const q = (req.query.q ?? "").toLowerCase();
    const offset = Number(req.query.offset ?? 0);
    const limit = Math.min(Number(req.query.limit ?? 50), 200);
    const items = [];
    let skipped = 0;
    const entries = await fsp.readdir(stateDir, { withFileTypes: true }).catch(() => []);
    for (const entry of entries) {
      if (!entry.isDirectory() || entry.name === "uploads") continue;
      const mp = path.join(stateDir, entry.name, "manifest.json");
      let manifest;
      try {
        manifest = await readJSON(mp);
      } catch {
        skipped++;
        items.push({ slug: entry.name, title: entry.name, fmt: null, sourceLang: null, targetLang: null,
          chaptersTotal: 0, chaptersDone: 0, status: "idle", updatedAt: null });
        continue;
      }
      const chapters = manifest.chapters ?? [];
      const done = chapters.filter((c) => c.status === "done").length;
      const st = await fsp.stat(mp);
      const active = registry.activeJob(entry.name);
      const status = active ? "translating"
        : chapters.length > 0 && done === chapters.length ? "done"
        : chapters.length > 0 ? "prepared" : "idle";
      items.push({
        slug: entry.name,
        title: manifest.title ?? entry.name,
        fmt: manifest.fmt ?? null,
        sourceLang: manifest.source_lang ?? null,
        targetLang: manifest.target_lang ?? null,
        chaptersTotal: chapters.length,
        chaptersDone: done,
        status,
        updatedAt: new Date(st.mtimeMs).toISOString(),
      });
    }
    const filtered = q ? items.filter((b) => (b.title ?? "").toLowerCase().includes(q) || b.slug.includes(q)) : items;
    return { items: filtered.slice(offset, offset + limit), total: filtered.length, offset, limit, details: { skipped } };
  });

  app.get("/api/v1/books/:slug", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const raw = await fsp.readFile(manifestPath(req.params.slug), "utf-8");
    const st = await fsp.stat(manifestPath(req.params.slug));
    reply.header("content-type", "application/json; charset=utf-8");
    return reply.send(`{"slug":${JSON.stringify(req.params.slug)},"manifest":${raw},"updatedAt":${JSON.stringify(new Date(st.mtimeMs).toISOString())}}`);
  });

  app.get("/api/v1/books/:slug/status", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const manifest = await readJSON(manifestPath(req.params.slug));
    let glossary = { terms: 0, open_conflicts: 0 };
    const dbPath = path.join(bookDir(req.params.slug), "glossary.db");
    if (fs.existsSync(dbPath)) {
      const db = new Database(dbPath, { readonly: true, fileMustExist: true });
      db.pragma("busy_timeout = 5000");
      glossary.terms = db.prepare("SELECT COUNT(*) n FROM glossary").get().n;
      glossary.open_conflicts = db.prepare("SELECT COUNT(*) n FROM term_conflicts WHERE resolved=0").get().n;
      db.close();
    }
    return {
      chapters: (manifest.chapters ?? []).map((c) => ({ index: c.index, title: c.title, status: c.status })),
      glossary,
    };
  });

  // ---- 章节（§4.3）----
  app.get("/api/v1/books/:slug/chapters", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const offset = Number(req.query.offset ?? 0);
    const limit = Math.min(Number(req.query.limit ?? 50), 200);
    const manifest = await readJSON(manifestPath(req.params.slug));
    const items = [];
    for (const c of manifest.chapters ?? []) {
      const chPath = path.join(bookDir(req.params.slug), "chapters", `ch${c.index}.json`);
      let extra = {};
      try {
        const ch = await readJSON(chPath);
        const st = await fsp.stat(chPath);
        extra = {
          titleTranslated: ch.title_translated ?? null,
          segmentCount: (ch.segments ?? []).length,
          updatedAt: new Date(st.mtimeMs).toISOString(),
        };
      } catch {}
      items.push({ index: c.index, title: c.title, status: c.status, ...extra });
    }
    return { items: items.slice(offset, offset + limit), total: items.length, offset, limit };
  });

  app.get("/api/v1/books/:slug/chapters/:n", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const n = Number(req.params.n);
    if (!Number.isInteger(n) || n < 0) return errReply(reply, 400, "VALIDATION_FAILED", "非法章节号");
    const chPath = path.join(bookDir(req.params.slug), "chapters", `ch${n}.json`);
    if (!fs.existsSync(chPath)) return errReply(reply, 404, "CHAPTER_NOT_FOUND", "章节不存在");
    const raw = await fsp.readFile(chPath, "utf-8");
    reply.header("content-type", "application/json; charset=utf-8");
    return reply.send(raw);
  });

  // ---- 任务端点（§4.4 + §5 映射总表）----
  async function startJob(req, reply, kind, argvBuilder) {
    const { slug } = req.params;
    if (!SLUG_RE.test(slug)) return errReply(reply, 400, "INVALID_SLUG", "非法书籍标识");
    const manifestExists = fs.existsSync(manifestPath(slug));
    let sourcePath;
    if (manifestExists) {
      const manifest = await readJSON(manifestPath(slug)).catch(() => null);
      sourcePath = manifest?.source_path ?? null;
    } else if (kind === "prepare") {
      // 新书豁免（§4.11）：sourcePath ← uploads 返回值
      sourcePath = req.body?.sourcePath ?? uploadsBySlug.get(slug) ?? null;
      if (!sourcePath || !fs.existsSync(sourcePath)) {
        return errReply(reply, 400, "VALIDATION_FAILED", "新书需要先上传文件（sourcePath 缺失或无效）");
      }
      await fsp.mkdir(bookDir(slug), { recursive: true });
    }
    if (!sourcePath) {
      return errReply(reply, 409, "MANIFEST_INCOMPLETE", "manifest 缺少 source_path");
    }
    const active = registry.activeJob(slug);
    if (active) {
      return errReply(reply, 409, "BUSY", `本书已有运行中的任务 ${active.id}（kind=${active.kind}）`, {
        details: { activeJobId: active.id },
      });
    }
    let extraArgv = [];
    if (argvBuilder) {
      try {
        extraArgv = argvBuilder(req.body ?? {});
      } catch (err) {
        return errReply(reply, err.statusCode ?? 400, err.code ?? "VALIDATION_FAILED", err.message);
      }
    }
    const job = registry.newJob(kind, slug);
    const argv = ["-c", configPath, "--state-dir", path.resolve(stateDir), kind, sourcePath, ...extraArgv];
    spawnEngine({
      enginePath,
      argv,
      cwd: process.cwd(),
      job,
      registry,
      env: engineEnv ?? process.env,
      onExit: async (code) => {
        if (code === 0 && (kind === "prepare" || kind === "translate")) {
          try {
            const manifest = await readJSON(manifestPath(slug));
            const done = (manifest.chapters ?? []).filter((c) => c.status === "done").length;
            registry.update(job.id, {
              result: kind === "prepare"
                ? { chapters: (manifest.chapters ?? []).length }
                : { chaptersDone: done, chaptersTotal: (manifest.chapters ?? []).length },
            });
          } catch {}
        }
      },
    });
    reply.code(202);
    return { job: { ...job, logTail: undefined } };
  }

  app.post("/api/v1/books/:slug/prepare", (req, reply) => startJob(req, reply, "prepare"));
  app.post("/api/v1/books/:slug/translate", (req, reply) => startJob(req, reply, "translate", buildTranslateArgv));
  app.post("/api/v1/books/:slug/review", (req, reply) => startJob(req, reply, "review"));
  app.post("/api/v1/books/:slug/qa", (req, reply) => startJob(req, reply, "qa"));
  app.post("/api/v1/books/:slug/report", (req, reply) => startJob(req, reply, "report"));
  app.post("/api/v1/books/:slug/assemble", (req, reply) => startJob(req, reply, "assemble", buildAssembleArgv));

  app.get("/api/v1/jobs", async (req) => {
    return registry.list({
      offset: Number(req.query.offset ?? 0),
      limit: Math.min(Number(req.query.limit ?? 50), 200),
      book: req.query.book ?? null,
    });
  });

  app.get("/api/v1/jobs/:id", async (req, reply) => {
    const job = registry.get(req.params.id);
    if (!job) return errReply(reply, 404, "JOB_NOT_FOUND", "任务不存在");
    if (req.query.includeLog !== "1") return { job: { ...job, logTail: undefined } };
    return { job };
  });

  app.post("/api/v1/jobs/:id/cancel", async (req, reply) => {
    const job = registry.cancel(req.params.id);
    if (!job) return errReply(reply, 404, "JOB_NOT_FOUND", "任务不存在");
    return { job: { ...job, logTail: undefined } };
  });

  // Job SSE 流（§2.1）
  app.get("/api/v1/jobs/:id/events", async (req, reply) => {
    const job = registry.get(req.params.id);
    if (!job) return errReply(reply, 404, "JOB_NOT_FOUND", "任务不存在");
    reply.raw.writeHead(200, {
      "content-type": "text/event-stream",
      "cache-control": "no-cache",
      connection: "keep-alive",
    });
    let seq = 0;
    const send = (event, data) => {
      seq++;
      reply.raw.write(`id: ${seq}\nevent: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
    };
    send("job.status", { status: job.status });
    if (job.progress) send("job.progress", job.progress);
    const onUpdate = (updatedJob, sseEvent) => {
      if (updatedJob.id === job.id) send(sseEvent.type, sseEvent.data);
    };
    const onLog = (loggedJob, ev) => {
      if (loggedJob.id === job.id) send("job.log", ev);
    };
    registry.on("update", onUpdate);
    registry.on("log", onLog);
    const heartbeat = setInterval(() => send("heartbeat", { t: new Date().toISOString() }), 15000);
    req.raw.on("close", () => {
      clearInterval(heartbeat);
      registry.off("update", onUpdate);
      registry.off("log", onLog);
    });
  });

  // ---- 术语（§4.5 直连 glossary.db，只读）----
  app.get("/api/v1/books/:slug/glossary", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const q = (req.query.q ?? "").toLowerCase();
    const type = req.query.type ?? null;
    const status = req.query.status ?? null;
    const offset = Number(req.query.offset ?? 0);
    const limit = Math.min(Number(req.query.limit ?? 50), 200);
    const db = new Database(path.join(bookDir(req.params.slug), "glossary.db"), { readonly: true, fileMustExist: true });
    db.pragma("busy_timeout = 5000");
    const rows = db.prepare("SELECT * FROM glossary ORDER BY type, source").all();
    const conflicts = db.prepare("SELECT COUNT(*) n FROM term_conflicts WHERE resolved=0").get().n;
    db.close();
    let filtered = rows.map((r) => ({ ...r, aliases: safeParseList(r.aliases) }));
    if (q) filtered = filtered.filter((r) => r.source.toLowerCase().includes(q) || r.target.toLowerCase().includes(q));
    if (type) filtered = filtered.filter((r) => r.type === type);
    if (status) filtered = filtered.filter((r) => r.status === status);
    return {
      items: filtered.slice(offset, offset + limit),
      total: filtered.length,
      offset,
      limit,
      stats: { terms: rows.length, open_conflicts: conflicts },
    };
  });

  app.get("/api/v1/books/:slug/glossary/conflicts", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const offset = Number(req.query.offset ?? 0);
    const limit = Math.min(Number(req.query.limit ?? 50), 200);
    const db = new Database(path.join(bookDir(req.params.slug), "glossary.db"), { readonly: true, fileMustExist: true });
    db.pragma("busy_timeout = 5000");
    const rows = db.prepare("SELECT * FROM term_conflicts WHERE resolved=0 ORDER BY created_at").all();
    db.close();
    return { items: rows.slice(offset, offset + limit), total: rows.length, offset, limit };
  });

  app.post("/api/v1/books/:slug/glossary/conflicts/resolve", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const { source, target } = req.body ?? {};
    if (!source || !target) return errReply(reply, 400, "VALIDATION_FAILED", "source/target 不能为空");
    const manifest = await readJSON(manifestPath(req.params.slug));
    const sourcePath = manifest.source_path;
    if (!sourcePath) return errReply(reply, 409, "MANIFEST_INCOMPLETE", "manifest 缺少 source_path");
    let stderrTail = "";
    const code = await new Promise((resolve) => {
      const p = spawn(enginePath, ["-c", configPath, "--state-dir", path.resolve(stateDir), "glossary", "resolve", sourcePath, source, target], { stdio: ["ignore", "ignore", "pipe"], env: engineEnv ?? process.env });
      p.stderr?.on("data", (c) => (stderrTail = (stderrTail + c).slice(-4096)));
      p.on("exit", resolve);
      p.on("error", () => resolve(-1));
    });
    if (code === 0) return { resolved: true };
    return errReply(reply, 404, "TERM_NOT_FOUND", "术语不存在或裁决失败", { exitCode: code, engineStderr: stderrTail });
  });

  // ---- 审校（§4.6）----
  app.get("/api/v1/books/:slug/reviews", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const offset = Number(req.query.offset ?? 0);
    const limit = Math.min(Number(req.query.limit ?? 50), 200);
    const reviewsDir = path.join(bookDir(req.params.slug), "reviews");
    const items = [];
    const entries = await fsp.readdir(reviewsDir, { withFileTypes: true }).catch(() => []);
    for (const entry of entries) {
      if (!entry.isDirectory() || !entry.name.startsWith("review-")) continue;
      const rp = path.join(reviewsDir, entry.name, "result.json");
      try {
        const result = await readJSON(rp);
        items.push({
          id: entry.name,
          status: result.status ?? "unknown",
          termination: result.termination ?? null,
          issueCount: (result.issues ?? []).length,
          changeCount: (result.changes ?? []).length,
          startedAt: result.started_at ?? null,
          finishedAt: result.finished_at ?? null,
        });
      } catch {}
    }
    items.sort((a, b) => (b.startedAt ?? "").localeCompare(a.startedAt ?? ""));
    return { items: items.slice(offset, offset + limit), total: items.length, offset, limit };
  });

  app.get("/api/v1/books/:slug/reviews/:reviewId", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const { reviewId } = req.params;
    if (!/^review-[A-Za-z0-9._-]+$/.test(reviewId)) return errReply(reply, 400, "INVALID_PATH", "非法审校 ID");
    const rp = path.join(bookDir(req.params.slug), "reviews", reviewId, "result.json");
    if (!fs.existsSync(rp)) return errReply(reply, 404, "REVIEW_NOT_FOUND", "审校结果不存在");
    const raw = await fsp.readFile(rp, "utf-8");
    reply.header("content-type", "application/json; charset=utf-8");
    return reply.send(raw);
  });

  // ---- QA 与用量（§4.7）----
  app.get("/api/v1/books/:slug/report", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const rp = path.join(bookDir(req.params.slug), "report.json");
    if (!fs.existsSync(rp)) return errReply(reply, 404, "REPORT_NOT_FOUND", "报告不存在");
    reply.header("content-type", "application/json; charset=utf-8");
    return reply.send(await fsp.readFile(rp, "utf-8"));
  });

  app.get("/api/v1/books/:slug/usage", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const up = path.join(bookDir(req.params.slug), "usage.json");
    if (!fs.existsSync(up)) return errReply(reply, 404, "USAGE_NOT_FOUND", "用量数据不存在");
    reply.header("content-type", "application/json; charset=utf-8");
    return reply.send(await fsp.readFile(up, "utf-8"));
  });

  // ---- 导出（§4.8）----
  app.get("/api/v1/books/:slug/exports", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const manifest = await readJSON(manifestPath(req.params.slug));
    const sourcePath = manifest.source_path ?? "";
    const outDir = sourcePath ? path.join(path.dirname(sourcePath), "output") : null;
    const items = [];
    if (outDir && fs.existsSync(outDir)) {
      const entries = await fsp.readdir(outDir, { withFileTypes: true }).catch(() => []);
      for (const entry of entries) {
        if (!entry.isFile()) continue;
        const full = path.join(outDir, entry.name);
        const st = await fsp.stat(full);
        const ext = path.extname(entry.name).slice(1).toLowerCase();
        items.push({
          name: entry.name,
          size: st.size,
          mtime: new Date(st.mtimeMs).toISOString(),
          format: ext === "md" ? "markdown" : ext,
          bilingual: entry.name.includes("-bi") || entry.name.includes(".zh-bi"),
        });
      }
    }
    items.sort((a, b) => b.mtime.localeCompare(a.mtime));
    return { items, total: items.length, offset: 0, limit: 50 };
  });

  app.get("/api/v1/books/:slug/exports/:name", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const { name } = req.params;
    if (!FILENAME_RE.test(name)) return errReply(reply, 400, "INVALID_PATH", "非法文件名");
    const manifest = await readJSON(manifestPath(req.params.slug));
    const sourcePath = manifest.source_path ?? "";
    const outDir = sourcePath ? path.join(path.dirname(sourcePath), "output") : null;
    const full = outDir ? path.join(outDir, name) : null;
    if (!full || !fs.existsSync(full)) return errReply(reply, 404, "EXPORT_NOT_FOUND", "导出文件不存在");
    reply.header("content-disposition", `attachment; filename*=UTF-8''${encodeURIComponent(name)}`);
    return reply.send(fs.createReadStream(full));
  });

  // ---- 事件（§4.9 + SSE §2.4 游标协议）----
  async function readEvents(slug, cursor, limit) {
    const ep = path.join(bookDir(slug), "events.jsonl");
    if (!fs.existsSync(ep)) return { items: [], nextCursor: 0, total: 0 };
    const raw = await fsp.readFile(ep, "utf-8");
    const lines = raw.split("\n").filter((l) => l.trim() !== "");
    if (cursor > lines.length) {
      const err = new Error("游标超界");
      err.statusCode = 400;
      err.code = "INVALID_CURSOR";
      throw err;
    }
    const slice = lines.slice(cursor, cursor + limit);
    const items = slice.map((line, i) => {
      try {
        return { line: cursor + i, ...JSON.parse(line) };
      } catch {
        return { line: cursor + i, raw: line };
      }
    });
    return { items, nextCursor: Math.min(cursor + limit, lines.length), total: lines.length };
  }

  app.get("/api/v1/books/:slug/events", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    try {
      return await readEvents(req.params.slug, Number(req.query.cursor ?? 0), Math.min(Number(req.query.limit ?? 500), 2000));
    } catch (err) {
      return errReply(reply, err.statusCode ?? 500, err.code ?? "INTERNAL", err.message);
    }
  });

  app.get("/api/v1/books/:slug/events/stream", async (req, reply) => {
    if (!(await requireBook(req, reply))) return;
    const ep = path.join(bookDir(req.params.slug), "events.jsonl");
    const lastEventID = req.headers["last-event-id"] ?? "";
    const m = String(lastEventID).match(/line=(\d+)/);
    let cursor = m ? Number(m[1]) : 0;
    reply.raw.writeHead(200, {
      "content-type": "text/event-stream",
      "cache-control": "no-cache",
      connection: "keep-alive",
    });
    let seq = cursor;
    let lastLineCount = -1;
    const pushLines = async () => {
      if (!fs.existsSync(ep)) return;
      const raw = await fsp.readFile(ep, "utf-8");
      const lines = raw.split("\n").filter((l) => l.trim() !== "");
      if (lastLineCount > lines.length) {
        reply.raw.write(`id: ${++seq}\nevent: reset\ndata: ${JSON.stringify({ reason: "truncated", line: 0 })}\n\n`);
        cursor = 0;
      }
      lastLineCount = lines.length;
      while (cursor < lines.length) {
        reply.raw.write(`id: line=${cursor}\nevent: engine.event\ndata: ${lines[cursor]}\n\n`);
        cursor++;
      }
    };
    await pushLines();
    const watcher = setInterval(pushLines, 1000);
    const heartbeat = setInterval(() => {
      reply.raw.write(`id: ${++seq}\nevent: heartbeat\ndata: ${JSON.stringify({ t: new Date().toISOString() })}\n\n`);
    }, 15000);
    req.raw.on("close", () => {
      clearInterval(watcher);
      clearInterval(heartbeat);
    });
  });

  // ---- 配置（§4.10）----
  app.get("/api/v1/config", async () => {
    const exists = fs.existsSync(configPath);
    const raw = exists ? await fsp.readFile(configPath, "utf-8") : "";
    let parsed = null;
    const errors = [];
    if (exists && raw.trim() !== "") {
      try {
        parsed = yaml.parse(raw);
      } catch (e) {
        errors.push({ line: e.line ?? null, message: e.message });
      }
    }
    return { path: path.resolve(configPath), exists, raw, parsed, errors };
  });

  app.put("/api/v1/config", async (req, reply) => {
    const { content } = req.body ?? {};
    if (typeof content !== "string") return errReply(reply, 400, "VALIDATION_FAILED", "content 必须为字符串");
    try {
      yaml.parse(content);
    } catch (e) {
      return errReply(reply, 422, "CONFIG_PARSE_ERROR", "YAML 语法错误", {
        details: { errors: [{ line: e.line ?? null, message: e.message }] },
      });
    }
    const tmp = configPath + ".tmp";
    await fsp.writeFile(tmp, content, "utf-8");
    await fsp.rename(tmp, configPath);
    return { path: path.resolve(configPath), errors: [] };
  });

  app.delete("/api/v1/config", async (_req, reply) => {
    if (fs.existsSync(configPath)) await fsp.unlink(configPath);
    return { ok: true };
  });

  // ---- 上传（§4.11）----
  app.post("/api/v1/uploads", async (req, reply) => {
    const parts = req.parts();
    let filePart = null;
    for await (const part of parts) {
      if (part.type === "file" && part.fieldname === "file") {
        filePart = part;
        break;
      }
    }
    if (!filePart) return errReply(reply, 400, "VALIDATION_FAILED", "缺少 file 字段");
    const originalName = path.basename(filePart.filename ?? "");
    if (!FILENAME_RE.test(originalName)) return errReply(reply, 400, "INVALID_PATH", "非法文件名");
    const ext = path.extname(originalName).slice(1).toLowerCase();
    if (!["epub", "txt", "md", "html", "fb2", "pdf"].includes(ext)) {
      return errReply(reply, 400, "VALIDATION_FAILED", `不支持的扩展名：${ext}`);
    }
    const uploadsDir = path.join(stateDir, "uploads");
    await fsp.mkdir(uploadsDir, { recursive: true });
    const safeName = originalName.replace(/[^\w\u4e00-\u9fff\u3040-\u30ff.-]+/g, "_");
    const target = path.join(uploadsDir, safeName);
    const buffer = await filePart.toBuffer();
    const stat = await fsp.stat(target).catch(() => null);
    if (stat && stat.size === buffer.length) {
      // 幂等：同名同大小复用
    } else {
      await fsp.writeFile(target, buffer);
    }
    const stem = path.basename(safeName, path.extname(safeName));
    const slug = slugifyNode(stem);
    uploadsBySlug.set(slug, path.resolve(target));
    reply.code(201);
    return { sourcePath: path.resolve(target), slug, size: buffer.length, ext };
  });

  // ---- 前端静态资源（Vite 构建产物）----
  if (uiDir && fs.existsSync(uiDir)) {
    const fastifyStatic = (await import("@fastify/static")).default;
    await app.register(fastifyStatic, { root: uiDir, prefix: "/" });
  }

  return { app, registry };
}

export async function cmdWebServe(argv) {
  let port = 8731;
  let configPath = "config.yaml";
  let stateDir = "state";
  let noOpen = false;
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--port" || a === "-p") port = Number(argv[++i]);
    else if (a.startsWith("--port=")) port = Number(a.slice(7));
    else if (a === "--config" || a === "-c") configPath = argv[++i];
    else if (a.startsWith("--config=")) configPath = a.slice(9);
    else if (a === "--state-dir") stateDir = argv[++i];
    else if (a.startsWith("--state-dir=")) stateDir = a.slice(12);
    else if (a === "--no-open") noOpen = true;
  }
  const enginePath = findEnginePath();
  if (!enginePath) {
    console.error("未找到 wenyi 引擎（设置 WENYI_ENGINE 或构建 dist/wenyi.exe）");
    return 1;
  }
  const uiDir = path.resolve(import.meta.dirname, "../../ui-dist");
  // 离线体验模式（provider=fake）需要路由开关才有可用的假翻译行为，按当前配置自动注入
  let engineEnv = null;
  try {
    const cfg = yaml.parse(await fsp.readFile(configPath, "utf8"));
    if (cfg?.llm?.provider === "fake") engineEnv = { ...process.env, WENYI_FAKE_ROUTING: "1" };
  } catch { /* 配置缺失或格式错误时按原样运行，由设置页/引擎报错 */ }
  const { app } = await buildServer({ stateDir, configPath, port, enginePath, uiDir, engineEnv });
  await app.listen({ host: "127.0.0.1", port });
  console.log(`Wenyi WebUI 已启动：http://127.0.0.1:${port}`);
  if (!noOpen && process.env.WENYI_NO_OPEN !== "1") {
    // 可选打开浏览器（open 为可选依赖）
    try {
      const open = (await import("open")).default;
      open(`http://127.0.0.1:${port}`).catch(() => {});
    } catch {}
  }
  return new Promise(() => {});
}
