// 切分算法与文档加载分发（主规格 §7.1 / 分册 03 §1.12）。
import path from "node:path";
import { KIND_TEXT, Segment } from "./models.js";
import { rlen, cpSlice } from "./dom.js";
import { readEpub } from "./epub-reader.js";
import { readText } from "./text-reader.js";
import { readFb2 } from "./fb2-reader.js";
import { readHtml } from "./html-reader.js";
import { readPdf } from "./pdf-reader.js";

// 句末标点 lookbehind（含换行）
const _SENT_SPLIT = /(?<=[。．.!！？!?…\n])/u;

function _lastIndexOfCP(cps, ch, endBound) {
  // rest.rfind(ch, 0, endBound) 的码点等价：在 cps[0..endBound) 中找最后一个 ch
  for (let i = Math.min(endBound, cps.length) - 1; i >= 0; i--) {
    if (cps[i] === ch) return i;
  }
  return -1;
}

/** 兜底拆分单个超长句（优先不拆英文单词：空格 → Tab → 换行 → 硬切）。 */
export function splitOversizedSentence(text, maxChars) {
  const chunks = [];
  let rest = Array.from(text);
  while (rest.length > maxChars) {
    let cut = _lastIndexOfCP(rest, " ", maxChars + 1);
    if (cut <= 0) cut = _lastIndexOfCP(rest, "\t", maxChars + 1);
    if (cut <= 0) cut = _lastIndexOfCP(rest, "\n", maxChars + 1);
    if (cut <= 0) cut = maxChars;
    chunks.push(rest.slice(0, cut).join(""));
    rest = rest.slice(cut);
  }
  if (rest.length > 0) chunks.push(rest.join(""));
  return chunks;
}

/** 按句末标点贪心打包；空文本保底返回原文。 */
export function splitText(text, maxChars) {
  const chunks = [];
  let cur = "";
  for (const p of text.split(_SENT_SPLIT)) {
    if (p === "") continue;
    if (rlen(p) > maxChars) {
      if (cur !== "") {
        chunks.push(cur);
        cur = "";
      }
      chunks.push(...splitOversizedSentence(p, maxChars));
      continue;
    }
    if (cur !== "" && rlen(cur) + rlen(p) > maxChars) {
      chunks.push(cur);
      cur = "";
    }
    cur += p;
  }
  if (cur !== "") chunks.push(cur);
  return chunks.length > 0 ? chunks : [text];
}

/** 就地把各章超长 Segment 拆段：首段保留 anchor/meta，续段 cont=true 无 anchor。 */
export function splitLongSegments(chapters, maxChars) {
  if (maxChars <= 0) return;
  for (const chapter of chapters) {
    const newSegs = [];
    for (const seg of chapter.segments) {
      if (rlen(seg.source) <= maxChars) {
        seg.index = newSegs.length;
        newSegs.push(seg);
        continue;
      }
      const pieces = splitText(seg.source, maxChars);
      pieces.forEach((piece, i) => {
        if (i === 0) {
          newSegs.push(
            new Segment({
              index: newSegs.length,
              source: piece,
              kind: seg.kind,
              anchor: seg.anchor,
              resource_href: seg.resource_href,
              cont: false,
              meta: structuredClone(seg.meta ?? {}),
            }),
          );
        } else {
          newSegs.push(
            new Segment({
              index: newSegs.length,
              source: piece,
              kind: KIND_TEXT,
              anchor: null,
              resource_href: seg.resource_href,
              cont: true,
              meta: {},
            }),
          );
        }
      });
    }
    chapter.segments = newSegs;
  }
}

/** 按扩展名分发（PDF 必须给 cache_dir）。 */
export async function loadDocument(filePath, sourceLang, targetLang, splitSegments = 0, { cacheDir = null } = {}) {
  const ext = path.extname(filePath).toLowerCase();
  let doc;
  if (ext === ".epub") {
    doc = await readEpub(filePath, sourceLang, targetLang);
  } else if (ext === ".md" || ext === ".markdown" || ext === ".txt" || ext === ".text") {
    doc = await readText(filePath, sourceLang, targetLang);
  } else if (ext === ".fb2") {
    doc = await readFb2(filePath, sourceLang, targetLang);
  } else if (ext === ".html" || ext === ".htm" || ext === ".xhtml") {
    doc = await readHtml(filePath, sourceLang, targetLang);
  } else if (ext === ".pdf") {
    if (cacheDir == null) throw new Error("PDF 读取需要指定运行状态缓存目录");
    doc = await readPdf(filePath, sourceLang, targetLang, { cacheDir });
  } else {
    throw new Error(`不支持的格式：${ext}（支持 .epub / .txt / .md / .fb2 / .html / .xhtml / .pdf）`);
  }
  if (splitSegments > 0) splitLongSegments(doc.chapters, splitSegments);
  return doc;
}

/** 贪心按字符预算分批；单个超长段自成一批。 */
export function batchSegments(segments, maxChars) {
  const batches = [];
  let cur = [];
  let curLen = 0;
  for (const seg of segments) {
    const slen = rlen(seg.source);
    if (cur.length > 0 && curLen + slen > maxChars) {
      batches.push(cur);
      cur = [];
      curLen = 0;
    }
    cur.push(seg);
    curLen += slen;
  }
  if (cur.length > 0) batches.push(cur);
  return batches;
}

export function chapterBatches(chapter, maxChars) {
  return batchSegments(chapter.text_segments, maxChars);
}
