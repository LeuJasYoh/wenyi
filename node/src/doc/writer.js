// writer.js 输出组装核心（主规格 §13）：TXT/Markdown/EPUB 回填/双语/HTML。
import path from "node:path";
import { promises as fs } from "node:fs";
import { createHash } from "node:crypto";
import JSZip from "jszip";
import {
  parseHTML, findAllElements, directChildren, isTag, isText, serializeNode, serializeDocument,
  createElement, extract, ancestors, getText,
  BLOCK_TAGS, HEADING_TAGS,
} from "./dom.js";
import { openZip, annotateEpubResource, fragmentAnchorMap } from "./epub-reader.js";
import { readFb2Binaries } from "./fb2-reader.js";
import { navTocScopes, navRootList, resolveEpubHref, posixBasename, unquote } from "./epub-toc.js";
import {
  sanitizeFilename, defaultOut, bilingualOutPath, chTitleFromEntry, exportBookTitle,
  segText, epubLang, mergedParagraphs, bilingualSource, writeZipAtomic, ensureParentDir,
  pyRound, escapeHtmlText, escapeHtmlAttr,
  _SOURCE_ANCHOR_PREFIX, _INLINE_ID_ATTR, _ANNOTATION_META_KEY, _ANNOTATION_ID_ATTR,
  _LINE_WRAPPER_ATTR, _BILINGUAL_STYLE_ID, _HORIZONTAL_OVERRIDE_ID, _HTML_EXTS,
} from "./writer-utils.js";

export const BILINGUAL_CSS = `.tn-source{opacity:.55;font-size:.85em;color:#666;line-height:1.4}
.tn-source[data-tn-source-block]{display:block;margin:.35em 0}
@media (prefers-color-scheme: dark){.tn-source{color:#9a9a9a;opacity:.7}}
.ibooks-dark-theme-use-custom-text-color .tn-source{color:#9a9a9a}`;

function sha256Of(s) {
  return createHash("sha256").update(s, "utf-8").digest("hex");
}

// ---- 状态读取 ----

async function readState(stateDir) {
  const manifest = JSON.parse(await fs.readFile(path.join(stateDir, "manifest.json"), "utf-8"));
  const chapters = [];
  for (const info of manifest.chapters ?? []) {
    chapters.push(JSON.parse(await fs.readFile(path.join(stateDir, "chapters", `ch${info.index}.json`), "utf-8")));
  }
  return { manifest, chapters };
}

// ---- TXT / Markdown ----

function assembleTextRows(doc, { bilingual = false } = {}) {
  const parts = [];
  for (const chapter of doc.chapters) {
    for (const [kind, text, source] of mergedParagraphs(chapter)) {
      const src = bilingual ? bilingualSource(source, text) : "";
      if (bilingual && kind !== "heading" && src) {
        if (doc.bilingual_order === "source_first") parts.push(src, text);
        else parts.push(text, src);
      } else {
        parts.push(text);
      }
    }
    parts.push("");
  }
  return parts.join("\n\n");
}

function assembleMarkdown(doc) {
  const parts = [];
  for (const chapter of doc.chapters) {
    let level = chapter.meta?.heading_level;
    if (!(Number.isInteger(level) && level >= 1 && level <= 6)) level = 1;
    for (const [kind, text, source] of mergedParagraphs(chapter)) {
      if (kind === "heading") {
        parts.push("#".repeat(level) + " " + text);
      } else {
        parts.push(text);
        const src = bilingualSource(source, text);
        if (doc.bilingual_source_enabled && src) parts.push(src);
      }
    }
    parts.push("");
  }
  return parts.join("\n\n");
}

// ---- 段级回填：内联节点 + 注释链接 ----

function findDescendantByAttr(root, attr, value) {
  return findAllElements(root, (n) => n.attribs?.[attr] === value)[0] ?? null;
}

function detach(node) {
  if (!node) return node;
  const parent = node.parent;
  if (!parent) return node;
  const idx = parent.children.indexOf(node);
  if (idx >= 0) parent.children.splice(idx, 1);
  if (node.prev) node.prev.next = node.next;
  if (node.next) node.next.prev = node.prev;
  node.prev = node.next = null;
  return node;
}

function shallowClone(el) {
  const clone = createElement(el.name, { ...el.attribs });
  delete clone.attribs[_ANNOTATION_ID_ATTR];
  clone.children = [];
  return clone;
}

const DEGRADED_STATUSES = new Set(["fallback", "failed", "invalid", "missing", "stale"]);

/** 收集内联节点与注释恢复事件，然后按偏移重建块内容。 */
function replaceBlockContent(el, seg, target, source) {
  const meta = seg?.meta ?? {};
  const targetChars = [...target];
  const events = [];
  // 内联节点：比例映射偏移
  const inline = meta.epub_inline;
  if (inline && Array.isArray(inline.nodes)) {
    const sourceLen = Number.isInteger(inline.source_length) ? inline.source_length : 0;
    for (const node of inline.nodes) {
      const nodeEl = findDescendantByAttr(el, _INLINE_ID_ATTR, node.id);
      if (!nodeEl) continue;
      extract(nodeEl);
      const offset = sourceLen > 0
        ? Math.min(Math.max(pyRound((node.offset * targetChars.length) / sourceLen), 0), targetChars.length)
        : targetChars.length;
      events.push({ offset, type: "inline", node: nodeEl });
    }
  }
  // 注释链接：digest 校验 + placement 校验
  const annotations = meta[_ANNOTATION_META_KEY];
  if (annotations && Array.isArray(annotations.items)) {
    const digestOK = annotations.target_digest === sha256Of(target);
    const acceptedRanges = [];
    for (const item of annotations.items) {
      const root = findDescendantByAttr(el, _ANNOTATION_ID_ATTR, item.id);
      if (!root) continue;
      let placement = null;
      if (digestOK && Array.isArray(annotations.placements)) {
        placement = annotations.placements.find((p) => p && p.id === item.id) ?? null;
      }
      let usable = false;
      if (placement) {
        const statusBad = DEGRADED_STATUSES.has(placement.status) || DEGRADED_STATUSES.has(placement.method ?? "");
        if (!statusBad && Number.isInteger(placement.target_start) && Number.isInteger(placement.target_end)) {
          if (item.mode === "point") {
            usable = placement.target_start === placement.target_end &&
              !acceptedRanges.some(([s, e]) => placement.target_start > s && placement.target_start < e);
          } else {
            usable = placement.target_start < placement.target_end &&
              !acceptedRanges.some(([s, e]) => placement.target_start < e && placement.target_end > s);
          }
          if (usable && item.mode === "range") {
            acceptedRanges.push([placement.target_start, placement.target_end]);
          }
        }
      }
      if (usable) {
        if (item.mode === "point") {
          events.push({ offset: placement.target_start, type: "point", item, root });
        } else {
          const marker = (root.children ?? []).filter((c) => c.type === "tag" && (c.name === "sup" || c.name === "sub")).pop() ?? null;
          events.push({ offset: placement.target_start, type: "rangeStart", item, root, marker });
          events.push({ offset: placement.target_end, type: "rangeEnd", item, root });
        }
      } else {
        events.push({ offset: targetChars.length, type: "fallback", item, root });
      }
    }
  }
  // 排序：offset 升序；同偏移 rangeEnd → rangeStart → point → inline
  const weight = { rangeEnd: 0, rangeStart: 1, point: 2, inline: 3, fallback: 4 };
  events.sort((a, b) => a.offset - b.offset || weight[a.type] - weight[b.type]);
  // 先把注释根/内联节点从原树摘除（清空 el.children 前保住子树）
  for (const ev of events) {
    if (ev.type === "inline") {
      if (ev.node) detach(ev.node);
    } else if (ev.type === "point" || ev.type === "fallback" || ev.type === "rangeStart") {
      if (ev.marker) detach(ev.marker);
      if (ev.root) detach(ev.root);
    }
  }
  // 栈式重建
  el.children = [];
  const stack = [{ container: el }];
  let cursor = 0;
  const append = (piece) => {
    const top = stack[stack.length - 1].container;
    piece.parent = top;
    if (top.children.length > 0) {
      const prev = top.children[top.children.length - 1];
      piece.prev = prev;
      prev.next = piece;
    }
    top.children.push(piece);
  };
  const flushText = (end) => {
    end = Math.min(end, targetChars.length);
    if (end > cursor) {
      append({ type: "text", data: targetChars.slice(cursor, end).join("") });
      cursor = end;
    }
  };
  for (const ev of events) {
    flushText(ev.offset);
    if (ev.type === "inline") {
      append(ev.node);
    } else if (ev.type === "point") {
      // point：原根子树整体保留（含内部 <a> 链接与标记文字）
      detach(ev.root);
      append(ev.root);
    } else if (ev.type === "rangeStart") {
      const shell = shallowClone(ev.root);
      append(shell);
      stack.push({ container: shell, id: ev.item.id, marker: ev.marker ?? null });
    } else if (ev.type === "rangeEnd") {
      const idx = stack.findIndex((s) => s.id === ev.item.id);
      if (idx > 0) {
        const frame = stack.splice(idx, 1)[0];
        if (frame.marker) {
          detach(frame.marker);
          frame.marker.parent = frame.container;
          frame.container.children.push(frame.marker);
          if (frame.container.children.length > 1) {
            const prev = frame.container.children[frame.container.children.length - 2];
            frame.marker.prev = prev;
            prev.next = frame.marker;
          }
        }
      }
    } else if (ev.type === "fallback") {
      append(fallbackAnnotationNode(ev.root, ev.item));
    }
  }
  flushText(targetChars.length);
}

function fallbackAnnotationNode(root, item) {
  if (item.mode === "point") {
    detach(root);
    return root;
  }
  const shell = shallowClone(root);
  // range 降级：段末可点击 sup 标记
  const marker = createElement("sup", {});
  const textNode = { type: "text", data: item.marker_text || "↩", parent: marker };
  marker.children = [textNode];
  shell.children = [marker];
  marker.parent = shell;
  return shell;
}

// ---- 双语源块 ----

function appendSourceBlock(el, target, source, { order, preserveSourceStyle, sourceAnchorMap, anchor }) {
  const src = bilingualSource(source, target);
  if (!src) return;
  let sourceID;
  if (sourceAnchorMap && anchor) {
    sourceID = sourceAnchorMap.get(`${anchor}|${src}`) ?? null;
  }
  const attrs = { class: preserveSourceStyle ? undefined : "tn-source" };
  if (!preserveSourceStyle) attrs["data-tn-source-block"] = "true";
  if (sourceID) attrs.id = sourceID;
  Object.keys(attrs).forEach((k) => attrs[k] === undefined && delete attrs[k]);
  const block = createElement("span", attrs);
  block.children = [{ type: "text", data: src, parent: block }];
  const insert = () => {
    if (order === "source_first") {
      el.parent && insertBefore(el, block);
    } else {
      insertAfterNode(el, block);
    }
  };
  insert();
}

function insertAfterNode(ref, node) {
  const parent = ref.parent;
  if (!parent) return;
  const idx = parent.children.indexOf(ref);
  parent.children.splice(idx + 1, 0, node);
  node.parent = parent;
  node.prev = ref;
  node.next = ref.next;
  if (ref.next) ref.next.prev = node;
  ref.next = node;
}

function cleanupRendered(doc) {
  for (const el of [...findAllElements(doc, (n) => n.attribs?.[_LINE_WRAPPER_ATTR] != null)]) {
    unwrapNode(el);
  }
  for (const el of findAllElements(doc, () => true)) {
    if (el.attribs) {
      delete el.attribs["data-tn-id"];
      delete el.attribs[_LINE_WRAPPER_ATTR];
      delete el.attribs[_INLINE_ID_ATTR];
      delete el.attribs[_ANNOTATION_ID_ATTR];
    }
  }
}

function unwrapNode(el) {
  const parent = el.parent;
  if (!parent) return;
  const idx = parent.children.indexOf(el);
  const kids = el.children;
  parent.children.splice(idx, 1, ...kids);
  let prev = el.prev;
  for (const k of kids) {
    k.parent = parent;
    k.prev = prev;
    if (prev) prev.next = k;
    prev = k;
  }
  if (prev) prev.next = el.next;
  if (el.next) el.next.prev = prev;
  el.parent = el.prev = el.next = null;
}

/** 按锚点回填模板（_render_segments_html 等价）。 */
export function renderSegmentsHTML(templateHTML, segments, {
  bilingual = false, order = "target_first", preserveSourceStyle = false, sourceAnchorMap = null,
} = {}) {
  const doc = parseHTML(templateHTML);
  const byAnchor = new Map();
  for (const seg of segments ?? []) {
    if (!seg.anchor) continue;
    if (!byAnchor.has(seg.anchor)) {
      byAnchor.set(seg.anchor, { seg, text: "", source: "" });
    }
    const entry = byAnchor.get(seg.anchor);
    entry.text += segText(seg);
    entry.source += seg.source;
  }
  for (const el of findAllElements(doc, (n) => n.attribs?.["data-tn-id"] != null)) {
    const anchor = el.attribs["data-tn-id"];
    const entry = byAnchor.get(anchor);
    if (!entry) continue;
    const target = entry.text;
    const source = entry.source;
    if (bilingual) appendSourceBlock(el, target, source, { order, preserveSourceStyle, sourceAnchorMap, anchor });
    if (target === source) continue; // 译文==源文：跳过替换
    replaceBlockContent(el, entry.seg, target, source);
  }
  cleanupRendered(doc);
  return serializeDocument(doc);
}

/** 无模板章重建（旧状态/HTML 源）。 */
export function renderChapterHTML(chapter, { bilingual = false, order = "target_first" } = {}) {
  const parts = [];
  for (const [kind, text, source] of mergedParagraphs(chapter)) {
    if (kind === "heading") {
      const level = headingLevelOf(chapter);
      parts.push(`<h${level}>${escapeHtmlText(text)}</h${level}>`);
    } else {
      parts.push(`<p>${escapeHtmlText(text)}</p>`);
      const src = bilingualSource(source, text);
      if (bilingual && src) {
        parts.push(`<p class="tn-source">${escapeHtmlText(src)}</p>`);
      }
    }
  }
  return parts.join("\n");
}

function headingLevelOf(chapter) {
  const level = chapter.meta?.heading_level;
  return Number.isInteger(level) && level >= 1 && level <= 6 ? level : 1;
}

// ---- 对外入口 ----

export async function assemble(stateDir, inputPath, {
  outPath = null, outFormat = "epub", bilingual = false,
  order = "target_first", preserveSourceStyle = false, aboutPage = true,
} = {}) {
  const { manifest, chapters } = await readState(stateDir);
  const doc = {
    manifest, chapters,
    title: manifest.title ?? "",
    fmt: manifest.fmt ?? "text",
    meta: manifest.meta ?? {},
    bilingual_order: order,
  };
  let resolvedOut;
  if (outPath) {
    resolvedOut = path.resolve(outPath);
  } else {
    const base = defaultOut(inputPath, outFormat, null, { bilingual: false });
    resolvedOut = bilingual ? bilingualOutPath(base) : base;
  }
  if (outFormat === "txt" || outFormat === "markdown") {
    const content = outFormat === "txt"
      ? assembleTextRows(doc, { bilingual })
      : assembleMarkdown(doc);
    await ensureParentDir(resolvedOut);
    await fs.writeFile(resolvedOut, content.replace(/\n*$/, "\n"), "utf-8");
    return resolvedOut;
  }
  if (outFormat === "html") {
    const content = await assembleHTML(doc, { bilingual, order, preserveSourceStyle });
    await ensureParentDir(resolvedOut);
    await fs.writeFile(resolvedOut, content, "utf-8");
    return resolvedOut;
  }
  if (outFormat === "epub") {
    return await assembleEPUB(doc, inputPath, resolvedOut, { bilingual, order, preserveSourceStyle, aboutPage });
  }
  throw new Error(`不支持的输出格式：${outFormat}`);
}

// ---- HTML 输出 ----

async function assembleHTML(doc, { bilingual, order, preserveSourceStyle }) {
  const headHTML = typeof doc.meta?.head_html === "string" ? doc.meta.head_html : "";
  const hasCharset = /<meta[^>]+charset/i.test(headHTML);
  const head = hasCharset ? headHTML : `<meta charset="utf-8"/>${headHTML}`;
  const bodyParts = [];
  for (const chapter of doc.chapters) {
    bodyParts.push(renderChapterHTML(chapter, { bilingual, order }));
  }
  const style = bilingual && !preserveSourceStyle ? `<style>${BILINGUAL_CSS}</style>` : "";
  return `<!DOCTYPE html>\n<html><head>${head}${style}</head>\n<body>\n${bodyParts.join("\n")}\n</body></html>\n`;
}

// ---- EPUB 输出 ----

async function assembleEPUB(doc, inputPath, outPath, opts) {
  if (doc.fmt === "epub") {
    return await backfillEPUB(doc, inputPath, outPath, opts);
  }
  return await buildEPUBFromChapters(doc, inputPath, outPath, opts);
}

// 三处一致性硬校验 + 回填（主规格 §13.3）
async function backfillEPUB(doc, inputPath, outPath, { bilingual, order, preserveSourceStyle, aboutPage }) {
  const files = await openZip(inputPath);
  const names = [...files.keys()];
  const meta = doc.meta ?? {};
  const resources = Array.isArray(meta.epub_resources) ? meta.epub_resources : [];
  const tocPaths = Array.isArray(meta.toc_paths) ? meta.toc_paths : [];
  // 段索引：anchor → (source 合并, seg)；resource_href → segments
  const segByAnchor = new Map();
  const segsByHref = new Map();
  for (const chapter of doc.chapters) {
    for (const seg of chapter.segments ?? []) {
      if (seg.anchor) {
        if (!segByAnchor.has(seg.anchor)) segByAnchor.set(seg.anchor, { seg, source: "" });
        segByAnchor.get(seg.anchor).source += seg.source;
      }
      if (seg.resource_href) {
        if (!segsByHref.has(seg.resource_href)) segsByHref.set(seg.resource_href, []);
        segsByHref.get(seg.resource_href).push(seg);
      }
    }
  }
  // 竖排检测
  const forceHorizontal = epubLooksVertical(files);
  // 重建每个资源
  const rendered = new Map();
  const freshAnchors = new Map(); // resource → Set(anchor)
  const errors = [];
  const globalIDSet = collectGlobalIDs(files);
  const sourceAnchorMap = new Map();
  for (const resource of resources) {
    const href = resource.href;
    if (!names.includes(href)) {
      errors.push(`状态引用的物理资源不在 EPUB 内：${href}`);
      continue;
    }
    const html = files.get(href).toString("utf-8");
    const [, freshSegs, template] = annotateEpubResource(html, resource.index, href, {
      bookTitle: doc.title,
      skipNavigation: tocPaths.includes(href),
    });
    freshAnchors.set(href, new Set(freshSegs.map((s) => s.anchor)));
    // ① 状态非 cont 锚点 ⊆ fresh 锚点集
    const stateSegs = segsByHref.get(href) ?? [];
    for (const seg of stateSegs) {
      if (seg.cont) continue;
      if (seg.anchor && !freshAnchors.get(href)?.has(seg.anchor)) {
        errors.push(`状态锚点 ${seg.anchor} 不在重建资源 ${href} 中`);
      }
    }
    // ② 逐 anchor source 一致
    const freshByAnchor = new Map();
    for (const s of freshSegs) {
      if (!freshByAnchor.has(s.anchor)) freshByAnchor.set(s.anchor, { source: "" });
      freshByAnchor.get(s.anchor).source += s.source;
    }
    for (const [anchor, entry] of freshByAnchor) {
      const stateEntry = segByAnchor.get(anchor);
      if (stateEntry && stateEntry.source.trim() !== entry.source.trim()) {
        errors.push(`锚点 ${anchor} 源文与原始 EPUB 不一致（书稿可能被替换）`);
      }
    }
    rendered.set(href, renderSegmentsHTML(template, stateSegs, {
      bilingual, order, preserveSourceStyle, sourceAnchorMap: null,
    }));
    // 双语合成原文锚点（第二遍在含译文的模板上分配）
    if (bilingual) {
      const rerendered = renderSegmentsHTML(template, stateSegs, {
        bilingual: true, order, preserveSourceStyle,
        sourceAnchorMap: buildSourceAnchorMap(template, stateSegs, globalIDSet),
      });
      rendered.set(href, rerendered);
    }
  }
  if (errors.length > 0) {
    const shown = errors.slice(0, 3).join("；");
    throw new Error(`回填一致性校验失败：${shown}${errors.length > 3 ? "…" : ""}`);
  }
  // OPF/书名/语言
  const opfPath = findOpfPathInFiles(files);
  const lang = epubLang(doc.manifest?.target_lang ?? "zh");
  const exportTitle = exportBookTitle(doc.title, lang, { bilingual });
  // 跨资源链接改写映射（双语）
  const outEntries = [];
  const seen = new Set();
  for (const name of names) {
    seen.add(name);
    let data = files.get(name);
    if (name === opfPath) {
      data = Buffer.from(rewriteOPF(data.toString("utf-8"), exportTitle, lang, forceHorizontal), "utf-8");
    } else if (isTocEntry(name, opfPath, tocPaths, files)) {
      data = Buffer.from(rewriteTocTitles(data.toString("utf-8"), doc.manifest, tocPaths, name), "utf-8");
    } else if (rendered.has(name)) {
      let html = rendered.get(name);
      if (forceHorizontal) html = injectHorizontalOverride(html);
      if (bilingual && !preserveSourceStyle) html = injectStyleTag(html, BILINGUAL_CSS, _BILINGUAL_STYLE_ID);
      data = Buffer.from(rewriteHTMLDocument(html, lang), "utf-8");
    }
    outEntries.push({ name, data, store: name === "mimetype" });
  }
  await writeZipAtomic(outPath, outEntries);
  if (aboutPage) {
    await appendAboutPage(outPath, lang);
  }
  return outPath;
}

function collectGlobalIDs(files) {
  const ids = new Set();
  for (const [name, data] of files) {
    if (!_HTML_EXTS.some((e) => name.toLowerCase().endsWith(e))) continue;
    const doc = parseHTML(data.toString("utf-8"));
    for (const el of findAllElements(doc, () => true)) {
      if (el.attribs?.id) ids.add(el.attribs.id);
      if (el.attribs?.name) ids.add(el.attribs.name);
    }
  }
  return ids;
}

function buildSourceAnchorMap(templateHTML, segments, globalIDSet) {
  // anchor → synthetic id（避开全书 id/name）
  const map = new Map();
  for (const seg of segments ?? []) {
    if (!seg.anchor) continue;
    const src = seg.source ?? "";
    let base = `${_SOURCE_ANCHOR_PREFIX}${seg.anchor}`;
    let id = base;
    let n = 1;
    while (globalIDSet.has(id) || map.values().some?.((v) => v === id)) {
      id = `${base}-${++n}`;
    }
    globalIDSet.add(id);
    map.set(`${seg.anchor}|${src}`, id);
  }
  return map;
}

function epubLooksVertical(files) {
  const patterns = [
    /writing-mode:\s*(?:-epub-|-webkit-)?vertical-(?:rl|lr)/i,
    /writing-mode:\s*(?:-epub-|-webkit-)?tb-rl/i,
    /page-progression-direction\s*=\s*"rtl"/i,
    /class\s*=\s*"[^"]*\bvrtl\b/i,
  ];
  for (const [name, data] of files) {
    const lower = name.toLowerCase();
    if (lower.endsWith(".opf") || lower.endsWith(".css") || _HTML_EXTS.some((e) => lower.endsWith(e))) {
      const text = data.toString("utf-8");
      for (const re of patterns) {
        if (re.test(text)) return true;
      }
    }
  }
  return false;
}

function findOpfPathInFiles(files) {
  const container = files.get("META-INF/container.xml");
  if (container) {
    const m = container.toString("utf-8").match(/full-path\s*=\s*"([^"]+)"/);
    if (m) return m[1];
  }
  for (const name of files.keys()) {
    if (name.toLowerCase().endsWith(".opf")) return name;
  }
  throw new Error("EPUB 损坏：未找到 OPF");
}

function rewriteOPF(opf, title, lang, forceHorizontal) {
  let out = opf;
  if (/<dc:title[^>]*>[\s\S]*?<\/dc:title>/.test(out)) {
    out = out.replace(/<dc:title[^>]*>[\s\S]*?<\/dc:title>/, `<dc:title>${escapeHtmlText(title)}</dc:title>`);
  } else if (/<metadata[^>]*>/.test(out)) {
    out = out.replace(/<metadata([^>]*)>/, `<metadata$1>\n    <dc:title>${escapeHtmlText(title)}</dc:title>`);
  }
  if (/<dc:language[^>]*>[\s\S]*?<\/dc:language>/.test(out)) {
    out = out.replace(/<dc:language[^>]*>[\s\S]*?<\/dc:language>/, `<dc:language>${lang}</dc:language>`);
  } else if (/<metadata[^>]*>/.test(out)) {
    out = out.replace(/<metadata([^>]*)>/, `<metadata$1>\n    <dc:language>${lang}</dc:language>`);
  }
  if (forceHorizontal) {
    out = out.replace(/<spine([^>]*?)>/, (m, attrs) => {
      if (/page-progression-direction/.test(attrs)) {
        return `<spine${attrs.replace(/page-progression-direction\s*=\s*"[^"]*"/, 'page-progression-direction="ltr"')}>`;
      }
      return `<spine${attrs} page-progression-direction="ltr">`;
    });
  }
  return out;
}

function isTocEntry(name, opfPath, tocPaths, files) {
  if (tocPaths.includes(name)) return true;
  const lower = name.toLowerCase();
  return lower.endsWith(".ncx");
}

/** NCX/NAV 标题回填：按 toc_path+node_index 精确匹配且 raw_href 一致才替换。 */
function rewriteTocTitles(html, manifest, tocPaths, name) {
  const meta = manifest?.meta ?? {};
  const entries = Array.isArray(meta.toc_entries) ? meta.toc_entries : [];
  const byKey = new Map();
  for (const entry of entries) {
    if (entry?.toc_path === name && Number.isInteger(entry.node_index)) {
      const title = entry.title_translated;
      if (typeof title === "string" && title.trim() !== "") {
        byKey.set(`${entry.node_index}|${entry.raw_href ?? ""}`, title);
      }
    }
  }
  if (byKey.size === 0) return html;
  if (name.toLowerCase().endsWith(".ncx")) {
    // NCX：navPoint 顺序遍历（与解析端一致），匹配 node_index+raw_href
    const doc = parseHTML(html);
    let nodeIndex = 0;
    for (const point of findAllElements(doc, (n) => n.name === "navpoint" || localNameOf(n) === "navPoint")) {
      const idx = nodeIndex++;
      const label = point.children?.find((c) => isTag(c) && localNameOf(c) === "navLabel");
      const content = point.children?.find((c) => isTag(c) && localNameOf(c) === "content");
      const rawHref = content?.attribs?.["src"] ?? "";
      const title = byKey.get(`${idx}|${rawHref}`);
      if (label && title != null) {
        const textEl = findAllElements(label, (n) => localNameOf(n) === "text")[0];
        if (textEl) {
          textEl.children = [{ type: "text", data: title, parent: textEl }];
        }
      }
    }
    return serializeDocument(doc);
  }
  // NAV：ol/li preorder
  const doc = parseHTML(html);
  let nodeIndex = 0;
  for (const scope of navTocScopes(doc)) {
    const rootList = navRootList(scope);
    if (!rootList) continue;
    visitLi(rootList);
  }
  function visitLi(list) {
    for (const li of directChildren(list)) {
      if (li.name !== "li") continue;
      const idx = nodeIndex++;
      const a = directChildren(li).find((c) => c.name === "a");
      const rawHref = a?.attribs?.["href"] ?? "";
      const title = byKey.get(`${idx}|${rawHref}`);
      if (a && title != null) {
        a.children = [{ type: "text", data: title, parent: a }];
      }
      const childOl = directChildren(li).find((c) => c.name === "ol");
      if (childOl) visitLi(childOl);
    }
  }
  return serializeDocument(doc);
}

function localNameOf(el) {
  let t = el.name ?? "";
  const brace = t.lastIndexOf("}");
  if (brace >= 0) t = t.slice(brace + 1);
  return t;
}

function injectHorizontalOverride(html) {
  const css = `html,body,.vrtl,.vertical,[class*="vrtl"]{writing-mode:horizontal-tb !important;text-orientation:mixed !important}`;
  return injectStyleTag(html, css, _HORIZONTAL_OVERRIDE_ID);
}

function injectStyleTag(html, css, id) {
  const tag = `<style id="${id}">${css}</style>`;
  if (/<\/head>/i.test(html)) return html.replace(/<\/head>/i, `${tag}</head>`);
  if (/<body[^>]*>/i.test(html)) return html.replace(/<body([^>]*)>/i, `<body$1>${tag}`);
  return tag + html;
}

function rewriteHTMLDocument(html, lang) {
  let out = html;
  if (/<html[^>]*>/i.test(out)) {
    out = out.replace(/<html([^>]*)>/i, (m, attrs) => {
      attrs = attrs.replace(/\s(?:xml:)?lang\s*=\s*"[^"]*"/gi, "");
      return `<html${attrs} lang="${lang}" xml:lang="${lang}">`;
    });
  }
  return out;
}

// ---- 关于页 ----

export async function appendAboutPage(epubPath, lang = "zh-Hans") {
  const data = await fs.readFile(epubPath);
  const zip = await JSZip.loadAsync(data);
  const container = zip.file("META-INF/container.xml");
  let opfPath = null;
  if (container) {
    const m = (await container.async("string")).match(/full-path\s*=\s*"([^"]+)"/);
    if (m) opfPath = m[1];
  }
  if (!opfPath) return false;
  const opfFile = zip.file(opfPath);
  if (!opfFile) return false;
  const opf = await opfFile.async("string");
  const names = Object.keys(zip.files);
  let aboutPath = path.posix.join(path.posix.dirname(opfPath), "trans-novel-about.xhtml");
  if (names.includes(aboutPath)) {
    let n = 2;
    while (names.includes(aboutPath)) {
      aboutPath = path.posix.join(path.posix.dirname(opfPath), `trans-novel-about-${n}.xhtml`);
      n++;
    }
  }
  const aboutXHTML = aboutPageContent(lang);
  let itemID = "trans-novel-about";
  let idN = 2;
  while (new RegExp(`id\\s*=\\s*"${itemID}"`).test(opf)) {
    itemID = `trans-novel-about-${idN++}`;
  }
  let newOpf = opf;
  const manifestInsert = `<item id="${itemID}" href="${path.posix.basename(aboutPath)}" media-type="application/xhtml+xml"/>`;
  if (/<\/manifest>/i.test(newOpf)) {
    newOpf = newOpf.replace(/<\/manifest>/i, `${manifestInsert}</manifest>`);
  } else {
    return false;
  }
  const spineInsert = `<itemref idref="${itemID}"/>`;
  if (/<\/spine>/i.test(newOpf)) {
    newOpf = newOpf.replace(/<\/spine>/i, `${spineInsert}</spine>`);
  } else {
    return false;
  }
  const entries = [];
  for (const [name, file] of Object.entries(zip.files)) {
    if (file.dir) continue;
    entries.push({ name, data: await file.async("nodebuffer"), store: name === "mimetype" });
  }
  entries.push({ name: aboutPath, data: Buffer.from(aboutXHTML, "utf-8") });
  entries.find((e) => e.name === opfPath).data = Buffer.from(newOpf, "utf-8");
  await writeZipAtomic(epubPath, entries);
  return true;
}

function aboutPageContent(lang) {
  return `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="${lang}" lang="${lang}">
<head><title>关于此翻译</title>
<style>html,body{writing-mode:horizontal-tb}</style></head>
<body>
<h1>关于此翻译</h1>
<p>本书由 文译 Wenyi ——多智能体长篇小说翻译系统——自动翻译生成。</p>
<p>翻译流程包含全书理解预扫、逐批翻译与润色、术语一致性与全书审校。</p>
<p>项目主页与反馈：GitHub BigDawnGhost/wenyi。</p>
</body></html>`;
}

// ---- 从章节构建 EPUB（非 EPUB 源）----

async function buildEPUBFromChapters(doc, inputPath, outPath, { bilingual, order, preserveSourceStyle, aboutPage }) {
  const lang = epubLang(doc.manifest?.target_lang ?? "zh");
  const exportTitle = exportBookTitle(doc.title, lang, { bilingual });
  const entries = [{ name: "mimetype", data: "application/epub+zip", store: true }];
  entries.push({ name: "META-INF/container.xml", data: `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>` });
  const manifestItems = [];
  const spineRefs = [];
  const zipExtra = [];
  if (doc.fmt === "fb2") {
    const binaries = await readFb2Binaries(inputPath);
    const used = new Map();
    for (const [id, [contentType, bytes]] of Object.entries(binaries)) {
      // id 即文件名（原样保留扩展名，与 Python 实现一致）；冲突时 -2 后缀
      let name = `images/${id}`;
      if (used.has(name)) {
        let n = 2;
        while (used.has(`${name}-${n}`)) n++;
        name = `${name}-${n}`;
      }
      used.set(name, true);
      zipExtra.push({ name: `OEBPS/${name}`, data: bytes });
      manifestItems.push({ id: `img-${slugID(id)}`, href: name, mediaType: contentType });
    }
    const coverID = doc.meta?.fb2_cover_image;
    if (coverID && binaries[coverID]) {
      const item = manifestItems.find((m) => m.href === `images/${coverID}`);
      if (item) {
        item.properties = "cover-image";
        item.id = "cover-image";
      }
    }
  }
  const chapterFiles = [];
  for (const chapter of doc.chapters) {
    const fname = `ch${chapter.index}.xhtml`;
    let bodyHTML;
    if (doc.fmt === "html" || doc.fmt === "pdf") {
      bodyHTML = chapter.template
        ? renderSegmentsHTML(chapter.template, chapter.segments, { bilingual, order, preserveSourceStyle })
        : renderChapterHTML(chapter, { bilingual, order });
    } else {
      bodyHTML = renderChapterHTML(chapter, { bilingual, order });
      // FB2 章内图片按 meta.fb2_images position 插入
      const images = Array.isArray(chapter.meta?.fb2_images) ? chapter.meta.fb2_images : [];
      for (const img of images) {
        const src = `images/${img.id}`;
        const tag = `<p><img src="${escapeHtmlAttr(src)}" alt=""/></p>`;
        // 插到 position（段序）之后
        bodyHTML = insertImageAtPosition(bodyHTML, tag, img.position);
      }
    }
    const title = chTitleFromEntry(doc.manifest.chapters.find((c) => c.index === chapter.index) ?? {}) || chapter.title || `Chapter ${chapter.index}`;
    const xhtml = xhtmlDocument(title, lang, bodyHTML);
    chapterFiles.push({ name: `OEBPS/${fname}`, data: Buffer.from(xhtml, "utf-8") });
    manifestItems.push({ id: `ch${chapter.index}`, href: fname, mediaType: "application/xhtml+xml" });
    spineRefs.push(`ch${chapter.index}`);
  }
  const ncx = buildNCX(exportTitle, doc.chapters, doc.manifest);
  const nav = buildNav(exportTitle, doc.chapters, doc.manifest, lang);
  entries.push({ name: "OEBPS/toc.ncx", data: Buffer.from(ncx, "utf-8") });
  entries.push({ name: "OEBPS/nav.xhtml", data: Buffer.from(nav, "utf-8") });
  manifestItems.push({ id: "ncx", href: "toc.ncx", mediaType: "application/x-dtbncx+xml" });
  manifestItems.push({ id: "nav", href: "nav.xhtml", mediaType: "application/xhtml+xml", properties: "nav" });
  for (const extra of zipExtra) entries.push(extra);
  for (const cf of chapterFiles) entries.push(cf);
  const opf = buildOPF(exportTitle, lang, manifestItems, spineRefs);
  entries.push({ name: "OEBPS/content.opf", data: Buffer.from(opf, "utf-8") });
  await writeZipAtomic(outPath, entries);
  if (bilingual && !preserveSourceStyle) {
    await injectBilingualStyleIntoZip(outPath, chapterFiles.map((c) => c.name), lang);
  }
  if (aboutPage) {
    await appendAboutPage(outPath, lang);
  }
  return outPath;
}

function binaryTypeOf(doc, id) {
  const resources = doc.meta?.fb2_resources ?? [];
  const hit = resources.find((r) => r.id === id);
  return hit?.content_type ?? "application/octet-stream";
}

function imageExtensionOf(contentType) {
  const map = {
    "image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif",
    "image/svg+xml": ".svg", "image/webp": ".webp",
  };
  return map[String(contentType).toLowerCase()] ?? ".bin";
}

function slugID(id) {
  return String(id).replace(/[^\w.-]+/g, "_");
}

function insertImageAtPosition(bodyHTML, tag, position) {
  // position = 段序；把 <p><img/></p> 插到第 position 个 <p> 之前（近似语义）
  const parts = bodyHTML.split(/(<p>[\s\S]*?<\/p>)/);
  let count = 0;
  for (let i = 1; i < parts.length; i += 2) {
    if (count === position) {
      parts.splice(i, 0, tag);
      return parts.join("");
    }
    count++;
  }
  return bodyHTML + tag;
}

function xhtmlDocument(title, lang, body) {
  return `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="${lang}" lang="${lang}">
<head><title>${escapeHtmlText(title)}</title></head>
<body>
${body}
</body></html>`;
}

function buildOPF(title, lang, items, spineRefs) {
  const manifest = items.map((i) => {
    const props = i.properties ? ` properties="${i.properties}"` : "";
    return `    <item id="${i.id}" href="${escapeHtmlAttr(i.href)}" media-type="${i.mediaType}"${props}/>`;
  }).join("\n");
  const spine = spineRefs.map((r) => `    <itemref idref="${r}"/>`).join("\n");
  return `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="bookid">trans-novel-${escapeHtmlText(title)}</dc:identifier>
    <dc:title>${escapeHtmlText(title)}</dc:title>
    <dc:language>${lang}</dc:language>
  </metadata>
  <manifest>
${manifest}
  </manifest>
  <spine toc="ncx">
    <itemref idref="nav"/>
${spine}
  </spine>
</package>`;
}

function buildNCX(title, chapters, manifest) {
  const titles = chapters.map((ch) => {
    const info = (manifest.chapters ?? []).find((c) => c.index === ch.index);
    return chTitleFromEntry(info ?? {}) || ch.title || `Chapter ${ch.index}`;
  });
  const navPoints = chapters.map((ch, i) => `    <navPoint id="nav${i}" playOrder="${i + 1}">
      <navLabel><text>${escapeHtmlText(titles[i])}</text></navLabel>
      <content src="ch${ch.index}.xhtml"/>
    </navPoint>`).join("\n");
  return `<?xml version="1.0" encoding="utf-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
<head><meta name="dtb:uid" content="trans-novel"/></head>
<docTitle><text>${escapeHtmlText(title)}</text></docTitle>
<navMap>
${navPoints}
</navMap></ncx>`;
}

function buildNav(title, chapters, manifest, lang) {
  const lis = chapters.map((ch) => {
    const info = (manifest.chapters ?? []).find((c) => c.index === ch.index);
    const t = chTitleFromEntry(info ?? {}) || ch.title || `Chapter ${ch.index}`;
    return `      <li><a href="ch${ch.index}.xhtml">${escapeHtmlText(t)}</a></li>`;
  }).join("\n");
  return `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="${lang}" lang="${lang}">
<head><title>${escapeHtmlText(title)}</title></head>
<body><nav epub:type="toc"><ol>
${lis}
</ol></nav></body></html>`;
}

/** zip 后处理注入双语样式（ebooklib 重建 head 会丢内联样式，故必须 zip 后注入）。 */
export async function injectBilingualStyleIntoZip(epubPath, xhtmlNames, lang) {
  const data = await fs.readFile(epubPath);
  const zip = await JSZip.loadAsync(data);
  const entries = [];
  for (const [name, file] of Object.entries(zip.files)) {
    if (file.dir) continue;
    let buf = await file.async("nodebuffer");
    if (xhtmlNames.includes(name)) {
      buf = Buffer.from(injectStyleTag(buf.toString("utf-8"), BILINGUAL_CSS, _BILINGUAL_STYLE_ID), "utf-8");
    }
    entries.push({ name, data: buf, store: name === "mimetype" });
  }
  await writeZipAtomic(epubPath, entries);
}

export { assemble as default };
