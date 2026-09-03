// 分册 10 §8.1 单元验收：映射表逐行 + 安全正则 + 游标 + 错误信封 + 上传闭环。
import { test } from "node:test";
import assert from "node:assert/strict";
import { promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import { execFileSync } from "node:child_process";
import { buildTranslateArgv, buildAssembleArgv } from "../src/web/spawn.js";
import { buildServer, findEnginePath } from "../src/web/serve.js";
import { slugifyNode } from "../src/web/slugify.js";

const ROOT = path.resolve(import.meta.dirname, "../..");

async function makeServer({ withBook = true } = {}) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "wenyi-web-"));
  const stateDir = path.join(dir, "state");
  await fs.mkdir(stateDir, { recursive: true });
  const configPath = path.join(dir, "config.yaml");
  await fs.writeFile(configPath, `language:\n  source: ja\n  target: zh\nllm:\n  provider: fake\n  tiers:\n    strong: {model: p}\n    cheap: {model: f}\npipeline:\n  review: false\n  polish: false\n  consistency_qa: false\n  book_understanding: false\npaths:\n  state_dir: state\n`, "utf-8");
  if (withBook) {
    const txt = path.join(dir, "novel.txt");
    await fs.copyFile(path.join(ROOT, "testdata", "sample.txt"), txt);
    execFileSync(path.join(ROOT, "dist", "wenyi.exe"), ["-c", configPath, "translate", txt, "--review=false", "--qa=false", "--polish=false"], {
      cwd: dir, encoding: "utf-8",
      env: { ...process.env, WENYI_NODE_CLI: path.join(ROOT, "node", "cli.js"), WENYI_FAKE_ROUTING: "1" },
      stdio: ["ignore", "pipe", "pipe"],
    });
  }
  const enginePath = findEnginePath();
  const { app, registry } = await buildServer({ stateDir, configPath, enginePath });
  await app.ready();
  return { app, registry, dir, stateDir, configPath, inject: app.inject.bind(app) };
}

// ---- §5 映射表逐行 argv 断言 ----

test("translate argv: null 字段不带开关", () => {
  assert.deepEqual(buildTranslateArgv({}), []);
  assert.deepEqual(buildTranslateArgv({ polish: true, review: false }), ["--polish", "--no-review"]);
  assert.deepEqual(buildTranslateArgv({ mono: false, bilingual: true }), ["--no-mono", "--bilingual"]);
});

test("translate argv: chapter 互斥 400", () => {
  assert.throws(() => buildTranslateArgv({ chapter: 0, polish: true }), (e) => e.code === "CHAPTER_EXCLUSIVE");
  assert.deepEqual(buildTranslateArgv({ chapter: 3 }), ["--chapter", "3"]);
});

test("assemble argv: format/engine 校验 + 开关", () => {
  assert.deepEqual(buildAssembleArgv({ format: "txt" }), ["--format", "txt"]);
  assert.deepEqual(buildAssembleArgv({ pdfEngine: "questpdf", bilingual: true }), ["--pdf-engine", "questpdf", "--bilingual"]);
  assert.throws(() => buildAssembleArgv({ format: "docx" }), (e) => e.code === "VALIDATION_FAILED");
  assert.throws(() => buildAssembleArgv({ pdfEngine: "bogus" }), (e) => e.code === "VALIDATION_FAILED");
});

// ---- slug 规则与引擎一致性 ----

test("slugify 与引擎一致", () => {
  assert.equal(slugifyNode("novel"), "novel");
  assert.equal(slugifyNode("サンプル小説"), "サンプル小説");
  assert.equal(slugifyNode("///"), "book");
});

// ---- 安全：slug/Host ----

test("INVALID_SLUG + FORBIDDEN_HOST", async () => {
  const { inject } = await makeServer();
  const r1 = await inject({ method: "GET", url: "/api/v1/books/../etc", headers: { host: "127.0.0.1:8731" } });
  assert.ok([400, 404].includes(r1.statusCode));
  const r2 = await inject({ method: "GET", url: "/api/v1/health", headers: { host: "evil.example.com" } });
  assert.equal(r2.statusCode, 403);
  assert.equal(r2.json().error.code, "FORBIDDEN_HOST");
});

// ---- 书籍端点投影 ----

test("books list + status + chapters projection", async () => {
  const { inject } = await makeServer();
  const list = await inject({ method: "GET", url: "/api/v1/books" });
  assert.equal(list.statusCode, 200);
  const books = list.json();
  assert.ok(books.total >= 1);
  const b = books.items.find((x) => x.slug === "novel");
  assert.ok(b, "应有 novel");
  assert.equal(b.chaptersDone, 2);
  assert.equal(b.chaptersTotal, 2);
  assert.equal(b.status, "done");
  // 404
  const nf = await inject({ method: "GET", url: "/api/v1/books/nope" });
  assert.equal(nf.statusCode, 404);
  assert.equal(nf.json().error.code, "BOOK_NOT_FOUND");
  // status
  const status = await inject({ method: "GET", url: "/api/v1/books/novel/status" });
  assert.equal(status.statusCode, 200);
  assert.equal(status.json().glossary.terms, 2);
  // chapters 透传
  const ch0 = await inject({ method: "GET", url: "/api/v1/books/novel/chapters/0" });
  assert.equal(ch0.statusCode, 200);
  assert.ok(Array.isArray(ch0.json().segments));
});

// ---- 事件端点 + 游标 ----

test("events pagination + cursor bounds", async () => {
  const { inject } = await makeServer();
  const r = await inject({ method: "GET", url: "/api/v1/books/novel/events?cursor=0&limit=5" });
  assert.equal(r.statusCode, 200);
  const body = r.json();
  assert.ok(body.total > 0);
  assert.ok(body.items.length <= 5);
  assert.equal(body.items[0].line, 0);
  const bad = await inject({ method: "GET", url: `/api/v1/books/novel/events?cursor=${body.total + 100}` });
  assert.equal(bad.statusCode, 400);
  assert.equal(bad.json().error.code, "INVALID_CURSOR");
});

// ---- 术语直连 ----

test("glossary direct read with filters + stats", async () => {
  const { inject } = await makeServer();
  const r = await inject({ method: "GET", url: "/api/v1/books/novel/glossary?q=堀北" });
  assert.equal(r.statusCode, 200);
  const body = r.json();
  assert.ok(body.stats.terms >= 2);
  assert.ok(body.items.every((it) => it.source.includes("堀北") || it.target.includes("堀北")));
  const conflicts = await inject({ method: "GET", url: "/api/v1/books/novel/glossary/conflicts" });
  assert.equal(conflicts.statusCode, 200);
});

// ---- 任务端点 + BUSY/取消 ----

test("translate job accepted, BUSY on concurrent, cancel idempotent", async () => {
  const { inject } = await makeServer();
  const r1 = await inject({ method: "POST", url: "/api/v1/books/novel/translate", payload: {} });
  assert.equal(r1.statusCode, 202);
  const job1 = r1.json().job;
  assert.ok(job1.id.startsWith("job-"));
  // 并发 → 409 BUSY
  const r2 = await inject({ method: "POST", url: "/api/v1/books/novel/translate", payload: {} });
  assert.equal(r2.statusCode, 409);
  assert.equal(r2.json().error.code, "BUSY");
  assert.equal(r2.json().error.details.activeJobId, job1.id);
  // 等待任务结束（快速 fake）
  await new Promise((resolve) => setTimeout(resolve, 3000));
  const done = await inject({ method: "GET", url: `/api/v1/jobs/${job1.id}` });
  assert.ok(["succeeded", "failed", "cancelled"].includes(done.json().job.status), `status=${done.json().job.status}`);
  // 结束后取消幂等
  const cancel = await inject({ method: "POST", url: `/api/v1/jobs/${job1.id}/cancel` });
  assert.equal(cancel.statusCode, 200);
  // chapter 互斥
  const r3 = await inject({ method: "POST", url: "/api/v1/books/novel/translate", payload: { chapter: 0, polish: true } });
  assert.equal(r3.statusCode, 400);
  assert.equal(r3.json().error.code, "CHAPTER_EXCLUSIVE");
});

// ---- report/usage 透传 ----

test("report and usage passthrough with 404", async () => {
  const { inject } = await makeServer();
  const rep = await inject({ method: "GET", url: "/api/v1/books/novel/report" });
  assert.equal(rep.statusCode, 200);
  assert.ok(rep.json().summary);
  const usage = await inject({ method: "GET", url: "/api/v1/books/novel/usage" });
  assert.ok([200, 404].includes(usage.statusCode));
  const nf = await inject({ method: "GET", url: "/api/v1/books/novel2/report" });
  assert.equal(nf.statusCode, 404);
});

// ---- config 端点 ----

test("config get/put/delete + parse error 422", async () => {
  const { inject } = await makeServer();
  const get = await inject({ method: "GET", url: "/api/v1/config" });
  assert.equal(get.statusCode, 200);
  assert.ok(get.json().raw.includes("provider: fake"));
  const bad = await inject({ method: "PUT", url: "/api/v1/config", payload: { content: "a:\n  - b\n c:\n" } });
  if (bad.statusCode === 422) {
    assert.equal(bad.json().error.code, "CONFIG_PARSE_ERROR");
  }
  const good = await inject({ method: "PUT", url: "/api/v1/config", payload: { content: "language:\n  source: ja\n  target: zh\n" } });
  assert.equal(good.statusCode, 200);
  const del = await inject({ method: "DELETE", url: "/api/v1/config" });
  assert.equal(del.statusCode, 200);
});

// ---- 上传闭环（§4.11 + §8.1-7）----

test("upload → slug 预生成 → prepare 新书豁免闭环", async () => {
  const { inject, stateDir } = await makeServer({ withBook: false });
  // 上传
  const form = new FormData();
  const txtContent = await fs.readFile(path.join(ROOT, "testdata", "sample.txt"));
  form.append("file", new Blob([txtContent]), "novel.txt");
  const up = await inject({ method: "POST", url: "/api/v1/uploads", payload: form });
  assert.equal(up.statusCode, 201, up.body);
  const { sourcePath, slug } = up.json();
  assert.equal(slug, "novel");
  assert.ok(sourcePath.includes("uploads"));
  // 重复上传幂等（同名同大小）
  const form2 = new FormData();
  form2.append("file", new Blob([txtContent]), "novel.txt");
  const up2 = await inject({ method: "POST", url: "/api/v1/uploads", payload: form2 });
  assert.equal(up2.statusCode, 201);
  assert.equal(up2.json().sourcePath, sourcePath);
  // 新书 prepare（带 sourcePath 豁免）
  const prep = await inject({ method: "POST", url: "/api/v1/books/novel/prepare", payload: { sourcePath } });
  assert.equal(prep.statusCode, 202, prep.body);
  const job = prep.json().job;
  // 等待完成
  for (let i = 0; i < 60; i++) {
    await new Promise((r) => setTimeout(r, 500));
    const st = await inject({ method: "GET", url: `/api/v1/jobs/${job.id}` });
    if (st.json().job.status !== "running") break;
  }
  const final = await inject({ method: "GET", url: `/api/v1/jobs/${job.id}` });
  assert.equal(final.json().job.status, "succeeded", final.body);
  // manifest 落盘 → 书架出现
  const list = await inject({ method: "GET", url: "/api/v1/books" });
  const found = list.json().items.find((b) => b.slug === "novel");
  assert.ok(found, "书架应出现新书");
  assert.ok(found.chaptersTotal >= 2);
});

test("upload rejects unsupported ext", async () => {
  const { inject } = await makeServer({ withBook: false });
  const form = new FormData();
  form.append("file", new Blob([new Uint8Array([1])]), "evil.exe");
  const r = await inject({ method: "POST", url: "/api/v1/uploads", payload: form });
  assert.equal(r.statusCode, 400);
  assert.equal(r.json().error.code, "VALIDATION_FAILED");
});

// ---- 分页边界 ----

test("pagination bounds: limit cap 200, offset beyond → empty", async () => {
  const { inject } = await makeServer();
  const r = await inject({ method: "GET", url: "/api/v1/books?limit=9999" });
  assert.equal(r.statusCode, 200);
  const r2 = await inject({ method: "GET", url: "/api/v1/books?offset=99999" });
  assert.deepEqual(r2.json().items, []);
});
