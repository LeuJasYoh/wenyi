// 组装层共用常量与工具（主规格 §13.1 / 分册 08 §1.2–1.3）。
import { promises as fs } from "node:fs";
import path from "node:path";
import JSZip from "jszip";
import { KIND_TEXT } from "./models.js";

export const _ILLEGAL_FN = /[\\/:*?"<>|\r\n\t]+/g;
export const _HTML_EXTS = [".xhtml", ".html", ".htm"];
// 格式 → 扩展名；同时是 assemble 的格式白名单
export const _OUT_EXT = { epub: ".epub", txt: ".txt", html: ".html", markdown: ".md", pdf: ".pdf" };
export const _HORIZONTAL_OVERRIDE_ID = "trans-novel-horizontal-override";
export const _BILINGUAL_STYLE_ID = "tn-bilingual-style";
export const _SOURCE_ANCHOR_PREFIX = "tn-source-";
export const _INLINE_META_KEY = "epub_inline";
export const _INLINE_ID_ATTR = "data-tn-inline-id";
export const _ANNOTATION_META_KEY = "epub_annotations";
export const _ANNOTATION_ID_ATTR = "data-tn-annotation-id";
export const _LINE_WRAPPER_ATTR = "data-tn-line";

export const _IMAGE_EXTENSION_BY_TYPE = new Map([
  ["image/gif", ".gif"],
  ["image/jpeg", ".jpg"],
  ["image/png", ".png"],
  ["image/svg+xml", ".svg"],
  ["image/webp", ".webp"],
]);

/** 双语跨资源链接映射键：(resource_href, fragment) → source_id。 */
export function anchorLinkKey(resourceHref, fragment) {
  return `${resourceHref ?? ""}\u0000${fragment ?? ""}`;
}

export function isPlainObject(v) {
  return v != null && typeof v === "object" && !Array.isArray(v);
}

/** Python round 语义（四舍五入偶数：round(0.5)=0、round(1.5)=2）。 */
export function pyRound(x) {
  const floor = Math.floor(x);
  const diff = x - floor;
  if (diff < 0.5) return floor;
  if (diff > 0.5) return floor + 1;
  return floor % 2 === 0 ? floor : floor + 1;
}

export function escapeHtmlText(s) {
  return String(s ?? "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

export function escapeHtmlAttr(s) {
  return escapeHtmlText(s).replace(/"/g, "&quot;");
}

// ---- 文件名与输出路径 ----

/** 非法字符→空格、去首尾空白与点、压空白、截 120 字符、空回退 fallback。 */
export function sanitizeFilename(name, fallback = "translated") {
  let s = String(name ?? "").replace(_ILLEGAL_FN, " ");
  s = s.replace(/^[\s.]+|[\s.]+$/g, "");
  s = s.replace(/\s+/g, " ");
  s = Array.from(s).slice(0, 120).join("");
  return s === "" ? fallback : s;
}

export async function ensureParentDir(filePath) {
  await fs.mkdir(path.dirname(path.resolve(filePath)), { recursive: true });
}

/** 默认导出路径：源文件旁 output/；有书名用 sanitize(title)，否则 basename + .zh[-bi]。 */
export function defaultOut(sourcePath, outFormat, title = null, { bilingual = false } = {}) {
  const ext = _OUT_EXT[outFormat] ?? `.${outFormat}`;
  const outDir = path.join(path.dirname(path.resolve(sourcePath)), "output");
  let filename;
  if (title) {
    filename = `${sanitizeFilename(title)}${ext}`;
  } else {
    const stem = path.basename(sourcePath, path.extname(sourcePath));
    const suffix = bilingual ? ".zh-bi" : ".zh";
    filename = `${stem}${suffix}${ext}`;
  }
  return path.join(outDir, filename);
}

/** 调用方显式指定 out_path 时派生双语路径：stem 追加 "-bi"。 */
export function bilingualOutPath(outPath) {
  const ext = path.extname(outPath);
  const base = ext ? outPath.slice(0, outPath.length - ext.length) : outPath;
  return `${base}-bi${ext}`;
}

/** zip 内路径的 basename（去 fragment）。 */
export function baseNoFrag(href) {
  return path.posix.basename(String(href ?? "").split("#", 1)[0]);
}

// ---- 文本抽取 ----

/** 章节展示标题：(title_translated or title or "").strip()。 */
export function chTitleFromEntry(c) {
  if (!isPlainObject(c)) return "";
  const t = c.title_translated || c.title || "";
  return typeof t === "string" ? t.trim() : "";
}

/** 导出书名：<base>-wenyi-<lang>[-bi]（已含后缀防重复）。 */
export function exportBookTitle(title, targetLang, { bilingual = false } = {}) {
  const base = title || "translated";
  const lang = String(targetLang ?? "").replace(/_/g, "-").toLowerCase() || "zh";
  const suffix = `-wenyi-${lang}${bilingual ? "-bi" : ""}`;
  return base.endsWith(suffix) ? base : `${base}${suffix}`;
}

/** 有效译文优先，空译文回退源文。 */
export function segText(seg) {
  return seg.target && seg.target.trim() ? seg.target : seg.source;
}

/** ""/zh/zh-cn/zh-hans/cn → "zh-Hans"；否则原样（None → zh-Hans）。 */
export function epubLang(lang) {
  const normalized = String(lang ?? "").replace(/_/g, "-").toLowerCase();
  if (["", "zh", "zh-cn", "zh-hans", "cn"].includes(normalized)) return "zh-Hans";
  return lang || "zh-Hans";
}

/** cont 续段并入上一段；跳过 source 为空的段。返回 [kind, 译文拼接, 原文拼接]。 */
export function mergedParagraphs(chapter) {
  const rows = [];
  let current = null;
  for (const seg of chapter.segments ?? []) {
    if (!seg.source) continue;
    const text = segText(seg);
    if (seg.cont && current != null) {
      current[1] += text;
      current[2] += seg.source;
    } else {
      current = [seg.kind ?? KIND_TEXT, text, seg.source];
      rows.push(current);
    }
  }
  return rows;
}

/** 双语原文去重：source 空或 ==target → ""。 */
export function bilingualSource(source, target) {
  const src = String(source ?? "");
  if (src.trim() === "" || src === target) return "";
  return src;
}

// ---- zip 原子写 ----

/**
 * 按给定顺序写 zip（mimetype 恒 STORED），tmp + rename 原子替换；失败清理 tmp。
 * entries: [{name, data: string|Buffer, store?: boolean}]
 */
export async function writeZipAtomic(outPath, entries, { rename = fs.rename } = {}) {
  await ensureParentDir(outPath);
  const zip = new JSZip();
  for (const e of entries) {
    zip.file(e.name, e.data, e.store ? { compression: "STORE", createFolders: false } : { createFolders: false });
  }
  const buf = await zip.generateAsync({ type: "nodebuffer", compression: "DEFLATE" });
  const tmp = `${outPath}.tmp`;
  await fs.writeFile(tmp, buf);
  try {
    await rename(tmp, outPath);
  } catch (err) {
    await fs.unlink(tmp).catch(() => {});
    throw err;
  }
}
