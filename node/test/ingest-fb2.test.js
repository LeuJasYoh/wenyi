// 迁移自 tests/test_ingest.py::TestFb2Ingest。
import { test } from "node:test";
import assert from "node:assert/strict";
import { promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import { loadDocument } from "../src/doc/segmenter.js";
import { readFb2Binaries } from "../src/doc/fb2-reader.js";
import { KIND_HEADING } from "../src/doc/models.js";

const _FB2_FLAT = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info><book-title>平铺之书</book-title></title-info></description>
<body>
  <section><title><p>第一章</p></title><p>第一段。</p><p>第二段。</p></section>
  <section><title><p>第二章</p></title><p>仅一段。</p></section>
</body>
<body name="notes"><section><p>这是注释，应被跳过。</p></section></body>
</FictionBook>
`;

const _FB2_BODY_TITLE = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info><book-title>正文标题之书</book-title></title-info></description>
<body>
  <title><p>作者姓名</p><p>正文标题之书</p></title>
  <section><title><p>第一章</p></title><p>第一段。</p></section>
</body>
</FictionBook>
`;

const _FB2_NESTED = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info><book-title>嵌套之书</book-title></title-info></description>
<body>
  <section>
    <title><p>第一部</p></title>
    <section><title><p>第一章</p></title><p>一章首段。</p><p>一章次段。</p></section>
    <section><title><p>第二章</p></title><p>二章仅一段。</p></section>
  </section>
</body>
</FictionBook>
`;

const _FB2_BLOCKS = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info><book-title>块之书</book-title></title-info></description>
<body>
  <section>
    <title><p>第一章</p></title>
    <epigraph><p>题记一行。</p><text-author>题记作者</text-author></epigraph>
    <p>普通段落。</p>
    <subtitle>场景小标题</subtitle>
    <poem><title><p>诗名</p></title>
      <stanza><v>第一诗行。</v><v>第二诗行。</v></stanza>
      <text-author>诗人</text-author></poem>
    <cite><p>引文段落。</p><text-author>引文作者</text-author></cite>
    <p>结尾段落。</p>
  </section>
</body>
</FictionBook>
`;

const _FB2_IMAGES = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0"
             xmlns:xlink="http://www.w3.org/1999/xlink">
<description><title-info>
  <book-title>插图之书</book-title>
  <coverpage><image xlink:href="#cover.jpg"/></coverpage>
</title-info></description>
<body>
  <section><title><p>第一章</p></title>
    <image xlink:href="#inside.png"/>
    <p>带插图的正文。</p>
  </section>
</body>
<binary id="cover.jpg" content-type="image/jpeg">Y292ZXItYnl0ZXM=</binary>
<binary id="inside.png" content-type="image/png">aW5zaWRlLWJ5dGVz</binary>
</FictionBook>
`;

async function loadFb2(content) {
  const d = await fs.mkdtemp(path.join(os.tmpdir(), "wenyi-fb2-"));
  const p = path.join(d, "novel.fb2");
  await fs.writeFile(p, content, "utf-8");
  return loadDocument(p, "ja", "zh");
}

test("flat sections and notes skipped", async () => {
  const doc = await loadFb2(_FB2_FLAT);
  assert.equal(doc.fmt, "fb2");
  assert.equal(doc.title, "平铺之书");
  assert.equal(doc.chapters.length, 2); // notes body 不计入
  const ch1 = doc.chapters[0];
  assert.equal(ch1.title, "第一章");
  assert.equal(ch1.segments[0].kind, KIND_HEADING);
  assert.equal(ch1.text_segments.length, 3); // 标题 + 2 段
  const allSrc = doc.chapters.flatMap((ch) => ch.segments.map((s) => s.source));
  assert.ok(!allSrc.includes("这是注释，应被跳过。"));
});

test("namespace variants are supported", async () => {
  const variants = {
    "2.1": _FB2_FLAT.replace("fictionbook/2.0", "fictionbook/2.1"),
    none: _FB2_FLAT.replace(' xmlns="http://www.gribuser.ru/xml/fictionbook/2.0"', ""),
  };
  for (const content of Object.values(variants)) {
    const doc = await loadFb2(content);
    assert.equal(doc.title, "平铺之书");
    assert.deepEqual(doc.chapters.map((c) => c.title), ["第一章", "第二章"]);
    assert.deepEqual(
      doc.chapters[0].text_segments.map((s) => s.source),
      ["第一章", "第一段。", "第二段。"],
    );
  }
});

test("single quoted windows-1251 declaration", async () => {
  const content = `<?xml version='1.0' encoding='windows-1251'?>
<FictionBook>
  <description><title-info><book-title>Детство</book-title></title-info></description>
  <body><section><title><p>Глава</p></title><p>Текст</p></section></body>
</FictionBook>`;
  const d = await fs.mkdtemp(path.join(os.tmpdir(), "wenyi-fb2-"));
  const p = path.join(d, "book.fb2");
  const iconv = (await import("iconv-lite")).default;
  await fs.writeFile(p, iconv.encode(content, "windows-1251"));
  const doc = await loadDocument(p, "ru", "zh");
  assert.equal(doc.title, "Детство");
  assert.equal(doc.chapters[0].segments[0].source, "Глава");
});

test("body title becomes a separate chapter", async () => {
  const doc = await loadFb2(_FB2_BODY_TITLE);
  assert.equal(doc.chapters.length, 2);
  const [titlePage, firstChapter] = doc.chapters;
  assert.equal(titlePage.title, "正文标题之书");
  assert.deepEqual(titlePage.segments.map((s) => s.source), ["作者姓名", "正文标题之书"]);
  assert.ok(titlePage.segments.every((s) => s.kind === KIND_HEADING));
  assert.deepEqual(titlePage.segments.map((s) => s.index), [0, 1]);
  assert.deepEqual(titlePage.segments.map((s) => s.anchor), ["tn0_0", "tn0_1"]);
  assert.equal(firstChapter.index, 1);
  assert.equal(firstChapter.title, "第一章");
  assert.deepEqual(firstChapter.segments.map((s) => s.anchor), ["tn1_0", "tn1_1"]);
});

test("block types not lost", async () => {
  const doc = await loadFb2(_FB2_BLOCKS);
  const ch = doc.chapters[0];
  const texts = ch.segments.map((s) => s.source);
  for (const expect of [
    "题记一行。",
    "题记作者",
    "普通段落。",
    "诗名",
    "第一诗行。",
    "第二诗行。",
    "诗人",
    "引文段落。",
    "引文作者",
    "结尾段落。",
  ]) {
    assert.ok(texts.includes(expect), `缺少 ${expect}`);
  }
  const headings = ch.segments.filter((s) => s.kind === KIND_HEADING).map((s) => s.source);
  assert.ok(headings.includes("场景小标题")); // subtitle 作为 heading
});

test("nested sections not lost", async () => {
  const doc = await loadFb2(_FB2_NESTED);
  assert.deepEqual(doc.chapters.map((c) => c.title), ["第一部", "第一章", "第二章"]);
  const allText = doc.chapters.flatMap((c) => c.text_segments.filter((s) => s.kind !== KIND_HEADING).map((s) => s.source));
  assert.ok(allText.includes("一章首段。"));
  assert.ok(allText.includes("一章次段。"));
  assert.ok(allText.includes("二章仅一段。"));
});

test("images and cover recorded without persisting binary data", async () => {
  const d = await fs.mkdtemp(path.join(os.tmpdir(), "wenyi-fb2-"));
  const p = path.join(d, "images.fb2");
  await fs.writeFile(p, _FB2_IMAGES, "utf-8");
  const doc = await loadDocument(p, "ru", "zh");
  const binaries = await readFb2Binaries(p);
  assert.equal(doc.meta.fb2_cover_image, "cover.jpg");
  assert.deepEqual(doc.meta.fb2_resources, [
    { id: "cover.jpg", content_type: "image/jpeg" },
    { id: "inside.png", content_type: "image/png" },
  ]);
  assert.deepEqual(doc.chapters[0].meta.fb2_images, [{ id: "inside.png", position: 1 }]);
  assert.ok(!JSON.stringify(doc.meta).includes(Buffer.from("cover-bytes").toString("base64")));
  assert.deepEqual(binaries["cover.jpg"], ["image/jpeg", Buffer.from("cover-bytes")]);
  assert.deepEqual(binaries["inside.png"], ["image/png", Buffer.from("inside-bytes")]);
});
