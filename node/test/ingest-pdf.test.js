// 迁移自 tests/test_pdf_support.py::TestPdfIngest（阶段 1 相关部分：
// 缓存复用 + 外部转换错误包装 + MinerU 纯函数辅助；其余用例归阶段 3/5）。
import { test } from "node:test";
import assert from "node:assert/strict";
import { promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import JSZip from "jszip";
import { loadDocument } from "../src/doc/segmenter.js";
import { readPdf } from "../src/doc/pdf-reader.js";
import { MinerUError } from "../src/doc/errors.js";
import { htmlFromZip, assembleHtml, bodyText, cleanHead } from "../src/doc/pdf-to-html.js";

const _HTML = `<!doctype html>
<html>
<head><meta charset="utf-8"><title>Sample</title></head>
<body>
<h1>Chapter One</h1><p>First paragraph.</p>
<h2>Chapter Two</h2><p>Second paragraph.</p>
</body>
</html>
`;

async function tmpDir() {
  return fs.mkdtemp(path.join(os.tmpdir(), "wenyi-pdf-"));
}

test("pdf reuses state html without api call", async () => {
  const d = await tmpDir();
  const pdfPath = path.join(d, "sample.pdf");
  await fs.writeFile(pdfPath, Buffer.from("not accessed when cached HTML exists"));
  const cacheDir = path.join(d, "state", "sample", "source");
  await fs.mkdir(cacheDir, { recursive: true });
  const cachedHtml = path.join(cacheDir, "converted.html");
  await fs.writeFile(cachedHtml, _HTML, "utf-8");
  const document = await loadDocument(pdfPath, "en", "zh", 0, { cacheDir });
  assert.equal(document.title, "sample");
  assert.equal(document.fmt, "pdf");
  assert.equal(document.source_path, path.resolve(pdfPath));
  assert.equal(document.meta.converted_html_path, path.resolve(cachedHtml));
  assert.deepEqual(document.chapters.map((c) => c.title), ["Chapter One", "Chapter Two"]);
  assert.ok(document.chapters.every((c) => c.template));
});

test("pdf wraps external conversion errors", async () => {
  const d = await tmpDir();
  const pdfPath = path.join(d, "sample.pdf");
  await fs.writeFile(pdfPath, Buffer.from("invalid PDF is not read because conversion is mocked"));
  const cacheDir = path.join(d, "state", "sample", "source");
  const convert = () => {
    throw new Error("connection reset");
  };
  await assert.rejects(
    () => readPdf(pdfPath, "en", "zh", { cacheDir, convert }),
    (err) => {
      assert.ok(err instanceof MinerUError);
      assert.match(err.message, /PDF 转换失败/);
      assert.equal(err.cause?.message, "connection reset");
      return true;
    },
  );
});

// ---- MinerU 纯函数辅助 ----

test("htmlFromZip takes first html member", async () => {
  const zip = new JSZip();
  zip.file("a.txt", "x");
  zip.file("out/index.htm", "<html><body>A</body></html>");
  zip.file("out/b.html", "<html><body>B</body></html>");
  const html = await htmlFromZip(await zip.generateAsync({ type: "nodebuffer" }));
  assert.equal(html, "<html><body>A</body></html>");
});

test("assembleHtml keeps first head and joins bodies", () => {
  const parts = [
    '<html><head><title>T</title><meta charset="utf-8"><link rel="s" href="x.css"></head><body>A</body></html>',
    '<html><head><title>U</title></head><body>B</body></html>',
  ];
  const assembled = assembleHtml(parts, "book.pdf");
  assert.equal(
    assembled,
    '<!DOCTYPE html><html><head><meta charset="utf-8"><title>book.pdf</title><link rel="s" href="x.css"></head><body>AB</body></html>',
  );
  assert.equal(bodyText("<html><body>X</body></html>"), "X");
  assert.equal(cleanHead("<html><head><title>t</title><meta charset=\"x\"><style/></head><body></body></html>"), "<style/>");
});
