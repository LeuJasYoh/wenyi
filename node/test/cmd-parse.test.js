// 阶段 1 验收：对 testdata/ 全部样例跑 doc parse，产物关键字段断言（任务书 §6 阶段 1 验收）。
import { test } from "node:test";
import assert from "node:assert/strict";
import { promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import { execFileSync } from "node:child_process";

const ROOT = path.resolve(import.meta.dirname, "../..");
const TESTDATA = path.join(ROOT, "testdata");

async function runParse(input, slug) {
  const stateRoot = await fs.mkdtemp(path.join(os.tmpdir(), "wenyi-parse-"));
  const stateDir = path.join(stateRoot, slug);
  execFileSync(
    process.execPath,
    [
      path.join(ROOT, "node", "cli.js"),
      "doc",
      "parse",
      "--input",
      input,
      "--state-dir",
      stateDir,
      "--lang",
      "ja,zh",
    ],
    { encoding: "utf-8" },
  );
  const doc = JSON.parse(await fs.readFile(path.join(stateDir, "source", "doc.json"), "utf-8"));
  const chapterFiles = (await fs.readdir(path.join(stateDir, "chapters"))).filter((f) => f.endsWith(".json"));
  return { stateDir, doc, chapterFiles };
}

test("parse sample.txt produces doc.json + chapters with key fields", async () => {
  const { stateDir, doc, chapterFiles } = await runParse(path.join(TESTDATA, "sample.txt"), "sample-txt");
  assert.equal(doc.fmt, "text");
  assert.equal(doc.source_lang, "ja");
  assert.equal(doc.target_lang, "zh");
  assert.equal(doc.chapters.length, 2);
  assert.deepEqual(chapterFiles.sort(), ["ch0.json", "ch1.json"]);
  const ch0 = JSON.parse(await fs.readFile(path.join(stateDir, "chapters", "ch0.json"), "utf-8"));
  assert.equal(ch0.title, "第一章　出会い");
  assert.ok(ch0.segments.length >= 4);
  assert.equal(ch0.segments[0].kind, "heading");
});

test("parse sample.epub produces anchors and epub meta", async () => {
  const { doc } = await runParse(path.join(TESTDATA, "epub", "sample.epub"), "sample-epub");
  assert.equal(doc.fmt, "epub");
  assert.equal(doc.meta.epub_schema, 4);
  assert.ok(Array.isArray(doc.meta.epub_resources));
  assert.equal(doc.chapters.length, 2);
  for (const seg of doc.chapters[0].segments) {
    assert.ok(seg.anchor != null);
    assert.ok(seg.resource_href != null);
    assert.ok(!("epub_inline" in seg.meta));
  }
});

test("parse nested-both.epub splits by nav", async () => {
  const { doc } = await runParse(path.join(TESTDATA, "epub", "nested-both.epub"), "nested-both");
  assert.deepEqual(doc.chapters.map((c) => c.title), ["PART I", "PART II"]);
  assert.equal(doc.meta.epub_split_toc_path, "OEBPS/nav.xhtml");
  assert.equal(doc.meta.toc_entries.length, 8);
});

test("parse cross-resource.epub spans chapters across xhtml", async () => {
  const { doc } = await runParse(path.join(TESTDATA, "epub", "cross-resource.epub"), "cross");
  assert.equal(doc.chapters.length, 2);
  assert.equal(doc.chapters[0].segments.length, 4);
});

test("parse grouped-nav.epub inherits group boundaries", async () => {
  const { doc } = await runParse(path.join(TESTDATA, "epub", "grouped-nav.epub"), "grouped");
  assert.deepEqual(doc.chapters.map((c) => c.title), ["PART I", "PART II"]);
});

test("parse inline-sample.epub keeps annotations only", async () => {
  const { doc } = await runParse(path.join(TESTDATA, "epub", "inline-sample.epub"), "inline");
  assert.equal(doc.fmt, "epub");
  const segs = doc.chapters.flatMap((c) => c.segments);
  assert.ok(segs.every((s) => !("epub_inline" in s.meta)));
});
