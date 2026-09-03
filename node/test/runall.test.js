// 迁移自 test_newfeatures.py::TestRunAll（连续全流程：translate→review→qa→report→assemble）。
import { test } from "node:test";
import assert from "node:assert/strict";
import { promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import { execFileSync } from "node:child_process";
import JSZip from "jszip";

const ROOT = path.resolve(import.meta.dirname, "../..");

async function tmpDir() {
  return fs.mkdtemp(path.join(os.tmpdir(), "wenyi-runall-"));
}

function runEngine(dir, args) {
  const goExe = path.join(ROOT, "dist", "wenyi.exe");
  return execFileSync(goExe, ["-c", path.join(dir, "config.yaml"), ...args], {
    cwd: dir,
    encoding: "utf-8",
    env: {
      ...process.env,
      WENYI_NODE_CLI: path.join(ROOT, "node", "cli.js"),
      WENYI_FAKE_ROUTING: "1",
    },
  });
}

test("continuous pipeline outputs epub", async () => {
  const dir = await tmpDir();
  const txt = path.join(dir, "novel.txt");
  await fs.copyFile(path.join(ROOT, "testdata", "sample.txt"), txt);
  await fs.writeFile(path.join(dir, "config.yaml"), `language:
  source: auto
  target: zh
llm:
  provider: fake
  tiers:
    strong: {model: p}
    cheap: {model: f}
pipeline:
  review: true
  polish: true
  backtranslate_sample: 0
  consistency_qa: true
paths:
  state_dir: state
`, "utf-8");
  const out = runEngine(dir, ["translate", txt]);
  // 译文路径
  const m = out.match(/译文：(.+)/);
  assert.ok(m, `应输出译文路径：${out}`);
  const output = m[1].trim();
  assert.ok(output.endsWith(".epub"), `output = ${output}`);
  const buf = await fs.readFile(output);
  assert.equal(buf.subarray(0, 4).toString("binary"), "PK\u0003\u0004", "应为 zip");
  // 状态目录：auto 检测为 ja + 事件集
  const stateEntries = await fs.readdir(path.join(dir, "state"));
  const stateDir = path.join(dir, "state", stateEntries[0]);
  const manifest = JSON.parse(await fs.readFile(path.join(stateDir, "manifest.json"), "utf-8"));
  assert.equal(manifest.source_lang, "ja");
  const eventsRaw = await fs.readFile(path.join(stateDir, "events.jsonl"), "utf-8");
  const events = eventsRaw.split("\n").filter((l) => l.trim()).map((l) => JSON.parse(l));
  const names = events.map((e) => e.event);
  for (const want of ["run_initialized", "batch_translated", "report_saved", "assembled"]) {
    assert.ok(names.includes(want), `缺少事件 ${want}：${names.join(",")}`);
  }
  const translated = events.find((e) => e.event === "batch_translated");
  assert.ok(Array.isArray(translated.segments) && translated.segments.length > 0);
  assert.ok(typeof translated.segments[0].source === "string");
  assert.ok(typeof translated.segments[0].target === "string");
  // 报告含一致性字段
  const report = JSON.parse(await fs.readFile(path.join(stateDir, "report.json"), "utf-8"));
  assert.ok("consistency_issues" in report);
  // Review 目录（pipeline.review: true）
  const reviews = await fs.readdir(path.join(stateDir, "reviews")).catch(() => []);
  assert.ok(reviews.length >= 1, "应产生 review 目录");
  const resultRaw = await fs.readFile(path.join(stateDir, "reviews", reviews[0], "result.json"), "utf-8");
  const result = JSON.parse(resultRaw);
  assert.ok(["completed", "failed"].includes(result.status));
});

test("subset assemble is idempotent without re-translation", async () => {
  const dir = await tmpDir();
  const txt = path.join(dir, "novel.txt");
  await fs.copyFile(path.join(ROOT, "testdata", "sample.txt"), txt);
  await fs.writeFile(path.join(dir, "config.yaml"), `language:
  source: ja
  target: zh
llm:
  provider: fake
  tiers:
    strong: {model: p}
    cheap: {model: f}
pipeline:
  review: false
  polish: false
  consistency_qa: false
  book_understanding: false
paths:
  state_dir: state
`, "utf-8");
  runEngine(dir, ["translate", txt]);
  const out = runEngine(dir, ["assemble", txt]);
  const m = out.match(/译文：(.+)/);
  assert.ok(m, out);
  assert.ok(m[1].trim().endsWith(".epub"));
  // 第二次仅 assemble 仍成功（幂等）
  const out2 = runEngine(dir, ["assemble", txt]);
  assert.match(out2, /译文：|OUTPUT:/);
});

test("pdf export routes to dotnet component", async () => {
  const dir = await tmpDir();
  const txt = path.join(dir, "novel.txt");
  await fs.copyFile(path.join(ROOT, "testdata", "sample.txt"), txt);
  await fs.writeFile(path.join(dir, "config.yaml"), `language:
  source: ja
  target: zh
llm:
  provider: fake
  tiers:
    strong: {model: p}
    cheap: {model: f}
pipeline:
  review: false
  polish: false
  consistency_qa: false
  book_understanding: false
paths:
  state_dir: state
`, "utf-8");
  runEngine(dir, ["translate", txt]);
  // dotnet 组件可能未发布——断言错误信息语义（或成功时 %PDF 头）
  const goExe = path.join(ROOT, "dist", "wenyi.exe");
  let failed = false;
  let output = "";
  try {
    output = execFileSync(goExe, ["-c", path.join(dir, "config.yaml"), "translate", txt, "--review=false", "--qa=false", "--polish=false", "--format", "pdf"], {
      cwd: dir, encoding: "utf-8",
      env: { ...process.env, WENYI_NODE_CLI: path.join(ROOT, "node", "cli.js"), WENYI_FAKE_ROUTING: "1" },
    });
  } catch (e) {
    failed = true;
    output = (e.stderr ?? "") + (e.stdout ?? "");
  }
  if (failed) {
    assert.match(output, /wenyi-pdf|PDF/);
  } else {
    const m = output.match(/译文：(.+)/);
    assert.ok(m, output);
    const buf = await fs.readFile(m[1].trim());
    assert.equal(buf.subarray(0, 4).toString("binary"), "%PDF");
  }
});
