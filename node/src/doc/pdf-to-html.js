// MinerU PDF→HTML 转换（主规格 §7.5 / 分册 03 §1.11）。
import { promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import JSZip from "jszip";
import { PDFDocument } from "pdf-lib";
import { MinerUError, MinerUTimeoutError } from "./errors.js";

export const API_BASE = "https://mineru.net/api/v4";
export const MAX_PAGES = 200;
export const POLL_INTERVAL = 3.0; // 秒
export const MAX_WAIT_PER_TASK = 600; // 秒

function check(body) {
  if (body?.code !== 0) {
    throw new MinerUError(`API error: code=${body?.code} msg=${body?.msg}`);
  }
  return body;
}

/** fetch 可注入（测试用 MockTransport 等价）。 */
export class MinerUApi {
  constructor(token, { fetchImpl = globalThis.fetch } = {}) {
    this.token = token;
    this.fetchImpl = fetchImpl;
  }

  async submitBatch(filePaths, timeout = MAX_WAIT_PER_TASK) {
    const filesMeta = filePaths.map((p) => ({ name: path.basename(p) }));
    const resp = await this.fetchImpl(`${API_BASE}/file-urls/batch`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${this.token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ files: filesMeta, model_version: "vlm", extra_formats: ["html"] }),
    });
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    const data = check(await resp.json()).data;
    // 上传：不带 Content-Type（OSS 签名要求）
    for (let i = 0; i < filePaths.length; i++) {
      const uploadUrl = data.file_urls[i];
      const content = await fs.readFile(filePaths[i]);
      const up = await this.fetchImpl(uploadUrl, {
        method: "PUT",
        body: content,
        timeout: 120000,
      });
      if (!up.ok) throw new Error(`HTTP ${up.status}`);
    }
    return this._pollBatch(data.batch_id, timeout);
  }

  async _pollBatch(batchId, timeout) {
    const deadline = Date.now() + timeout * 1000;
    let interval = POLL_INTERVAL;
    for (;;) {
      const resp = await this.fetchImpl(`${API_BASE}/extract-results/batch/${batchId}`, {
        headers: { Authorization: `Bearer ${this.token}` },
      });
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const results = (await resp.json()).data.extract_result;
      const states = results.map((r) => r.state);
      if (states.every((s) => s === "done" || s === "failed")) {
        for (const r of results) {
          if (r.state === "failed") {
            throw new MinerUError(`Extraction failed for ${r.file_name}: ${r.err_msg ?? "unknown"}`);
          }
        }
        const zips = [];
        for (const r of results) zips.push(await this._downloadZip(r.full_zip_url));
        return zips;
      }
      if (Date.now() > deadline) throw new MinerUTimeoutError(`Batch ${batchId} timed out`);
      await new Promise((resolve) => setTimeout(resolve, Math.min(interval, Math.max(0, (deadline - Date.now()) / 1000)) * 1000));
      interval = Math.min(interval * 1.5, 30.0);
    }
  }

  async _downloadZip(zipUrl) {
    const resp = await this.fetchImpl(zipUrl, { redirect: "follow" });
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    return Buffer.from(await resp.arrayBuffer());
  }
}

export async function pageCount(pdfPath) {
  const bytes = await fs.readFile(pdfPath);
  const doc = await PDFDocument.load(bytes, { ignoreEncryption: true });
  return doc.getPageCount();
}

/** >200 页拆块（临时文件，调用方负责删除）。 */
export async function splitPdf(pdfPath, maxPages = MAX_PAGES) {
  const bytes = await fs.readFile(pdfPath);
  const src = await PDFDocument.load(bytes, { ignoreEncryption: true });
  const total = src.getPageCount();
  const parts = [];
  for (let start = 0; start < total; start += maxPages) {
    const end = Math.min(start + maxPages, total);
    const out = await PDFDocument.create();
    const pages = await out.copyPages(src, Array.from({ length: end - start }, (_, i) => start + i));
    for (const p of pages) out.addPage(p);
    const tmp = path.join(os.tmpdir(), `chunk_p${start + 1}-${end}_${process.pid}-${Date.now()}.pdf`);
    await fs.writeFile(tmp, await out.save());
    parts.push(tmp);
  }
  return parts;
}

// ---- HTML 辅助 ----

export async function htmlFromZip(zipBytes) {
  const zip = await JSZip.loadAsync(zipBytes);
  const name = Object.keys(zip.files).find((n) => /\.html?$/i.test(n));
  if (!name) throw new MinerUError("No HTML file in result ZIP");
  return zip.files[name].async("string");
}

export function bodyText(html) {
  const m = html.match(/<body[^>]*>([\s\S]*?)<\/body>/i);
  return m ? m[1] : html;
}

export function cleanHead(html) {
  const m = html.match(/<head[^>]*>([\s\S]*?)<\/head>/i);
  if (!m) return "";
  return m[1]
    .replace(/<title[^>]*>[\s\S]*?<\/title>/gi, "")
    .replace(/<meta[^>]+charset[^>]*>/gi, "")
    .trim();
}

export function assembleHtml(parts, title) {
  const head = cleanHead(parts[0]);
  const bodies = parts.map(bodyText);
  return `<!DOCTYPE html><html><head><meta charset="utf-8"><title>${title}</title>${head}</head><body>${bodies.join("")}</body></html>`;
}

/** 把 PDF 转成 HTML（MinerU Precision API v4）。 */
export async function convertPdfToHtml(pdfPath, outputPath = null, { apiToken = null, onProgress = null, fetchImpl = globalThis.fetch } = {}) {
  const msg = onProgress ?? (() => {});
  const resolved = path.resolve(pdfPath);
  const stat = await fs.stat(resolved).catch(() => null);
  if (!stat || !stat.isFile()) {
    throw new Error(`ENOENT: no such file, open '${resolved}'`);
  }
  const out = outputPath ? path.resolve(outputPath) : resolved.replace(/\.[^.]*$/, "") + ".html";
  const token = apiToken ?? process.env.MINERU_API_KEY ?? null;
  if (!token) throw new MinerUError("API token not provided and MINERU_API_KEY not set");

  const total = await pageCount(resolved);
  msg(`PDF: ${total} pages`);
  let chunkPaths = [resolved];
  let owned = [];
  if (total > MAX_PAGES) {
    chunkPaths = await splitPdf(resolved, MAX_PAGES);
    owned = chunkPaths;
    for (let i = 0; i < chunkPaths.length; i++) {
      const start = i * MAX_PAGES + 1;
      const end = Math.min((i + 1) * MAX_PAGES, total);
      const n = await pageCount(chunkPaths[i]);
      msg(`chunk ${start}-${end}: ${n} pages`);
    }
  }
  const api = new MinerUApi(token, { fetchImpl });
  let html;
  try {
    const zipBytesList = await api.submitBatch(chunkPaths);
    const htmlParts = [];
    for (const zb of zipBytesList) htmlParts.push(await htmlFromZip(zb));
    msg(`converted ${htmlParts.length} chunk(s)`);
    html = htmlParts.length === 1 ? htmlParts[0] : assembleHtml(htmlParts, path.basename(resolved));
  } finally {
    for (const cp of owned) await fs.rm(cp, { force: true });
  }
  await fs.writeFile(out, html, "utf-8");
  msg(`Done → ${out} (${html.length.toLocaleString("en-US")} chars)`);
  return out;
}
