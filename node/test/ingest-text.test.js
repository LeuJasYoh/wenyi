// 迁移自 tests/test_ingest.py::TestTextIngest 与 TestSplitLongSegments。
import { test } from "node:test";
import assert from "node:assert/strict";
import { promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import { loadDocument, chapterBatches, splitLongSegments, splitText } from "../src/doc/segmenter.js";
import { Chapter, Segment, KIND_HEADING, KIND_TEXT } from "../src/doc/models.js";

const TESTDATA = path.resolve(import.meta.dirname, "../../testdata");

async function tmpDir() {
  return fs.mkdtemp(path.join(os.tmpdir(), "wenyi-ingest-"));
}

test("untitled preface does not gain book title heading", async () => {
  const d = await tmpDir();
  const p = path.join(d, "book.txt");
  await fs.writeFile(p, "preface\n\n# Chapter 1\nbody\n\n# Chapter 2\nbody", "utf-8");
  const doc = await loadDocument(p, "en", "zh");
  assert.equal(doc.chapters[0].title, "book");
  assert.deepEqual(
    doc.chapters[0].segments.map((s) => [s.kind, s.source]),
    [[KIND_TEXT, "preface"]],
  );
});

test("text chapters and segments", async () => {
  const doc = await loadDocument(path.join(TESTDATA, "sample.txt"), "ja", "zh");
  assert.equal(doc.fmt, "text");
  assert.equal(doc.chapters.length, 2);
  const ch1 = doc.chapters[0];
  assert.equal(ch1.title, "第一章　出会い");
  assert.equal(ch1.segments[0].kind, KIND_HEADING);
  assert.equal(ch1.text_segments.length, 4); // 标题 + 3 段正文
});

test("preamble before first heading does not gain book title", async () => {
  const content = "这是前言。\n\n# 第一章\n\n这是正文。\n";
  for (const suffix of [".txt", ".md"]) {
    const d = await tmpDir();
    const p = path.join(d, "novel" + suffix);
    await fs.writeFile(p, content, "utf-8");
    const document = await loadDocument(p, "zh", "en");
    assert.equal(document.chapters.length, 2);
    assert.deepEqual(document.chapters[0].segments.map((s) => s.kind), [KIND_TEXT]);
    assert.equal(document.chapters[0].segments[0].source, "这是前言。");
    assert.equal(document.chapters[1].segments[0].kind, KIND_HEADING);
    assert.equal(document.chapters[1].segments[0].source, "第一章");
  }
});

test("batching", async () => {
  const doc = await loadDocument(path.join(TESTDATA, "sample.txt"), "ja", "zh");
  const batches = chapterBatches(doc.chapters[0], 60);
  const total = batches.reduce((n, b) => n + b.length, 0);
  assert.equal(total, doc.chapters[0].text_segments.length); // 总段数守恒
  assert.ok(batches.length > 1); // 60 字符预算应切出多批
});

// ---- TestSplitLongSegments ----

test("split by sentence and cont flag", () => {
  const longSrc = "第一句。".repeat(10); // 40 字符
  const ch = new Chapter({
    index: 0,
    title: "章",
    segments: [
      new Segment({ index: 0, source: "标题", kind: KIND_HEADING, anchor: "a0" }),
      new Segment({ index: 1, source: longSrc, kind: KIND_TEXT, anchor: "a1" }),
      new Segment({ index: 2, source: "短。", kind: KIND_TEXT, anchor: "a2" }),
    ],
  });
  splitLongSegments([ch], 30);
  assert.ok(ch.segments.some((s) => s.cont));
  const longParts = ch.segments.filter((s) => !s.cont && s.anchor === "a1");
  assert.equal(longParts.length, 1); // 首段唯一带 a1
  const contParts = ch.segments.filter((s) => s.cont);
  assert.ok(contParts.every((s) => s.anchor === null));
  assert.deepEqual(
    ch.segments.map((s) => s.index),
    ch.segments.map((_, i) => i), // index 连续重排
  );
  const joined = ch.segments.filter((s) => s.anchor === "a1" || s.cont).map((s) => s.source).join("");
  assert.equal(joined, longSrc); // 拼回去等于原文
});

test("split keeps meta only on anchored first part", () => {
  const meta = {
    epub_inline: { version: 1, source_length: 40, nodes: [{ id: "a0_inline_0", offset: 0 }] },
  };
  const original = new Segment({ index: 0, source: "第一句。".repeat(10), kind: KIND_TEXT, anchor: "a0", meta });
  const ch = new Chapter({ index: 0, segments: [original] });
  splitLongSegments([ch], 20);
  assert.ok(ch.segments.length > 1);
  assert.deepEqual(ch.segments[0].meta, meta);
  assert.notEqual(ch.segments[0].meta, original.meta); // 深拷贝
  assert.ok(ch.segments.slice(1).every((s) => Object.keys(s.meta).length === 0));
});

test("no split when short", () => {
  const ch = new Chapter({
    index: 0,
    title: "章",
    segments: [new Segment({ index: 0, source: "短句。", kind: KIND_TEXT, anchor: "a0" })],
  });
  splitLongSegments([ch], 100);
  assert.equal(ch.segments.length, 1);
  assert.equal(ch.segments[0].cont, false);
});

test("oversized single sentence hard split", () => {
  const chunks = splitText("あ".repeat(50), 20);
  assert.ok(chunks.every((c) => [...c].length <= 20));
  assert.equal(chunks.join(""), "あ".repeat(50));
});

test("english splits on sentence punctuation", () => {
  const text = "Alpha beta gamma. Delta epsilon zeta! Eta theta iota?";
  const chunks = splitText(text, 25);
  assert.deepEqual(chunks, ["Alpha beta gamma.", " Delta epsilon zeta!", " Eta theta iota?"]);
  assert.equal(chunks.join(""), text);
});

test("oversized english sentence does not split words", () => {
  const text = "alphabet bravo charlie delta";
  const chunks = splitText(text, 18);
  assert.deepEqual(chunks, ["alphabet bravo", " charlie delta"]);
  assert.equal(chunks.join(""), text);
  assert.ok(!chunks[0].includes("char"));
  assert.equal(chunks[1].trim().split(/\s+/)[0], "charlie"); // Python split() 无参语义
});
