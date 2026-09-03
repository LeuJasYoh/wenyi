// PDF 读取（主规格 §7.5 / 分册 03 §1.10）：MinerU → HTML 缓存 → read_html。
import { promises as fs } from "node:fs";
import path from "node:path";
import { MinerUError } from "./errors.js";
import { convertPdfToHtml } from "./pdf-to-html.js";
import { readHtml } from "./html-reader.js";

export async function readPdf(filePath, sourceLang, targetLang, { cacheDir, apiToken = null, convert = convertPdfToHtml } = {}) {
  await fs.mkdir(cacheDir, { recursive: true });
  const htmlPath = path.join(cacheDir, "converted.html");
  if (!(await fs.stat(htmlPath).catch(() => null))) {
    try {
      await convert(filePath, htmlPath, { apiToken });
    } catch (err) {
      if (err instanceof MinerUError) throw err;
      const wrapped = new MinerUError(`PDF 转换失败：${err?.message ?? err}`);
      wrapped.cause = err;
      throw wrapped;
    }
  }
  const doc = await readHtml(htmlPath, sourceLang, targetLang);
  doc.title = path.basename(filePath).replace(/\.[^.]*$/, "");
  doc.fmt = "pdf";
  doc.source_path = path.resolve(filePath);
  doc.meta = {
    ...doc.meta,
    pdf_path: path.resolve(filePath),
    converted_html_path: path.resolve(htmlPath),
  };
  return doc;
}
