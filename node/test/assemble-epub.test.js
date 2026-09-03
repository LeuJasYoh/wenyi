// 迁移自 tests/test_assemble.py 的 EPUB 边界与 FB2 用例。
import { test } from "node:test";
import assert from "node:assert/strict";
import { promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import { createHash } from "node:crypto";
import JSZip from "jszip";
import { parseHTML, findAllElements } from "../src/doc/dom.js";
import { annotateEpubResource } from "../src/doc/epub-reader.js";
import { renderSegmentsHTML } from "../src/doc/writer.js";
import { Segment } from "../src/doc/models.js";

async function tmpDir() {
  return fs.mkdtemp(path.join(os.tmpdir(), "wenyi-asm2-"));
}

function sha(s) {
  return createHash("sha256").update(s, "utf-8").digest("hex");
}

function firstBodyEl(html, pred) {
  const doc = parseHTML(html);
  return findAllElements(doc, pred);
}

// ---- 模板回填：嵌套 fragment id 保留（test_nested_fragment_id_survives…）----

test("nested fragment id survives textual markup flattening", () => {
  const html = '<html><body><h2><span id="inside">Section</span></h2></body></html>';
  const [, segments, template] = annotateEpubResource(html, 0, "chapter.xhtml");
  segments[0].target = "章节";
  const chapter = {
    index: 0, title: "", segments, href: "chapter.xhtml", template, meta: {},
  };
  // 旧状态链路：模板回填（renderSegmentsHTML 等价 _render_chapter_html 的模板路径）
  const rendered = renderSegmentsHTML(template, segments);
  assert.ok(rendered.includes('id="inside"'), `标记应保留：${rendered}`);
  const doc = parseHTML(rendered);
  const heading = findAllElements(doc, (n) => n.name === "h2")[0];
  assert.ok(heading);
  const text = [];
  for (const n of heading.children ?? []) if (n.type === "text") text.push(n.data);
  assert.equal(text.join("").trim(), "章节");
});

// ---- point 注释恢复（test_epub_render_restores_point_annotation…）----

test("epub render restores point annotation link at aligned offset", () => {
  const target = "你好，世界";
  const template = `<html><body><p data-tn-id="tn1_0">Hello<sup
data-tn-annotation-id="ann-0"><a class="noteref" href="notes.xhtml#n1"
id="ref-1">1</a></sup> world</p></body></html>`;
  const segment = new Segment({
    index: 0, source: "Hello world", kind: "text", target, anchor: "tn1_0",
    meta: {
      epub_annotations: {
        version: 1, source_length: 11,
        items: [{
          id: "ann-0", mode: "point", source_start: 5, source_end: 5,
          source_text: "", marker_text: "1",
        }],
        target_digest: sha(target),
        placements: [{
          id: "ann-0", target_start: 2, target_end: 2, status: "aligned", method: "model",
        }],
      },
    },
  });
  const rendered = renderSegmentsHTML(template, [segment]);
  const doc = parseHTML(rendered);
  const ref = findAllElements(doc, (n) => n.name === "a")[0];
  assert.ok(ref, "注释链接应恢复");
  assert.equal(ref.attribs.href, "notes.xhtml#n1");
  assert.equal(ref.attribs.id, "ref-1");
  assert.ok((ref.attribs.class ?? "").split(/\s+/).includes("noteref"));
  assert.equal(ref.parent.name, "sup", "链接应包裹在 sup 内");
  // 正文剥掉标记等于 target
  const p = findAllElements(doc, (n) => n.name === "p")[0];
  const textParts = [];
  for (const n of p.children ?? []) {
    if (n.type === "text") textParts.push(n.data);
    else if (n.name === "sup") textParts.push(n.children?.map((c) => c.data).join("") ?? "");
  }
  assert.equal(textParts.join("").replace(/1/g, ""), target);
  assert.ok(!rendered.includes("data-tn-annotation-id"), "标记属性应清除");
});

// ---- range 注释恢复 ----

test("epub render restores range annotation around target phrase", () => {
  const targetPhrase = "国境隧道";
  const target = "火车穿过国境隧道后停下。";
  const start = target.indexOf(targetPhrase);
  const end = start + [...targetPhrase].length;
  const template = `<html><body><p data-tn-id="tn1_0"><a class="cyu"
data-tn-annotation-id="ann-0" href="notes.xhtml#note-1" id="ref-1">border
tunnel<sup class="key" id="key-1">〔＊1〕</sup></a> opens.</p></body></html>`;
  const segment = new Segment({
    index: 0, source: "border tunnel opens.", kind: "text", target, anchor: "tn1_0",
    meta: {
      epub_annotations: {
        version: 1,
        items: [{
          id: "ann-0", mode: "range", source_start: 0, source_end: 13,
          source_text: "border tunnel", marker_text: "〔＊1〕",
        }],
        target_digest: sha(target),
        placements: [{ id: "ann-0", target_start: start, target_end: end, status: "aligned", method: "model" }],
      },
    },
  });
  const rendered = renderSegmentsHTML(template, [segment]);
  const doc = parseHTML(rendered);
  const link = findAllElements(doc, (n) => n.name === "a")[0];
  assert.ok(link);
  assert.equal(link.attribs.href, "notes.xhtml#note-1");
  assert.equal(link.attribs.id, "ref-1");
  assert.ok((link.attribs.class ?? "").split(/\s+/).includes("cyu"));
  const marker = findAllElements(link, (n) => n.name === "sup")[0];
  assert.ok(marker, "range 末尾标记 sup 应保留");
  assert.equal(marker.attribs.id, "key-1");
  // 链接文本（去掉标记）= 目标短语
  const linkText = (link.children ?? []).map((c) => (c.type === "text" ? c.data : "")).join("");
  assert.equal(linkText, targetPhrase);
});

// ---- 降级：digest 不匹配 → 段末可点击标记 ----

test("stale digest degrades to clickable end-of-paragraph marker", () => {
  const target = "全新译文。";
  const template = `<html><body><p data-tn-id="tn1_0">origin<a class="noteref" id="ref-1" data-tn-annotation-id="ann-0" href="notes.xhtml#n1">1</a></p></body></html>`;
  const segment = new Segment({
    index: 0, source: "origin", kind: "text", target, anchor: "tn1_0",
    meta: {
      epub_annotations: {
        version: 1,
        items: [{ id: "ann-0", mode: "point", source_start: 6, source_end: 6, source_text: "", marker_text: "1" }],
        target_digest: "stale-digest",
        placements: [{ id: "ann-0", target_start: 2, target_end: 2, status: "aligned", method: "model" }],
      },
    },
  });
  const rendered = renderSegmentsHTML(template, [segment]);
  const doc = parseHTML(rendered);
  const link = findAllElements(doc, (n) => n.name === "a")[0];
  assert.ok(link, "降级保留链接外壳");
  assert.equal(link.attribs.href, "notes.xhtml#n1");
  const p = findAllElements(doc, (n) => n.name === "p")[0];
  // 链接位于段末
  const last = p.children[p.children.length - 1];
  assert.equal(last, link);
  const linkText = (link.children ?? []).map((c) => c.data ?? "").join("");
  assert.equal(linkText, "1");
});

// ---- 译文==源文 跳过替换 ----

test("segment equal to source keeps original block untouched", () => {
  const template = `<html><body><p data-tn-id="tn1_0"><em>Hello</em> world</p></body></html>`;
  const segment = new Segment({
    index: 0, source: "Hello world", kind: "text", target: "Hello world", anchor: "tn1_0", meta: {},
  });
  const rendered = renderSegmentsHTML(template, [segment]);
  assert.ok(rendered.includes("<em>Hello</em> world"), `应保留原块：${rendered}`);
});

// ---- 竖排转横排 ----

test("vertical epub gets horizontal override on backfill", async () => {
  const dir = await tmpDir();
  const ROOT = path.resolve(import.meta.dirname, "../..");
  const { execFileSync } = await import("node:child_process");
  const container = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`;
  const opf = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>縦書き小説</dc:title><dc:language>ja</dc:language></metadata>
  <manifest><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine page-progression-direction="rtl"><itemref idref="ch1"/></spine>
</package>`;
  const ch1 = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" class="vrtl"><head><title>第一章</title></head><body>
<h1>第一章　出会い</h1>
<p>綾小路は教室の窓際に座っていた。</p>
</body></html>`;
  const epubPath = path.join(dir, "vertical.epub");
  const zip = new JSZip();
  zip.file("mimetype", "application/epub+zip", { compression: "STORE" });
  zip.file("META-INF/container.xml", container);
  zip.file("OEBPS/content.opf", opf);
  zip.file("OEBPS/ch1.xhtml", ch1);
  await fs.writeFile(epubPath, await zip.generateAsync({ type: "nodebuffer" }));
  await fs.writeFile(path.join(dir, "config.yaml"), `language:\n  source: ja\n  target: zh\nllm:\n  provider: fake\n  tiers:\n    strong: {model: p}\n    cheap: {model: f}\npipeline:\n  review: false\n  polish: false\n  consistency_qa: false\n  book_understanding: false\npaths:\n  state_dir: state\n`, "utf-8");
  const goExe = path.join(ROOT, "dist", "wenyi.exe");
  execFileSync(goExe, ["-c", path.join(dir, "config.yaml"), "translate", epubPath, "--review=false", "--qa=false", "--polish=false"], {
    cwd: dir, encoding: "utf-8",
    env: { ...process.env, WENYI_NODE_CLI: path.join(ROOT, "node", "cli.js"), WENYI_FAKE_ROUTING: "1" },
  });
  const stateEntries = await fs.readdir(path.join(dir, "state"));
  const stateDir = path.join(dir, "state", stateEntries[0]);
  const { assemble } = await import("../src/doc/writer.js");
  const out = await assemble(stateDir, epubPath, { outFormat: "epub" });
  const outZip = await JSZip.loadAsync(await fs.readFile(out));
  const outOpf = await outZip.files["OEBPS/content.opf"].async("string");
  assert.ok(outOpf.includes('page-progression-direction="ltr"'), "OPF 应改 ltr");
  const outCh1 = await outZip.files["OEBPS/ch1.xhtml"].async("string");
  assert.ok(outCh1.includes("trans-novel-horizontal-override"), "应注入横排覆盖样式");
  assert.ok(/writing-mode:\s*horizontal-tb\s*!important/.test(outCh1));
});

// ---- FB2 → EPUB 图片与封面 ----

test("fb2 images and cover are preserved in generated epub", async () => {
  const dir = await tmpDir();
  const ROOT = path.resolve(import.meta.dirname, "../..");
  const { execFileSync } = await import("node:child_process");
  const fb2Text = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0"
             xmlns:xlink="http://www.w3.org/1999/xlink">
<description><title-info>
  <book-title>Illustrated Book</book-title>
  <coverpage><image xlink:href="#cover.jpg"/></coverpage>
</title-info></description>
<body><section><title><p>Chapter</p></title>
  <image xlink:href="#inside.png"/><p>Illustrated text.</p>
</section></body>
<binary id="cover.jpg" content-type="image/jpeg">Y292ZXItYnl0ZXM=</binary>
<binary id="inside.png" content-type="image/png">aW5zaWRlLWJ5dGVz</binary>
</FictionBook>`;
  const fb2Path = path.join(dir, "illustrated.fb2");
  await fs.writeFile(fb2Path, fb2Text, "utf-8");
  await fs.writeFile(path.join(dir, "config.yaml"), `language:\n  source: ja\n  target: zh\nllm:\n  provider: fake\n  tiers:\n    strong: {model: p}\n    cheap: {model: f}\npipeline:\n  review: false\n  polish: false\n  consistency_qa: false\n  book_understanding: false\npaths:\n  state_dir: state\n`, "utf-8");
  const goExe = path.join(ROOT, "dist", "wenyi.exe");
  execFileSync(goExe, ["-c", path.join(dir, "config.yaml"), "translate", fb2Path, "--review=false", "--qa=false", "--polish=false"], {
    cwd: dir, encoding: "utf-8",
    env: { ...process.env, WENYI_NODE_CLI: path.join(ROOT, "node", "cli.js"), WENYI_FAKE_ROUTING: "1" },
  });
  const stateEntries = await fs.readdir(path.join(dir, "state"));
  const stateDir = path.join(dir, "state", stateEntries[0]);
  const { assemble } = await import("../src/doc/writer.js");
  const out = await assemble(stateDir, fb2Path, { outFormat: "epub", aboutPage: false });
  const outZip = await JSZip.loadAsync(await fs.readFile(out));
  const names = Object.keys(outZip.files);
  const coverName = names.find((n) => n.endsWith("images/cover.jpg"));
  const insideName = names.find((n) => n.endsWith("images/inside.png"));
  const chapterName = names.find((n) => n.endsWith("/ch0.xhtml"));
  assert.ok(coverName, "封面应存在");
  assert.ok(insideName, "插图应存在");
  assert.ok(chapterName, "章节应存在");
  const coverBytes = await outZip.files[coverName].async("nodebuffer");
  const insideBytes = await outZip.files[insideName].async("nodebuffer");
  assert.ok(coverBytes.equals(Buffer.from("cover-bytes")));
  assert.ok(insideBytes.equals(Buffer.from("inside-bytes")));
  const chapterHTML = await outZip.files[chapterName].async("string");
  assert.ok(chapterHTML.includes('src="images/inside.png"'), `章内应引用插图：${chapterHTML.slice(0, 300)}`);
  const opfName = names.find((n) => n.endsWith("content.opf"));
  const opfText = await outZip.files[opfName].async("string");
  assert.ok(opfText.includes('properties="cover-image"'), "OPF 应标记 cover-image");
});

// ---- 一致性硬校验：状态与原书不匹配时报错 ----

test("backfill rejects when source text mismatches original epub", async () => {
  const dir = await tmpDir();
  const epubPath = path.join(dir, "novel.epub");
  const ROOT = path.resolve(import.meta.dirname, "../..");
  const { execFileSync } = await import("node:child_process");
  const srcZip = await JSZip.loadAsync(await fs.readFile(path.join(ROOT, "testdata", "epub", "sample.epub")));
  const entries = [];
  for (const [name, file] of Object.entries(srcZip.files)) {
    if (file.dir) continue;
    entries.push({ name, data: await file.async("nodebuffer"), store: name === "mimetype" });
  }
  // 篡改 ch1 源文（模拟用户换书）
  const ch1 = entries.find((e) => e.name === "OEBPS/ch1.xhtml");
  ch1.data = Buffer.from(ch1.data.toString("utf-8").replace("綾小路", "別人"), "utf-8");
  const { writeZipAtomic } = await import("../src/doc/writer-utils.js");
  await writeZipAtomic(epubPath, entries);
  await fs.writeFile(path.join(dir, "config.yaml"), `language:\n  source: ja\n  target: zh\nllm:\n  provider: fake\n  tiers:\n    strong: {model: p}\n    cheap: {model: f}\npipeline:\n  review: false\n  polish: false\n  consistency_qa: false\n  book_understanding: false\npaths:\n  state_dir: state\n`, "utf-8");
  // 先用原始书建状态
  const origPath = path.join(dir, "orig.epub");
  await fs.copyFile(path.join(ROOT, "testdata", "epub", "sample.epub"), origPath);
  const goExe = path.join(ROOT, "dist", "wenyi.exe");
  execFileSync(goExe, ["-c", path.join(dir, "config.yaml"), "translate", origPath, "--review=false", "--qa=false", "--polish=false"], {
    cwd: dir, encoding: "utf-8",
    env: { ...process.env, WENYI_NODE_CLI: path.join(ROOT, "node", "cli.js"), WENYI_FAKE_ROUTING: "1" },
  });
  // orig 的状态目录名 = サンプル小説；把状态目录复制为篡改书同名使用
  const stateEntries = await fs.readdir(path.join(dir, "state"));
  const stateDir = path.join(dir, "state", stateEntries[0]);
  const { assemble } = await import("../src/doc/writer.js");
  await assert.rejects(
    () => assemble(stateDir, epubPath, { outFormat: "epub" }),
    (err) => {
      assert.match(err.message, /一致性校验失败|不一致/);
      return true;
    },
  );
});
