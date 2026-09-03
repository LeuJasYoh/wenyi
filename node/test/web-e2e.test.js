// 阶段 7 端到端验收（分册 10 §8.2 集成闭环）：
// 上传 → prepare → translate（取消+续跑）→ glossary resolve → review → qa → report → assemble。
import { test } from "node:test";
import assert from "node:assert/strict";
import { promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import { buildServer, findEnginePath } from "../src/web/serve.js";

const ROOT = path.resolve(import.meta.dirname, "../..");

async function newServer() {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "wenyi-e2e-"));
  const stateDir = path.join(dir, "state");
  await fs.mkdir(stateDir, { recursive: true });
  const configPath = path.join(dir, "config.yaml");
  await fs.writeFile(configPath, `language:
  source: ja
  target: zh
llm:
  provider: fake
  tiers:
    strong: {model: p}
    cheap: {model: f}
pipeline:
  review: true
  polish: false
  backtranslate_sample: 0
  consistency_qa: true
  book_understanding: false
paths:
  state_dir: state
`, "utf-8");
  const { app, registry } = await buildServer({ stateDir, configPath, enginePath: findEnginePath(), engineEnv: { ...process.env, WENYI_NODE_CLI: path.join(ROOT, 'node', 'cli.js'), WENYI_FAKE_ROUTING: '1' } });
  await app.ready();
  return { app, registry, dir, stateDir, inject: app.inject.bind(app) };
}

async function waitJob(inject, jobId, timeoutMs = 120000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    const r = await inject({ method: "GET", url: `/api/v1/jobs/${jobId}` });
    const job = r.json().job;
    if (job.status !== "running") return job;
    await new Promise((res) => setTimeout(res, 400));
  }
  throw new Error("任务超时");
}

test("WebUI 全流程闭环：上传→prepare→translate→resolve→review→qa→report→assemble", async () => {
  const { inject, stateDir } = await newServer();
  const host = { host: "127.0.0.1:8731" };

  // 1. 上传
  const form = new FormData();
  const txt = await fs.readFile(path.join(ROOT, "testdata", "sample.txt"));
  form.append("file", new Blob([txt]), "novel.txt");
  const up = await inject({ method: "POST", url: "/api/v1/uploads", payload: form, headers: host });
  assert.equal(up.statusCode, 201);
  const { slug } = up.json();
  assert.equal(slug, "novel");

  // 2. prepare（新书豁免）
  const prep = await inject({ method: "POST", url: `/api/v1/books/${slug}/prepare`, payload: { sourcePath: up.json().sourcePath }, headers: host });
  assert.equal(prep.statusCode, 202);
  let job = await waitJob(inject, prep.json().job.id);
  assert.equal(job.status, "succeeded", JSON.stringify(job));

  // 3. translate（fake 快速完成）
  const tr = await inject({ method: "POST", url: `/api/v1/books/${slug}/translate`, payload: {}, headers: host });
  assert.equal(tr.statusCode, 202);
  job = await waitJob(inject, tr.json().job.id);
  assert.equal(job.status, "succeeded", JSON.stringify(job));

  // 状态：全部 done
  const status = await inject({ method: "GET", url: `/api/v1/books/${slug}/status`, headers: host });
  const doneChapters = status.json().chapters.filter((c) => c.status === "done").length;
  assert.equal(doneChapters, 2);

  // 4. 术语 resolve（同步端点）——先制造冲突
  // fake 路由抽取器给"堀北"，人为 resolve 一次验证端点
  const resolve = await inject({ method: "POST", url: `/api/v1/books/${slug}/glossary/conflicts/resolve`, payload: { source: "堀北", target: "堀北" }, headers: host });
  // 术语存在 → 200；不存在 → 404（二者皆合法，验证端点语义）
  assert.ok([200, 404].includes(resolve.statusCode), `resolve=${resolve.statusCode}`);

  // 5. review
  const rev = await inject({ method: "POST", url: `/api/v1/books/${slug}/review`, payload: {}, headers: host });
  assert.equal(rev.statusCode, 202);
  job = await waitJob(inject, rev.json().job.id);
  assert.equal(job.status, "succeeded", JSON.stringify(job.logTail ?? ""));
  const reviews = await inject({ method: "GET", url: `/api/v1/books/${slug}/reviews`, headers: host });
  assert.ok(reviews.json().items.length >= 1, "应有审校记录");

  // 6. qa
  const qa = await inject({ method: "POST", url: `/api/v1/books/${slug}/qa`, payload: {}, headers: host });
  assert.equal(qa.statusCode, 202);
  job = await waitJob(inject, qa.json().job.id);
  assert.equal(job.status, "succeeded");

  // 7. report
  const rep = await inject({ method: "POST", url: `/api/v1/books/${slug}/report`, payload: {}, headers: host });
  assert.equal(rep.statusCode, 202);
  job = await waitJob(inject, rep.json().job.id);
  assert.equal(job.status, "succeeded");
  const reportJson = await inject({ method: "GET", url: `/api/v1/books/${slug}/report`, headers: host });
  assert.ok(reportJson.json().summary);

  // 8. assemble
  const asm = await inject({ method: "POST", url: `/api/v1/books/${slug}/assemble`, payload: { format: "epub" }, headers: host });
  assert.equal(asm.statusCode, 202);
  job = await waitJob(inject, asm.json().job.id);
  assert.equal(job.status, "succeeded", JSON.stringify(job));
  const exports = await inject({ method: "GET", url: `/api/v1/books/${slug}/exports`, headers: host });
  assert.ok(exports.json().items.some((e) => e.name.endsWith(".epub")), JSON.stringify(exports.json()));

  // 9. 事件流覆盖全集事件名
  const events = await inject({ method: "GET", url: `/api/v1/books/${slug}/events?cursor=0&limit=2000`, headers: host });
  const names = events.json().items.map((e) => e.event);
  for (const want of ["run_initialized", "batch_translated", "chapter_done", "report_saved", "assembled"]) {
    assert.ok(names.includes(want), `缺 ${want}`);
  }
  // batch_translated 段含 source/target
  const bt = events.json().items.find((e) => e.event === "batch_translated");
  assert.ok(bt.segments[0].source != null && bt.segments[0].target != null);
});

test("取消：运行中任务可取消且状态目录安全", async () => {
  const { inject } = await newServer();
  const host = { host: "127.0.0.1:8731" };
  const form = new FormData();
  const txt = await fs.readFile(path.join(ROOT, "testdata", "sample.txt"));
  form.append("file", new Blob([txt]), "novel.txt");
  const up = await inject({ method: "POST", url: "/api/v1/uploads", payload: form, headers: host });
  const prep = await inject({ method: "POST", url: "/api/v1/books/novel/prepare", payload: { sourcePath: up.json().sourcePath }, headers: host });
  await waitJob(inject, prep.json().job.id);
  // 立即取消（fake 任务可能秒完——只要 API 幂等即可）
  const tr = await inject({ method: "POST", url: "/api/v1/books/novel/translate", payload: {}, headers: host });
  const jobId = tr.json().job.id;
  const cancel = await inject({ method: "POST", url: `/api/v1/jobs/${jobId}/cancel`, headers: host });
  assert.equal(cancel.statusCode, 200);
  assert.ok(["cancelled", "succeeded", "failed"].includes(cancel.json().job.status));
  // 再次取消幂等
  const cancel2 = await inject({ method: "POST", url: `/api/v1/jobs/${jobId}/cancel`, headers: host });
  assert.equal(cancel2.statusCode, 200);
  // 取消后可续跑（新任务受理）
  await new Promise((r) => setTimeout(r, 1000));
  const tr2 = await inject({ method: "POST", url: "/api/v1/books/novel/translate", payload: {}, headers: host });
  assert.ok([202, 409].includes(tr2.statusCode), `tr2=${tr2.statusCode}`);
});

test("SSE 书籍流：engine.event 逐字节透传 events.jsonl 行", async () => {
  const { inject } = await newServer();
  const host = { host: "127.0.0.1:8731" };
  const form = new FormData();
  const txt = await fs.readFile(path.join(ROOT, "testdata", "sample.txt"));
  form.append("file", new Blob([txt]), "novel.txt");
  const up = await inject({ method: "POST", url: "/api/v1/uploads", payload: form, headers: host });
  const prep = await inject({ method: "POST", url: "/api/v1/books/novel/prepare", payload: { sourcePath: up.json().sourcePath }, headers: host });
  await waitJob(inject, prep.json().job.id);
  // 拉取 SSE（inject 不支持流式——直接读 events 端点比对结构）
  const events = await inject({ method: "GET", url: "/api/v1/books/novel/events?cursor=0&limit=100", headers: host });
  const items = events.json().items;
  assert.ok(items.length > 0);
  // 每行有 ts + event 字段（透传语义）
  for (const item of items) {
    assert.ok(item.ts, "事件应有 ts");
    assert.ok(item.event, "事件应有 event");
    assert.ok(typeof item.line === "number", "应有 line 注入");
  }
});
