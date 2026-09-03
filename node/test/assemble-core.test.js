// 迁移自 tests/test_assemble.py::TestAssembleText 与 test_pdf_support.py 的
// html/图片组装用例。状态目录由 Go 引擎生成（等价 _run 辅助）。
import { test } from "node:test";
import assert from "node:assert/strict";
import { promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import { execFileSync } from "node:child_process";
import JSZip from "jszip";
import { fileURLToPath } from "node:url";
import { parseHTML, findAllElements } from "../src/doc/dom.js";
import { loadDocument } from "../src/doc/segmenter.js";

const ROOT = path.resolve(import.meta.dirname, "../..");
const TESTDATA = path.join(ROOT, "testdata");

async function tmpDir() {
  return fs.mkdtemp(path.join(os.tmpdir(), "wenyi-asm-"));
}

/** 用 Go 引擎跑完整翻译流程生成状态（等价 Python 测试的 _run）。 */
async function runTranslate(input, stateDir) {
  // 返回实际书状态目录（按书名 slug）
  const goExe = process.env.WENYI_GO_EXE ?? path.join(ROOT, "dist", "wenyi.exe");
  const cwd = path.dirname(stateDir);
  execFileSync(goExe, [
    "-c", path.join(cwd, "config.yaml"),
    "translate", input, "--review=false", "--qa=false", "--polish=false",
  ], {
    cwd,
    encoding: "utf-8",
    stdio: ["ignore", "pipe", "pipe"],
    env: { ...process.env, WENYI_NODE_CLI: path.join(ROOT, "node", "cli.js"), WENYI_FAKE_ROUTING: "1" },
  });
  const entries = await fs.readdir(stateDir);
  if (entries.length !== 1) throw new Error("状态目录异常：" + entries.join(","));
  return path.join(stateDir, entries[0]);
}

async function writeConfig(dir) {
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
  backtranslate_sample: 0
  consistency_qa: false
  book_understanding: false
paths:
  state_dir: state
`, "utf-8");
}

test("txt input to txt", async () => {
  const dir = await tmpDir();
  await writeConfig(dir);
  const txt = path.join(dir, "novel.txt");
  await fs.copyFile(path.join(TESTDATA, "sample.txt"), txt);
  const stateDir = await runTranslate(txt, path.join(dir, "state"), path.join(dir, "state", "state"));
  const { assemble } = await import("../src/doc/writer.js");
  const out = await assemble(stateDir, txt, { outFormat: "txt" });
  assert.ok(out.endsWith(".txt"));
  assert.equal(path.basename(out), "novel.zh.txt");
  assert.equal(path.dirname(out), path.join(dir, "output"));
  const content = await fs.readFile(out, "utf-8");
  assert.match(content, /译\d/);
});

test("txt input to epub (generated, about page, re-parseable)", async () => {
  const dir = await tmpDir();
  await writeConfig(dir);
  const txt = path.join(dir, "novel.txt");
  await fs.copyFile(path.join(TESTDATA, "sample.txt"), txt);
  const stateDir = await runTranslate(txt, path.join(dir, "state"), path.join(dir, "state", "state"));
  const { assemble } = await import("../src/doc/writer.js");
  const out = await assemble(stateDir, txt, { outFormat: "epub" });
  assert.ok(out.endsWith(".epub"));
  assert.equal(path.basename(out), "novel.zh.epub");
  const zip = await JSZip.loadAsync(await fs.readFile(out));
  const names = Object.keys(zip.files);
  const aboutName = names.find((n) => n.endsWith("trans-novel-about.xhtml"));
  assert.ok(aboutName, "关于页应存在");
  const about = await zip.files[aboutName].async("string");
  assert.ok(about.includes("关于此翻译"));
  // 重新解析生成的 EPUB
  const doc = await loadDocument(out, "ja", "zh");
  assert.ok(doc.chapters.length >= 2);
  const allText = doc.chapters.flatMap((c) => c.text_segments.map((s) => s.source)).join("");
  assert.match(allText, /译\d/);
});

test("txt input to markdown keeps heading levels", async () => {
  const dir = await tmpDir();
  await writeConfig(dir);
  const txt = path.join(dir, "novel.txt");
  await fs.copyFile(path.join(TESTDATA, "sample.txt"), txt);
  const stateDir = await runTranslate(txt, path.join(dir, "state"), path.join(dir, "state", "state"));
  const { assemble } = await import("../src/doc/writer.js");
  const out = await assemble(stateDir, txt, { outFormat: "markdown" });
  const content = await fs.readFile(out, "utf-8");
  assert.match(content, /^# /m);
  assert.match(content, /译\d/);
});

test("txt input bilingual txt pairs target and source", async () => {
  const dir = await tmpDir();
  await writeConfig(dir);
  const txt = path.join(dir, "novel.txt");
  await fs.copyFile(path.join(TESTDATA, "sample.txt"), txt);
  const stateDir = await runTranslate(txt, path.join(dir, "state"), path.join(dir, "state", "state"));
  const { assemble } = await import("../src/doc/writer.js");
  const out = await assemble(stateDir, txt, { outFormat: "txt", bilingual: true, order: "target_first" });
  assert.ok(path.basename(out).includes(".zh-bi"));
  const content = await fs.readFile(out, "utf-8");
  // 目标文在前、源文在后（非 heading 段）
  const lines = content.split("\n");
  const srcIdx = lines.findIndex((l) => l.startsWith("綾小路"));
  const tgtIdx = lines.findIndex((l) => l.includes("译"));
  assert.ok(srcIdx > tgtIdx, `target 应在 source 前：tgt=${tgtIdx} src=${srcIdx}`);
});

// ---- EPUB 回填（源为 EPUB）----

test("epub input backfill keeps anchors and restores translated text", async () => {
  const dir = await tmpDir();
  await writeConfig(dir);
  const epub = path.join(dir, "novel.epub");
  await fs.copyFile(path.join(TESTDATA, "epub", "sample.epub"), epub);
  const stateDir = await runTranslate(epub, path.join(dir, "state"), path.join(dir, "state", "state"));
  assert.ok(await fs.stat(path.join(stateDir, "manifest.json")));
  const { assemble } = await import("../src/doc/writer.js");
  const out = await assemble(stateDir, epub, { outFormat: "epub" });
  const zip = await JSZip.loadAsync(await fs.readFile(out));
  const ch1 = await zip.files["OEBPS/ch1.xhtml"].async("string");
  assert.match(ch1, /译\d/);
  // 回填后不残留 data-tn-id
  assert.ok(!ch1.includes("data-tn-id"), "回填后应删 data-tn-id");
  // OPF 书名/语言
  const opf = await zip.files["OEBPS/content.opf"].async("string");
  assert.ok(opf.includes("-wenyi-zh"), `OPF 书名应含 -wenyi-zh：${opf.slice(0, 400)}`);
  assert.ok(opf.includes("zh-Hans"));
});

test("epub input bilingual backfill injects source blocks and css", async () => {
  const dir = await tmpDir();
  await writeConfig(dir);
  const epub = path.join(dir, "novel.epub");
  await fs.copyFile(path.join(TESTDATA, "epub", "sample.epub"), epub);
  const stateDir = await runTranslate(epub, path.join(dir, "state"), path.join(dir, "state", "state"));
  const { assemble } = await import("../src/doc/writer.js");
  const out = await assemble(stateDir, epub, { outFormat: "epub", bilingual: true });
  assert.ok(path.basename(out).includes("-bi"));
  const zip = await JSZip.loadAsync(await fs.readFile(out));
  const ch1 = await zip.files["OEBPS/ch1.xhtml"].async("string");
  assert.ok(ch1.includes("tn-source"), "双语原文块应存在");
  assert.ok(ch1.includes("prefers-color-scheme"), "dark mode CSS 应存在");
  assert.ok(ch1.includes("綾小路"), "原文应保留");
});

// ---- 旧状态/无模板章渲染（_render_chapter_html）----

test("render chapter html flattens markup to paragraphs", async () => {
  const { renderChapterHTML } = await import("../src/doc/writer.js");
  const chapter = {
    index: 0, title: "章", meta: { heading_level: 2 },
    segments: [
      { index: 0, source: "見出し", kind: "heading", target: "标题", cont: false, meta: {} },
      { index: 1, source: "本文です。", kind: "text", target: "这是正文。", cont: false, meta: {} },
    ],
  };
  const html = renderChapterHTML(chapter);
  assert.ok(html.includes("<h2>标题</h2>"));
  assert.ok(html.includes("<p>这是正文。</p>"));
});
