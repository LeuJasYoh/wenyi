// 迁移自 tests/test_ingest.py::TestEpubIngest。
import { test } from "node:test";
import assert from "node:assert/strict";
import { promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import JSZip from "jszip";
import { loadDocument } from "../src/doc/segmenter.js";
import { annotateEpubResource, findOpfPath, parseOpf } from "../src/doc/epub-reader.js";
import { parseTocEntries, resolveEpubHref } from "../src/doc/epub-toc.js";
import { decodeMarkup } from "../src/doc/encoding.js";
import { parseHTML, findAllElements } from "../src/doc/dom.js";
import { openZip } from "../src/doc/epub-reader.js";

const TESTDATA = path.resolve(import.meta.dirname, "../../testdata");

async function tmpDir() {
  return fs.mkdtemp(path.join(os.tmpdir(), "wenyi-epub-"));
}

function selectOne(html, selector) {
  const doc = parseHTML(html);
  return findAllElements(doc, () => true).find((n) => n.attribs?.["data-tn-annotation-id"] != null || n.attribs?.["data-tn-inline-id"] != null) ?? null;
}

// ---- 注释 point/range ----

test("epub point annotation is excluded from source", () => {
  const html = `<html><body><p>Buck Mulligan<sup id="note-wrap"><a
        id="jpref1" href="notes.xhtml#jpnote1">2</a></sup> came down.</p></body></html>`;
  const [title, segments, template] = annotateEpubResource(html, 0, "body.xhtml");
  assert.deepEqual(segments.map((s) => s.source), ["Buck Mulligan came down."]);
  const annotations = segments[0].meta.epub_annotations;
  assert.equal(annotations.source_length, [...segments[0].source].length);
  assert.deepEqual(annotations.items, [
    {
      id: "tn0_0_annotation_0",
      mode: "point",
      source_start: [..."Buck Mulligan"].length,
      source_end: [..."Buck Mulligan"].length,
      source_text: "",
      marker_text: "2",
    },
  ]);
  const rendered = parseHTML(template);
  const marker = findAllElements(rendered, (n) => n.attribs?.["data-tn-annotation-id"] != null)[0];
  assert.ok(marker != null);
  assert.equal(marker.name, "sup");
  assert.equal(marker.attribs["id"], "note-wrap");
  const link = findAllElements(marker, (n) => n.name === "a")[0];
  assert.equal(link.attribs["id"], "jpref1");
  assert.ok(!("epub_inline" in segments[0].meta));
});

test("epub range annotation keeps phrase but excludes marker", () => {
  const html = `<html><body><p><a id="ref-1" href="#note-1">国境の長いトンネル
        <sup id="mark-1">〔＊１〕</sup></a>を抜けると雪国であった。</p></body></html>`;
  const [title, segments, template] = annotateEpubResource(html, 0, "body.xhtml");
  const source = "国境の長いトンネルを抜けると雪国であった。";
  assert.deepEqual(segments.map((s) => s.source), [source]);
  const item = segments[0].meta.epub_annotations.items[0];
  assert.equal(item.mode, "range");
  assert.equal(item.source_start, 0);
  assert.equal(item.source_end, [..."国境の長いトンネル"].length);
  assert.equal(item.source_text, "国境の長いトンネル");
  assert.equal(item.marker_text, "〔＊１〕");
  const rendered = parseHTML(template);
  const link = findAllElements(rendered, (n) => n.attribs?.["data-tn-annotation-id"] != null)[0];
  assert.equal(link.name, "a");
  assert.equal(link.attribs["id"], "ref-1");
  const marker = findAllElements(link, (n) => n.name === "sup")[0];
  assert.equal(marker.attribs["id"], "mark-1");
});

test("epub records multiple annotations in source order", () => {
  const html = `<html><body><p>Alpha<sup><a href="#n1">1</a></sup> beta
        <a href="notes.xhtml#n2">linked phrase<sup>*</sup></a> end.</p></body></html>`;
  const [title, segments, template] = annotateEpubResource(html, 3, "body.xhtml");
  assert.equal(segments[0].source, "Alpha beta linked phrase end.");
  const items = segments[0].meta.epub_annotations.items;
  assert.deepEqual(items.map((i) => i.id), ["tn3_0_annotation_0", "tn3_0_annotation_1"]);
  assert.deepEqual(items.map((i) => i.mode), ["point", "range"]);
  assert.deepEqual([items[0].source_start, items[0].source_end], [5, 5]);
  assert.equal(items[1].source_text, "linked phrase");
  assert.equal((template.match(/data-tn-annotation-id/g) ?? []).length, 2);
  assert.ok(!template.includes("data-tn-inline-id"));
});

test("epub external link is not recorded as annotation", () => {
  const html = '<html><body><p>See <a href="https://example.com/x">website</a>.</p></body></html>';
  const [title, segments, template] = annotateEpubResource(html, 0, "body.xhtml");
  assert.equal(segments[0].source, "See website.");
  assert.ok(!("epub_annotations" in segments[0].meta));
  assert.ok(!template.includes("data-tn-annotation-id"));
});

test("epub linked image uses inline preservation not annotation", () => {
  const html = '<html><body><p>See <a href="#full"><img src="thumb.png"/></a> now.</p></body></html>';
  const [title, segments, template] = annotateEpubResource(html, 0, "body.xhtml");
  assert.equal(segments[0].source, "See now.");
  assert.ok(!("epub_annotations" in segments[0].meta));
  const inline = segments[0].meta.epub_inline;
  assert.equal(inline.nodes[0].tag, "a");
  const rendered = parseHTML(template);
  const link = findAllElements(rendered, (n) => n.attribs?.["data-tn-inline-id"] != null)[0];
  assert.equal(link.name, "a");
  assert.equal(link.attribs["href"], "#full");
  assert.ok(findAllElements(link, (n) => n.name === "img" && n.attribs?.["src"] === "thumb.png").length > 0);
});

test("epub semantic subscript inside link remains in source", () => {
  const html = '<html><body><p>Use <a href="#water">H<sub>2</sub>O</a> here.</p></body></html>';
  const [title, segments] = annotateEpubResource(html, 0, "body.xhtml");
  assert.equal(segments[0].source, "Use H2O here.");
  const item = segments[0].meta.epub_annotations.items[0];
  assert.equal(item.mode, "range");
  assert.equal(item.source_text, "H2O");
  assert.equal(item.marker_text, "");
});

test("epub linked exponent is not mistaken for note marker", () => {
  const html = `<html><body><p>Use <a href="references.xhtml#eq">x<sup>2</sup></a>
        and y<sup><a href="#equation">3</a></sup>.</p></body></html>`;
  const [title, segments] = annotateEpubResource(html, 0, "body.xhtml");
  assert.equal(segments[0].source, "Use x2 and y3.");
  const items = segments[0].meta.epub_annotations.items;
  assert.deepEqual(items.map((i) => i.source_text), ["x2", "3"]);
  assert.deepEqual(items.map((i) => i.marker_text), ["", ""]);
});

test("read epub persists annotation metadata", async () => {
  const d = await tmpDir();
  const p = path.join(d, "annotations.epub");
  const zip = new JSZip();
  zip.file("mimetype", "application/epub+zip");
  zip.file(
    "META-INF/container.xml",
    '<container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>',
  );
  zip.file(
    "content.opf",
    `<package><metadata><title>Annotations</title></metadata><manifest>
                    <item id="body" href="body.xhtml" media-type="application/xhtml+xml"/>
                    </manifest><spine><itemref idref="body"/></spine></package>`,
  );
  zip.file(
    "body.xhtml",
    '<html><body><p>Body<sup><a href="#note-1">1</a></sup>.</p><p id="note-1">A note.</p></body></html>',
  );
  await fs.writeFile(p, await zip.generateAsync({ type: "nodebuffer" }));
  const document = await loadDocument(p, "en", "zh");
  const first = document.chapters[0].segments[0];
  assert.equal(first.source, "Body.");
  assert.ok("epub_annotations" in first.meta);
  assert.ok(!("epub_inline" in first.meta));
});

// ---- TOC 解析 ----

test("nav without epub type uses first navigation list", async () => {
  const d = await tmpDir();
  const p = path.join(d, "toc.zip");
  const zip = new JSZip();
  zip.file(
    "OEBPS/nav.xhtml",
    `<html><body><nav><h1>Contents</h1><ol>
        <li><a href="body.xhtml#one">One</a></li>
        </ol></nav></body></html>`,
  );
  await fs.writeFile(p, await zip.generateAsync({ type: "nodebuffer" }));
  const files = await openZip(p);
  const entries = parseTocEntries(files, ["OEBPS/nav.xhtml"]);
  assert.deepEqual(entries.map((e) => e.title), ["One"]);
  assert.equal(entries[0].resource_href, "OEBPS/body.xhtml");
});

test("broken secondary toc does not block valid primary nav", async () => {
  const d = await tmpDir();
  const p = path.join(d, "toc.zip");
  const zip = new JSZip();
  zip.file(
    "OEBPS/nav.xhtml",
    `<html><body><nav epub:type="toc"><ol>
        <li><a href="body.xhtml#one">One</a></li>
        </ol></nav></body></html>`,
  );
  zip.file("OEBPS/toc.ncx", "<ncx><navMap>");
  await fs.writeFile(p, await zip.generateAsync({ type: "nodebuffer" }));
  const files = await openZip(p);
  const entries = parseTocEntries(files, ["OEBPS/nav.xhtml", "OEBPS/toc.ncx"]);
  assert.deepEqual(entries.map((e) => e.title), ["One"]);
});

test("ncx with xml extension is detected from document root", async () => {
  const document = await loadDocument(path.join(TESTDATA, "epub/nested-ncx-custom-filename.epub"), "en", "zh");
  assert.deepEqual(document.chapters.map((c) => c.title), ["PART I", "PART II"]);
  assert.deepEqual(document.meta.toc_paths, ["OEBPS/toc.xml"]);
  assert.ok(document.meta.toc_entries.every((e) => e.kind === "ncx"));
});

test("real boundary wins when empty title page has same position", async () => {
  const document = await loadDocument(path.join(TESTDATA, "epub/nested-both-empty-title-page.epub"), "en", "zh");
  assert.deepEqual(document.chapters.map((c) => c.title), ["PART I", "PART II"]);
  const [titlePage, firstPart] = document.meta.toc_entries.slice(0, 2);
  assert.equal(titlePage.boundary_position, 0);
  assert.ok(!("segment_anchor" in titlePage));
  assert.equal(firstPart.boundary_position, 0);
  assert.ok(firstPart.segment_anchor);
});

test("spine nav preserves toc list but translates visible heading", () => {
  const html = `<html><body><nav epub:type="toc">
        <h1>Contents</h1>
        <ol><li><a href="body.xhtml#one">Chapter One</a></li></ol>
        </nav></body></html>`;
  const [title, segments, template] = annotateEpubResource(html, 0, "nav.xhtml", { skipNavigation: true });
  assert.deepEqual(segments.map((s) => s.source), ["Contents"]);
  assert.ok(template.includes('href="body.xhtml#one"'));
  const rendered = parseHTML(template);
  const listItem = findAllElements(rendered, (n) => n.name === "li")[0];
  assert.ok(listItem != null);
  assert.ok(!("data-tn-id" in (listItem.attribs ?? {})));
});

test("unlinked top level nav groups inherit first child boundary", async () => {
  const doc = await loadDocument(path.join(TESTDATA, "epub/grouped-nav.epub"), "en", "zh");
  assert.deepEqual(doc.chapters.map((c) => c.title), ["PART I", "PART II"]);
  assert.deepEqual(
    doc.chapters[0].segments.map((s) => s.source),
    ["Section 1", "One."],
  );
  assert.deepEqual(
    doc.chapters[1].segments.map((s) => s.source),
    ["Section 2", "Two."],
  );
  const groupEntries = doc.meta.toc_entries.filter((e) => e.depth === 0);
  assert.ok(groupEntries.every((e) => "inherited_boundary_from" in e));
});

test("nav is canonical when epub also contains legacy ncx", async () => {
  const doc = await loadDocument(path.join(TESTDATA, "epub/nested-both.epub"), "en", "zh");
  assert.deepEqual(doc.chapters.map((c) => c.title), ["PART I", "PART II"]);
  assert.equal(doc.meta.toc_entries.length, 8);
  assert.equal(doc.meta.epub_split_toc_path, "OEBPS/nav.xhtml");
});

test("unresolved fragment is not used as a chapter boundary", async () => {
  const doc = await loadDocument(path.join(TESTDATA, "epub/nested-both-broken-fragment.epub"), "en", "zh");
  assert.deepEqual(doc.chapters.map((c) => c.title), ["PART I"]);
  const broken = doc.meta.toc_entries.find((e) => e.title === "PART II");
  assert.ok(!("segment_anchor" in broken));
  assert.ok(!("boundary_position" in broken));
});

test("logical chapter can span multiple spine resources", async () => {
  const doc = await loadDocument(path.join(TESTDATA, "epub/cross-resource.epub"), "en", "zh");
  assert.deepEqual(doc.chapters.map((c) => c.title), ["PART I", "PART II"]);
  assert.deepEqual(
    doc.chapters[0].segments.map((s) => s.source),
    ["PART I", "One.", "Section 1", "Two."],
  );
  assert.deepEqual(
    new Set(doc.chapters[0].segments.map((s) => s.resource_href)),
    new Set(["OEBPS/one.xhtml", "OEBPS/two.xhtml"]),
  );
  assert.deepEqual(
    doc.chapters[1].segments.map((s) => s.source),
    ["PART II", "Three."],
  );
});

test("nested fragment anchor survives template flattening", () => {
  const html = '<html><body><h2><span id="inside">Section</span></h2></body></html>';
  const [title, segments, template] = annotateEpubResource(html, 0, "body.xhtml");
  assert.equal(segments[0].source, "Section");
  assert.ok(template.includes('id="inside"'));
  assert.ok("epub_inline" in segments[0].meta);
});

test("nested toc splits only top level and keeps all anchors", async () => {
  const expected = [
    ["PART I", 0, "part-1"],
    ["Section 1", 1, "section-1"],
    ["PART II", 0, "part-2"],
    ["Section 2", 1, "section-2"],
  ];
  for (const tocKind of ["ncx", "nav"]) {
    const file = tocKind === "ncx" ? "nested-ncx.epub" : "nested-nav.epub";
    const doc = await loadDocument(path.join(TESTDATA, "epub", file), "en", "zh");
    assert.deepEqual(doc.chapters.map((c) => c.title), ["PART I", "PART II"]);
    assert.deepEqual(
      doc.chapters[0].segments.map((s) => s.source),
      ["PART I", "Part I intro.", "Section 1", "Section 1 body."],
    );
    assert.deepEqual(
      doc.chapters[1].segments.map((s) => s.source),
      ["PART II", "Part II intro.", "Section 2", "Section 2 body."],
    );
    assert.deepEqual(
      doc.meta.toc_entries.map((e) => [e.title, e.depth, e.fragment]),
      expected,
    );
    assert.deepEqual(
      new Set(doc.meta.toc_entries.map((e) => e.resource_href)),
      new Set(["OEBPS/body.xhtml"]),
    );
    assert.ok(
      doc.chapters.every((c) => c.segments.every((s) => s.resource_href === "OEBPS/body.xhtml")),
    );
  }
});

test("epub href resolution preserves raw href and plus", () => {
  const resolved = resolveEpubHref("OEBPS/nav/toc.xhtml", "../text/A+B%20C.xhtml#section%201");
  assert.equal(resolved.raw_href, "../text/A+B%20C.xhtml#section%201");
  assert.equal(resolved.resource_href, "OEBPS/text/A+B C.xhtml");
  assert.equal(resolved.fragment, "section 1");
  assert.equal(resolved.target_key, "OEBPS/text/A+B C.xhtml#section 1");
});

test("ruby reading is not included in translatable source", () => {
  const html = `<html><body>
<p><ruby>漢字<rp>（</rp><rt>かんじ</rt><rp>）</rp></ruby>です</p>
</body></html>`;
  const [title, segments, template] = annotateEpubResource(html, 0, "chapter.xhtml");
  assert.deepEqual(segments.map((s) => s.source), ["漢字です"]);
  assert.ok(template.includes("<rt>かんじ</rt>"));
  assert.ok(template.includes("<rp>（</rp>"));
});

test("table and definition list cells are extracted", () => {
  const html = `<html><body>
<table><tr><td>Cell A</td><td>Cell B</td></tr></table>
<dl><dt>Term</dt><dd>Definition</dd></dl>
</body></html>`;
  const [title, segments] = annotateEpubResource(html, 0, "chapter.xhtml");
  assert.deepEqual(segments.map((s) => s.source), ["Cell A", "Cell B", "Term", "Definition"]);
});

test("leaf div paragraphs are extracted without layout duplicates", () => {
  const html = `<html><body>
<div class="layout"><p>Nested paragraph.</p></div>
<div class="calibre8">First <i>div</i> paragraph.</div>
<div class="outer"><div class="calibre8">Second div paragraph.</div></div>
</body></html>`;
  const [title, segments, template] = annotateEpubResource(html, 0, "chapter.xhtml");
  assert.deepEqual(segments.map((s) => s.source), ["Nested paragraph.", "First div paragraph.", "Second div paragraph."]);
  assert.equal((template.match(/data-tn-id/g) ?? []).length, 3);
});

test("nested lists and blockquotes use leaf translation targets", () => {
  const html = `<html><body>
<ul><li><a href="#author">Author</a><ul>
<li><a href="chapter.xhtml#one">Chapter One</a></li>
<li><a href="chapter.xhtml#two">Chapter Two</a></li>
</ul></li></ul>
<blockquote><div>Dedication One</div><div>Dedication Two</div></blockquote>
</body></html>`;
  const [title, segments, template] = annotateEpubResource(html, 0, "contents.xhtml");
  assert.deepEqual(segments.map((s) => s.source), [
    "Author",
    "Chapter One",
    "Chapter Two",
    "Dedication One",
    "Dedication Two",
  ]);
  const rendered = parseHTML(template);
  assert.ok(findAllElements(rendered, (n) => n.name === "li").every((li) => !("data-tn-id" in (li.attribs ?? {}))));
  assert.ok(findAllElements(rendered, (n) => n.name === "blockquote").every((q) => !("data-tn-id" in (q.attribs ?? {}))));
  assert.equal(findAllElements(rendered, (n) => n.name === "a" && n.attribs?.["data-tn-id"] != null).length, 3);
  assert.equal(findAllElements(rendered, (n) => n.name === "div" && n.attribs?.["data-tn-id"] != null && n.parent?.name === "blockquote").length, 2);
  assert.ok(segments.slice(0, 3).every((s) => !("epub_annotations" in s.meta)));
});

test("declared legacy xhtml encoding is honored", async () => {
  const iconv = (await import("iconv-lite")).default;
  const markup = iconv.encode(
    '<?xml version="1.0" encoding="Shift_JIS"?><html><body><p>日本語</p></body></html>',
    "shift_jis",
  );
  const decoded = decodeMarkup(markup);
  assert.ok(decoded.includes("日本語"));
  assert.ok(!decoded.includes("\uFFFD"));
});

test("missing required opf attributes are reported or skipped", async () => {
  const d = await tmpDir();
  const p = path.join(d, "book.epub");
  const zip = new JSZip();
  zip.file("META-INF/container.xml", "<container><rootfiles><rootfile/></rootfiles></container>");
  await fs.writeFile(p, await zip.generateAsync({ type: "nodebuffer" }));
  const files = await openZip(p);
  assert.throws(() => findOpfPath(files), /full-path/);

  const opfPath = path.join(d, "opf.epub");
  const zip2 = new JSZip();
  zip2.file(
    "content.opf",
    `<package><manifest>
<item href="ignored.xhtml" media-type="application/xhtml+xml"/>
<item id="valid" href="valid.xhtml" media-type="application/xhtml+xml"/>
</manifest><spine><itemref/><itemref idref="valid"/></spine></package>`,
  );
  await fs.writeFile(opfPath, await zip2.generateAsync({ type: "nodebuffer" }));
  const files2 = await openZip(opfPath);
  const [title, hrefs, toc] = parseOpf(files2, "content.opf");
  assert.deepEqual(hrefs, ["valid.xhtml"]);
});

test("epub records inline nodes in segment meta", () => {
  const html = `<html><body>
<p class="Textbody"><img src="before.jpg"/>Avant<br/>Après<img src="after.jpg"/></p>
<p class="illustration"><img src="standalone.jpg"/></p>
</body></html>`;
  const [title, segments, template] = annotateEpubResource(html, 2, "chapter.xhtml");
  assert.deepEqual(segments.map((s) => s.source), ["Avant", "Après"]);
  const firstInline = segments[0].meta.epub_inline;
  const secondInline = segments[1].meta.epub_inline;
  assert.equal(firstInline.source_length, [...segments[0].source].length);
  assert.equal(secondInline.source_length, [...segments[1].source].length);
  assert.deepEqual(firstInline.nodes.map((n) => n.placement), ["before"]);
  assert.deepEqual(secondInline.nodes.map((n) => n.placement), ["after"]);
  assert.equal((template.match(/data-tn-inline-id/g) ?? []).length, 2);
  assert.equal((template.match(/data-tn-line/g) ?? []).length, 2);
  assert.ok(template.includes('<img src="standalone.jpg"/>'));
});

test("epub chapters and anchors", async () => {
  const doc = await loadDocument(path.join(TESTDATA, "epub/sample.epub"), "ja", "zh");
  assert.equal(doc.fmt, "epub");
  assert.equal(doc.chapters.length, 2);
  const ch1 = doc.chapters[0];
  assert.equal(ch1.title, "第一章　出会い");
  assert.equal(ch1.text_segments.length, 3); // h1 + 2 p
  assert.equal(ch1.template, null); // 模板不落盘
  for (const s of ch1.text_segments) {
    assert.ok(s.anchor != null);
    assert.ok(s.resource_href != null);
    assert.ok(!("epub_inline" in s.meta));
  }
  assert.ok(ch1.href != null);
});

test("epub ignores internal file title when no heading", async () => {
  const d = await tmpDir();
  const p = path.join(d, "novel.epub");
  const zip = new JSZip();
  zip.file("mimetype", "application/epub+zip");
  zip.file(
    "META-INF/container.xml",
    `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
<rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles>
</container>`,
  );
  zip.file(
    "OEBPS/content.opf",
    `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Book</dc:title></metadata>
<manifest><item id="cUH.xhtml" href="cUH.xhtml" media-type="application/xhtml+xml"/></manifest>
<spine><itemref idref="cUH.xhtml"/></spine>
</package>`,
  );
  zip.file(
    "OEBPS/cUH.xhtml",
    `<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>cUH</title></head><body><p>Body text.</p></body>
</html>`,
  );
  await fs.writeFile(p, await zip.generateAsync({ type: "nodebuffer" }));
  const doc = await loadDocument(p, "en", "zh");
  assert.equal(doc.chapters.length, 1);
  assert.equal(doc.chapters[0].title, "");
});

test("epub uses ncx toc label before repeated html title", async () => {
  const d = await tmpDir();
  const p = path.join(d, "novel.epub");
  const zip = new JSZip();
  zip.file("mimetype", "application/epub+zip");
  zip.file(
    "META-INF/container.xml",
    `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
<rootfiles><rootfile full-path="content.opf"/></rootfiles>
</container>`,
  );
  zip.file(
    "content.opf",
    `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Intermezzo</dc:title></metadata>
<manifest>
<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
<item id="ch1" href="index_split_004.html" media-type="application/xhtml+xml"/>
</manifest>
<spine toc="ncx"><itemref idref="ch1"/></spine>
</package>`,
  );
  zip.file(
    "toc.ncx",
    `<?xml version="1.0"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/">
<navMap><navPoint id="n1" playOrder="1">
<navLabel><text>Chapter 1</text></navLabel>
<content src="index_split_004.html"/>
</navPoint></navMap>
</ncx>`,
  );
  zip.file(
    "index_split_004.html",
    `<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Intermezzo</title></head><body><p>1</p><p>Body text.</p></body>
</html>`,
  );
  await fs.writeFile(p, await zip.generateAsync({ type: "nodebuffer" }));
  const doc = await loadDocument(p, "en", "zh");
  assert.equal(doc.chapters.length, 1);
  assert.equal(doc.chapters[0].title, "Chapter 1");
});

test("epub keeps toc entry for skipped title page", async () => {
  const d = await tmpDir();
  const p = path.join(d, "novel.epub");
  const zip = new JSZip();
  zip.file("mimetype", "application/epub+zip");
  zip.file(
    "META-INF/container.xml",
    `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
<rootfiles><rootfile full-path="content.opf"/></rootfiles>
</container>`,
  );
  zip.file(
    "content.opf",
    `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Book</dc:title></metadata>
<manifest>
<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
<item id="title" href="title.xhtml" media-type="application/xhtml+xml"/>
<item id="body" href="body.xhtml" media-type="application/xhtml+xml"/>
</manifest>
<spine toc="ncx"><itemref idref="title"/><itemref idref="body"/></spine>
</package>`,
  );
  zip.file(
    "toc.ncx",
    `<?xml version="1.0"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/">
<navMap><navPoint id="n1" playOrder="1">
<navLabel><text>第一章</text></navLabel><content src="title.xhtml"/>
</navPoint></navMap>
</ncx>`,
  );
  zip.file(
    "title.xhtml",
    `<html xmlns="http://www.w3.org/1999/xhtml"><body><img src="title.jpg"/></body></html>`,
  );
  zip.file(
    "body.xhtml",
    `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Body text.</p></body></html>`,
  );
  await fs.writeFile(p, await zip.generateAsync({ type: "nodebuffer" }));
  const doc = await loadDocument(p, "ja", "zh");
  assert.equal(doc.chapters.length, 1);
  assert.equal(doc.chapters[0].href, "body.xhtml");
  assert.equal(doc.chapters[0].title, "第一章");
  assert.ok(
    doc.meta.toc_entries.some(
      (e) => e.resource_href === "title.xhtml" && e.title === "第一章",
    ),
  );
});
