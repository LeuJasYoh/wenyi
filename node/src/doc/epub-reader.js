// EPUB 解析核心（主规格 §7.6 / 分册 03 §1.4）——最复杂的解析器。
import JSZip from "jszip";
import { promises as fs } from "node:fs";
import path from "node:path";
import {
  parseHTML, parseXML, serializeDocument, walk, findAllElements, findDirect, directChildren,
  isTag, isText, isComment, localName, getText, rlen, cpSlice, ancestors,
  BLOCK_TAGS, BLOCK_CANDIDATE_TAGS, HEADING_TAGS, ATOMIC_INLINE_TAGS,
  insertBefore, createElement, extract, WS_CHARS,
} from "./dom.js";
import { KIND_HEADING, KIND_TEXT, Segment, Chapter, Document } from "./models.js";
import { decodeMarkup } from "./encoding.js";
import { parseTocEntries, resolveEpubHref, posixBasename } from "./epub-toc.js";

const _CONTAINER = "META-INF/container.xml";
const _INLINE_META_KEY = "epub_inline";
const _INLINE_ID_ATTR = "data-tn-inline-id";
const _ANNOTATION_META_KEY = "epub_annotations";
const _ANNOTATION_ID_ATTR = "data-tn-annotation-id";
const _LINE_WRAPPER_ATTR = "data-tn-line";

// 注释号字形：数字/空白/装饰符号（Python \d = Unicode Nd，故用 \p{Nd}）
const _ANNOTATION_MARKER_ONLY = /^[\p{Nd}\s*＊※†‡\[\]()〔〕（）{}↩↵←↑↓.·:：\-]+$/u;
// 注释语义线索
const _ANNOTATION_HINT = /(?:^|[^a-z0-9])(?:note|noteref|footnote|endnote|fn|jpref|jpnote|ref|key)(?:[-_]?\p{Nd}+)?(?:$|[^a-z0-9])/iu;
const _SHORT_NUMBERED_FRAGMENT = /^[a-zA-Z]?[-_]?\p{Nd}+$/u;
const _DECORATED_CHAR = /[^\p{Nd}\s.·:\-]/u;

// ---------- zip ----------

export async function openZip(filePath) {
  const data = await fs.readFile(filePath);
  const zip = await JSZip.loadAsync(data);
  const files = new Map();
  const entries = Object.values(zip.files).filter((f) => !f.dir);
  await Promise.all(
    entries.map(async (f) => {
      files.set(f.name, await f.async("nodebuffer"));
    }),
  );
  return files;
}

// ---------- 内联保留 ----------

/** 块内需原样回填的非文本 DOM 根（含包装上卷）。 */
function preservedInlineRoots(block) {
  const seen = new Set();
  const out = [];
  for (const node of findAllElements(block, () => true, { includeSelf: true })) {
    if (node.attribs?.[_ANNOTATION_ID_ATTR] != null) continue;
    const isAtomic = ATOMIC_INLINE_TAGS.has(node.name);
    const isEmptyAnchor =
      (node.name === "a" || node.name === "span") &&
      getText(node, { strip: true }) === "" &&
      (node.attribs?.["id"] != null || node.attribs?.["name"] != null);
    if (!isAtomic && !isEmptyAnchor) continue;
    let root = node;
    // 包装上卷：父级是无文字的行内包装则整体保留
    while (
      root.parent != null &&
      isTag(root.parent) &&
      root.parent !== block &&
      !BLOCK_TAGS.has(root.parent.name) &&
      root.parent.attribs?.[_ANNOTATION_ID_ATTR] == null &&
      getText(root.parent, { strip: true }) === ""
    ) {
      root = root.parent;
    }
    if (seen.has(root)) continue;
    seen.add(root);
    out.push(root);
  }
  return out;
}

// ---------- 注释识别 ----------

function isInternalLink(link) {
  const href = link.attribs?.["href"];
  if (typeof href !== "string" || href === "") return false;
  let rest = href;
  const hi = rest.indexOf("#");
  if (hi >= 0) rest = rest.slice(0, hi);
  const sm = rest.match(/^([a-zA-Z][a-zA-Z0-9+.\-]*):/);
  if (sm) return false; // scheme → external
  if (rest.startsWith("//")) return false; // netloc → external
  return true;
}

/** link 与翻译块之间最近的 sup/sub 包装（到达 block 时仅当 block 自身是 sup/sub）。 */
function nearestMarkerWrapper(link, block) {
  let node = link.parent;
  while (node != null) {
    if (node === block) return block.name === "sup" || block.name === "sub" ? block : null;
    if (node.name === "sup" || node.name === "sub") return node;
    node = node.parent;
  }
  return null;
}

function attrTokens(node) {
  const parts = [];
  for (const key of ["id", "class", "role", "rel", "epub:type"]) {
    const v = node.attribs?.[key];
    if (v != null && v !== "") parts.push(...String(v).split(/\s+/));
  }
  return parts;
}

function hasAnnotationHint(link, marker, markerText) {
  // ① 装饰字符
  if (_DECORATED_CHAR.test(markerText ?? "")) return true;
  // ② 属性串命中
  const joined = [
    ...(link.attribs?.["href"]?.includes("#") ? [decodeFragment(link.attribs["href"])] : []),
    ...attrTokens(link),
    ...attrTokens(marker),
  ].join(" ");
  if (_ANNOTATION_HINT.test(joined)) return true;
  // ③ 短编号 fragment
  const frag = link.attribs?.["href"]?.split("#")[1] ?? "";
  return _SHORT_NUMBERED_FRAGMENT.test(frag);
}

function decodeFragment(href) {
  const hi = href.indexOf("#");
  const raw = hi >= 0 ? href.slice(hi + 1) : "";
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

/** 范围链接末尾的高置信注释号节点。 */
function rangeMarkerNode(link) {
  const significant = (link.children ?? []).filter((c) => !(isText(c) && c.data.trim() === ""));
  if (significant.length === 0) return null;
  const last = significant[significant.length - 1];
  if (!isTag(last) || (last.name !== "sup" && last.name !== "sub")) return null;
  const markerText = getText(last, { strip: true });
  if (!_ANNOTATION_MARKER_ONLY.test(markerText)) return null;
  // sub 无装饰字符 = 化学式（H2O/CO2），不算注释
  if (last.name === "sub" && !_DECORATED_CHAR.test(markerText)) return null;
  if (!hasAnnotationHint(link, last, markerText)) return null;
  return last;
}

/** 链接正文文本（跳过 rt/rp 与 marker；折叠空白）。 */
function semanticLinkText(link, markerNode) {
  let out = "";
  const visit = (node) => {
    for (const child of node.children ?? []) {
      if (isTag(child)) {
        if (child.name === "rt" || child.name === "rp") continue;
        if (child === markerNode) continue;
        visit(child);
      } else if (isText(child)) {
        out += child.data;
      }
    }
  };
  visit(link);
  return out.replace(/[ \t\r\n\f\v]+/g, " ").trim();
}

/** 识别段内注释链接并打 data-tn-annotation-id。返回 Map<root, spec>。 */
function annotationRoots(block, anchor) {
  const links = [];
  if (block.name === "a" && block.attribs?.["href"] != null && hasSupSubInside(block)) {
    links.push(block);
  }
  links.push(...findAllElements(block, (n) => n.name === "a" && n.attribs?.["href"] != null));

  const roots = new Map();
  let ordinal = 0;
  for (const link of links) {
    if (!isInternalLink(link)) continue;
    let markerWrapper = nearestMarkerWrapper(link, block);
    if (markerWrapper != null) {
      const wrapperText = getText(markerWrapper, { strip: true });
      if (!hasAnnotationHint(link, markerWrapper, wrapperText)) markerWrapper = null;
    }
    const rangeMarker = markerWrapper != null ? null : rangeMarkerNode(link);
    const semanticText = semanticLinkText(link, rangeMarker);
    if (semanticText === "" && markerWrapper == null) continue; // 纯图片链接交给内联保留
    const markerOnly =
      _ANNOTATION_MARKER_ONLY.test(semanticText) && hasAnnotationHint(link, link, semanticText);
    const mode = markerWrapper != null || markerOnly ? "point" : "range";
    const root = mode === "point" && markerWrapper != null ? markerWrapper : link;
    if (roots.has(root)) continue;
    const annotationId = `${anchor}_annotation_${ordinal++}`;
    root.attribs = root.attribs ?? {};
    root.attribs[_ANNOTATION_ID_ATTR] = annotationId;
    roots.set(root, {
      id: annotationId,
      mode,
      marker_text: mode === "point" ? getText(root, { strip: false }).trim() : rangeMarker ? getText(rangeMarker, { strip: false }).trim() : "",
      marker_node: rangeMarker,
      root,
    });
  }
  return roots;
}

function hasSupSubInside(node) {
  return findAllElements(node, (n) => n.name === "sup" || n.name === "sub").length > 0;
}

// ---------- 文本规范化 ----------

/** 折叠排版空白为单空格，并把原始（码点）偏移映射到规范化文本。 */
function normalizeHtmlText(raw, offsets) {
  const cps = Array.from(raw);
  const out = [];
  const boundary = new Array(cps.length + 1);
  for (let i = 0; i <= cps.length; i++) {
    boundary[i] = out.length;
    if (i >= cps.length) break;
    const ch = cps[i];
    if (WS_CHARS.has(ch)) {
      if (out.length === 0 || out[out.length - 1] !== " ") out.push(" ");
    } else {
      out.push(ch);
    }
  }
  const collapsed = out.join("");
  const leading = collapsed.length - collapsed.replace(/^ /, "").length;
  const text = collapsed.trim();
  const rawLen = cps.length;
  return [text, offsets.map((o) => {
    const b = boundary[Math.min(Math.max(o, 0), rawLen)] - leading;
    return Math.min(Math.max(b, 0), text.length);
  })];
}

/** 提取可翻译文本 + 内联/注释元数据（主规格 §7.6.3 _segment_content）。 */
function segmentContent(block, anchor, annotations) {
  const markerNodes = new Set();
  for (const spec of annotations.values()) {
    if (spec.marker_node != null) markerNodes.add(spec.marker_node);
  }
  const preserved = preservedInlineRoots(block);
  const preservedIds = new Set(preserved);
  const textParts = [];
  const preservedOffsets = [];
  const annotationEvents = new Map();
  let rawLength = 0;
  const appendText = (s) => {
    textParts.push(s);
    rawLength += rlen(s);
  };
  const hasFollowingText = (node) => {
    for (let s = node.next; s != null; s = s.next) {
      if (isText(s) && s.data.trim() !== "") return true;
      if (isTag(s) && getText(s, { strip: true }) !== "") return true;
    }
    return false;
  };
  const walkNodes = (parent, insideRange) => {
    for (const child of parent.children ?? []) {
      if (isTag(child)) {
        if (child.name === "rt" || child.name === "rp") continue;
        if (insideRange && markerNodes.has(child)) continue;
        const ann = annotations.get(child);
        if (ann != null) {
          const start = rawLength;
          if (ann.mode === "range") walkNodes(child, true);
          annotationEvents.set(ann, [start, rawLength]);
          if (preservedIds.has(child)) preservedOffsets.push([child, rawLength]);
          continue; // 注释根不再普通下钻（point 子树排除 / range 已收集）
        }
        if (preservedIds.has(child)) {
          preservedOffsets.push([child, rawLength]);
          continue;
        }
        walkNodes(child, insideRange);
      } else if (isText(child)) {
        if (insideRange && child.next != null && markerNodes.has(child.next)) {
          appendText(child.data.replace(/[ \t\r\n\f\v]+$/, ""));
          continue;
        }
        if (
          insideRange &&
          child.data.trim() === "" &&
          child.prev != null &&
          markerNodes.has(child.prev) &&
          !hasFollowingText(child)
        ) {
          continue;
        }
        appendText(child.data);
      }
    }
  };
  const blockAnn = annotations.get(block);
  if (blockAnn != null) {
    if (blockAnn.mode === "range") walkNodes(block, true);
    annotationEvents.set(blockAnn, [0, rawLength]);
    if (preservedIds.has(block)) preservedOffsets.push([block, rawLength]);
  } else {
    walkNodes(block, false);
  }
  const rawText = textParts.join("");
  const offsets = [];
  for (const [, off] of preservedOffsets) offsets.push(off);
  for (const spec of annotations.values()) {
    const ev = annotationEvents.get(spec) ?? [rawLength, rawLength];
    offsets.push(ev[0], ev[1]);
  }
  const [text, mapped] = normalizeHtmlText(rawText, offsets);
  if (text === "") return ["", {}];
  const meta = {};
  if (preservedOffsets.length > 0) {
    const nodes = [];
    for (let i = 0; i < preservedOffsets.length; i++) {
      const [node] = preservedOffsets[i];
      const inlineId = `${anchor}_inline_${i}`;
      const offset = mapped[i];
      node.attribs = node.attribs ?? {};
      node.attribs[_INLINE_ID_ATTR] = inlineId;
      nodes.push({
        id: inlineId,
        tag: node.name,
        placement: offset === 0 ? "before" : offset === text.length ? "after" : "inline",
        offset,
      });
    }
    meta[_INLINE_META_KEY] = { version: 1, source_length: text.length, nodes };
  }
  const items = [];
  let ai = 0;
  for (const spec of annotations.values()) {
    const start = mapped[preservedOffsets.length + ai];
    const end = mapped[preservedOffsets.length + ai + 1];
    ai += 2;
    items.push({
      id: spec.id,
      mode: spec.mode,
      source_start: start,
      source_end: end,
      source_text: cpSlice(text, start, end),
      marker_text: spec.marker_text,
    });
  }
  if (items.length > 0) {
    meta[_ANNOTATION_META_KEY] = { version: 1, source_length: text.length, items };
  }
  return [text, meta];
}

// ---------- 目标选择 ----------

function hasMeaningfulDescendantBlock(element) {
  return findAllElements(element, (n) => BLOCK_CANDIDATE_TAGS.has(n.name)).some(
    (n) => getText(n, { strip: true }) !== "",
  );
}

function listItemLinkTarget(element) {
  const a = directChildren(element).find((c) => c.name === "a") ?? null;
  if (a != null && getText(a, { strip: true }) !== "") return a;
  return null;
}

/** 把直接 br 分隔的可见行包装为独立目标（原 br 不动）。 */
function splitDirectBreakLines(element) {
  if (!(element.children ?? []).some((c) => isTag(c) && c.name === "br")) return [element];
  const runs = [];
  let cur = [];
  for (const child of element.children) {
    if (isTag(child) && child.name === "br") {
      runs.push(cur);
      cur = [];
    } else {
      cur.push(child);
    }
  }
  runs.push(cur);
  const wrappers = [];
  for (const run of runs) {
    const visible = run.some((n) => (isTag(n) && getText(n, { strip: true }) !== "") || (isText(n) && n.data.trim() !== ""));
    if (!visible) continue;
    const wrapper = createElement("span", { [_LINE_WRAPPER_ATTR]: "true" });
    if (run.length > 0) insertBefore(run[0], wrapper);
    for (const n of run) {
      wrapper.children.push(extract(n));
      n.parent = wrapper;
      n.prev = wrapper.children.length > 1 ? wrapper.children[wrapper.children.length - 2] : null;
      n.next = null;
      if (n.prev) n.prev.next = n;
    }
    wrappers.push(wrapper);
  }
  return wrappers;
}

function insideNavigationList(element) {
  let insideListItem = false;
  let insideNav = false;
  for (const a of [element, ...ancestors(element)]) {
    if (a.name === "li") insideListItem = true;
    else if (a.name === "nav") {
      insideNav = true;
      break;
    }
  }
  return insideListItem && insideNav;
}

/** 选择可安全替换内容的最细粒度节点（文档顺序）。 */
function translationTargets(doc, { skipNavigation }) {
  const targets = [];
  for (const el of findAllElements(doc, (n) => BLOCK_CANDIDATE_TAGS.has(n.name))) {
    if (skipNavigation && insideNavigationList(el)) continue;
    const hasDescendantBlock = hasMeaningfulDescendantBlock(el);
    if (el.name === "li") {
      const link = listItemLinkTarget(el);
      if (link != null) {
        targets.push(...splitDirectBreakLines(link));
        continue;
      }
      if (hasDescendantBlock) continue;
    } else if (hasDescendantBlock) {
      continue;
    }
    targets.push(...splitDirectBreakLines(el));
  }
  return targets;
}

// ---------- OPF ----------

export function findOpfPath(files) {
  const data = files.get(_CONTAINER);
  if (data == null) throw new Error("EPUB 损坏：缺少 META-INF/container.xml");
  const doc = parseXML(data.toString("utf-8"));
  for (const el of findAllElements(doc, () => true)) {
    if (localName(el.name) === "rootfile") {
      const fp = (el.attribs?.["full-path"] ?? "").trim();
      if (fp) return fp;
    }
  }
  throw new Error("EPUB 损坏：container.xml 未找到有效的 rootfile full-path");
}

export function parseOpf(files, opfPath) {
  const data = files.get(opfPath);
  if (data == null) throw new Error(`EPUB 损坏：缺少 ${opfPath}`);
  const doc = parseXML(data.toString("utf-8"));
  let bookTitle = "";
  const manifest = new Map();
  const spineIds = [];
  const tocIds = [];
  for (const el of findAllElements(doc, () => true)) {
    const name = localName(el.name);
    if (name === "title") {
      const t = getText(el, { strip: true });
      if (t && !bookTitle) bookTitle = t;
    } else if (name === "item") {
      const id = el.attribs?.["id"] ?? "";
      if (!id) continue;
      manifest.set(id, {
        href: el.attribs?.["href"] ?? "",
        media: el.attribs?.["media-type"] ?? "",
        properties: el.attribs?.["properties"] ?? "",
      });
    } else if (name === "itemref") {
      spineIds.push(el.attribs?.["idref"] ?? "");
    } else if (name === "spine") {
      const toc = el.attribs?.["toc"];
      if (toc) tocIds.push(toc);
    }
  }
  const hrefs = [];
  const seenHrefs = new Set();
  for (const sid of spineIds) {
    const item = manifest.get(sid);
    if (!item) continue;
    const { href, media } = item;
    if (!media.includes("html") && !/\.(xhtml|html|htm)$/i.test(href)) continue;
    const zipHref = resolveEpubHref(opfPath, href).resource_href;
    if (seenHrefs.has(zipHref)) continue;
    seenHrefs.add(zipHref);
    hrefs.push(zipHref);
  }
  const navIds = [];
  const ncxIds = [];
  for (const [id, item] of manifest) {
    if (item.properties.split(/\s+/).includes("nav")) navIds.push(id);
    else if (item.media === "application/x-dtbncx+xml") ncxIds.push(id);
  }
  const tocPaths = [];
  const seenTocs = new Set();
  for (const id of [...navIds, ...tocIds, ...ncxIds]) {
    const item = manifest.get(id);
    if (!item || !item.href) continue;
    const p = resolveEpubHref(opfPath, item.href).resource_href;
    if (!p || seenTocs.has(p)) continue;
    seenTocs.add(p);
    tocPaths.push(p);
  }
  return [bookTitle, hrefs, tocPaths];
}

// ---------- 资源标注 ----------

function looksLikeInternalTitle(title, href, bookTitle = "") {
  const stripped = (title ?? "").trim();
  const base = posixBasename(href).split(".").slice(0, -1).join(".") || posixBasename(href);
  if (base && stripped === base) return true;
  if (bookTitle && stripped === bookTitle.trim()) return true;
  return false;
}

/** 核心标注函数：标注单个物理 XHTML，返回 [资源标题, segments, 模板 HTML]。 */
export function annotateEpubResource(html, resourceIndex, href, { bookTitle = "", skipNavigation = false } = {}) {
  const doc = parseHTML(html);
  let idx = 0;
  let firstHeading = null;
  const headingTitleParts = [];
  const segments = [];

  for (const el of translationTargets(doc, { skipNavigation })) {
    const anchor = `tn${resourceIndex}_${idx}`;
    const annotations = annotationRoots(el, anchor);
    // 保护注释相关节点（id/name 迁移时跳过）
    const protectedNodes = new Set();
    for (const [root, spec] of annotations) {
      if (spec.mode === "point") {
        protectedNodes.add(root);
        for (const d of findAllElements(root, () => true, { includeSelf: false })) protectedNodes.add(d);
      } else {
        protectedNodes.add(root);
        if (spec.marker_node != null) {
          protectedNodes.add(spec.marker_node);
          for (const a of ancestors(spec.marker_node)) {
            if (a === root) break;
            protectedNodes.add(a);
          }
        }
      }
    }
    // id/name 迁移：有文字的节点把 id/name 弹出为前置空锚点（range 根内用 span）
    const rangeRoots = new Set();
    for (const [root, spec] of annotations) if (spec.mode === "range") rangeRoots.add(root);
    const inRangeRoot = (node) => ancestors(node).some((a) => rangeRoots.has(a)) || rangeRoots.has(node);
    for (const node of findAllElements(el, () => true, { includeSelf: false })) {
      if (protectedNodes.has(node)) continue;
      const hasId = node.attribs?.["id"] != null && node.attribs["id"] !== "";
      const hasName = node.attribs?.["name"] != null && node.attribs["name"] !== "";
      if (!hasId && !hasName) continue;
      if (getText(node, { strip: true }) === "") continue; // 无文字节点走内联保留
      const marker = createElement(inRangeRoot(node) ? "span" : "a", {});
      if (hasId) marker.attribs["id"] = node.attribs["id"];
      if (hasName) marker.attribs["name"] = node.attribs["name"];
      delete node.attribs["id"];
      delete node.attribs["name"];
      insertBefore(node, marker);
    }
    const [text, meta] = segmentContent(el, anchor, annotations);
    if (text === "") continue; // 不占 idx
    el.attribs = el.attribs ?? {};
    el.attribs["data-tn-id"] = anchor;
    const kind = selfOrAncestorIn(el, HEADING_TAGS) ? KIND_HEADING : KIND_TEXT;
    if (kind === KIND_HEADING) {
      let headingEl = el;
      for (const a of [el, ...ancestors(el)]) {
        if (HEADING_TAGS.has(a.name)) {
          headingEl = a;
          break;
        }
      }
      if (firstHeading == null) firstHeading = headingEl;
      if (headingEl === firstHeading) headingTitleParts.push(text);
    }
    segments.push(
      new Segment({ index: idx, source: text, kind, anchor, resource_href: href, meta }),
    );
    idx += 1;
  }
  let title = headingTitleParts.join(" ");
  if (!title) {
    const titleTag = findAllElements(doc, (n) => n.name === "title")[0];
    if (titleTag) {
      const t = getText(titleTag, { strip: true });
      if (t && !looksLikeInternalTitle(t, href, bookTitle)) title = t;
    }
  }
  return [title, segments, serializeDocument(doc)];
}

function selfOrAncestorIn(node, names) {
  if (names.has(node?.name)) return true;
  return ancestors(node).some((a) => names.has(a.name));
}

// ---------- fragment → 锚点映射 ----------

/** template 内 id/name → 所属块锚点（null 表示在最后可译块之后）。 */
export function fragmentAnchorMap(templateHtml) {
  const doc = parseHTML(templateHtml);
  const mapping = new Map();
  const allElements = findAllElements(doc, () => true);
  const withAnchor = (node) => node.attribs?.["data-tn-id"];
  for (const node of allElements) {
    for (const key of ["id", "name"]) {
      const value = node.attribs?.[key];
      if (typeof value !== "string" || value === "") continue;
      if (mapping.has(value)) continue;
      let anchor = null;
      if (withAnchor(node)) {
        anchor = node.attribs["data-tn-id"];
      } else {
        const anc = [node, ...ancestors(node)].find((a) => withAnchor(a));
        if (anc != null) anchor = anc.attribs["data-tn-id"];
        else {
          // 向后找下一个带 data-tn-id 的元素
          const idx = allElements.indexOf(node);
          for (let j = idx + 1; j < allElements.length; j++) {
            if (withAnchor(allElements[j])) {
              anchor = allElements[j].attribs["data-tn-id"];
              break;
            }
          }
        }
      }
      mapping.set(value, anchor ?? null);
    }
  }
  return mapping;
}

// ---------- 逻辑章切分 ----------

export class TopLevelTocStrategy {
  get name() {
    return "top-level-toc";
  }

  select(tocEntries) {
    const byPosition = new Map();
    for (const entry of tocEntries) {
      if (entry.depth !== 0) continue;
      if (entry.external) continue;
      if (!Number.isInteger(entry.boundary_position) || entry.boundary_position < 0) continue;
      const existing = byPosition.get(entry.boundary_position);
      if (existing == null) {
        byPosition.set(entry.boundary_position, entry);
      } else if (entry.segment_anchor != null || existing.segment_anchor == null) {
        byPosition.set(entry.boundary_position, entry);
      }
    }
    return [...byPosition.values()];
  }
}

export function getChapterSplitStrategy() {
  return new TopLevelTocStrategy();
}

/** 把物理资源流按目录边界切成逻辑 Chapter。返回 [chapters, 策略名, 规范目录路径]。 */
export function logicalChapters(resources, tocEntries) {
  const allSegments = [];
  const anchorPositions = new Map();
  const resourceStarts = new Map();
  const resourceByHref = new Map();
  for (const resource of resources) {
    resourceStarts.set(resource.href, allSegments.length);
    resourceByHref.set(resource.href, resource);
    for (const seg of resource.segments) {
      if (!(seg instanceof Segment)) continue;
      if (seg.anchor != null) anchorPositions.set(seg.anchor, allSegments.length);
      allSegments.push(seg);
    }
  }

  // 定位目录边界
  for (const entry of tocEntries) {
    const href = entry.resource_href;
    if (!href || !resourceStarts.has(href)) continue;
    const resource = resourceByHref.get(href);
    const fragment = entry.fragment;
    if (fragment && !resource.fragment_anchors.has(fragment)) continue; // 坏 fragment 跳过
    let segmentAnchor = null;
    if (fragment) {
      segmentAnchor = resource.fragment_anchors.get(fragment) ?? null;
    } else {
      segmentAnchor = resource.segments[0]?.anchor ?? null;
    }
    if (segmentAnchor != null && anchorPositions.has(segmentAnchor)) {
      entry.segment_anchor = segmentAnchor;
      entry.boundary_position = anchorPositions.get(segmentAnchor);
      continue;
    }
    if (fragment) {
      // fragment 在最后可译块之后
      entry.boundary_position = resourceStarts.get(href) + resource.segments.length;
    } else if (resource.segments.length === 0) {
      // 无文字标题页也是有效目录边界
      entry.boundary_position = resourceStarts.get(href);
    }
  }

  // 分组节点继承（同 toc_path 内逆序）
  const byPath = new Map();
  tocEntries.forEach((entry, i) => {
    if (!byPath.has(entry.toc_path)) byPath.set(entry.toc_path, []);
    byPath.get(entry.toc_path).push(i);
  });
  for (const [, indices] of byPath) {
    const childrenOf = new Map(); // parent_index -> [entry...]
    for (const i of indices) {
      const p = tocEntries[i].parent_index;
      if (p == null) continue;
      if (!childrenOf.has(p)) childrenOf.set(p, []);
      childrenOf.get(p).push(tocEntries[i]);
    }
    for (let k = indices.length - 1; k >= 0; k--) {
      const entry = tocEntries[indices[k]];
      if (entry.boundary_position != null) continue;
      if (entry.raw_href) continue;
      const children = childrenOf.get(indices[k]) ?? [];
      const child = children.find((c) => c.boundary_position != null);
      if (child != null) {
        entry.boundary_position = child.boundary_position;
        entry.inherited_boundary_from = child.entry_id;
      }
    }
  }

  // 策略选择：第一份能产出候选边界的目录
  const strategy = getChapterSplitStrategy();
  const orderedPaths = [...new Set(tocEntries.map((e) => e.toc_path))];
  let canonicalTocPath = "";
  let boundaries = [];
  for (const p of orderedPaths) {
    const selected = strategy.select(tocEntries.filter((e) => e.toc_path === p));
    if (selected.length > 0) {
      canonicalTocPath = p;
      boundaries = selected;
      break;
    }
  }
  boundaries = [...boundaries].sort((a, b) => a.boundary_position - b.boundary_position);
  for (const b of boundaries) {
    if (!Number.isInteger(b.boundary_position)) {
      throw new Error("EPUB chapter boundary is missing an integer position");
    }
  }

  // 无边界回退：每非空 spine 资源一章
  if (boundaries.length === 0) {
    const chapters = [];
    for (const resource of resources) {
      if (resource.segments.length === 0) continue;
      const segs = resource.segments.map((s, i) => reindex(s, i));
      chapters.push(
        new Chapter({
          index: chapters.length,
          title: resource.title,
          segments: segs,
          href: resource.href,
          template: null,
          meta: { epub_split_strategy: "spine-fallback" },
        }),
      );
    }
    return [chapters, "spine-fallback", canonicalTocPath];
  }

  // 切片
  const slices = [];
  const first = boundaries[0].boundary_position;
  if (first > 0) slices.push([0, first, null]);
  for (let i = 0; i < boundaries.length; i++) {
    const start = boundaries[i].boundary_position;
    const end = i + 1 < boundaries.length ? boundaries[i + 1].boundary_position : allSegments.length;
    if (start === end) continue;
    slices.push([start, end, boundaries[i]]);
  }
  const chapters = [];
  for (const [start, end, boundary] of slices) {
    const slice = allSegments.slice(start, end);
    if (slice.length === 0) continue;
    const segs = slice.map((s, i) => reindex(s, i));
    let title;
    let meta;
    let firstHref;
    if (boundary != null) {
      title = boundary.title ?? "";
      meta = { epub_split_strategy: strategy.name };
      if (typeof boundary.entry_id === "string") meta.toc_entry_id = boundary.entry_id;
      firstHref = segs[0].resource_href ?? boundary.resource_href ?? null;
    } else {
      title = segs[0].kind === KIND_HEADING ? segs[0].source : "";
      meta = { epub_split_strategy: strategy.name };
      firstHref = segs[0].resource_href ?? null;
    }
    chapters.push(
      new Chapter({ index: chapters.length, title, segments: segs, href: firstHref ?? null, template: null, meta }),
    );
  }
  return [chapters, strategy.name, canonicalTocPath];
}

function reindex(seg, i) {
  const s = new Segment({ ...seg, meta: structuredClone(seg.meta ?? {}) });
  s.index = i;
  return s;
}

// ---------- read_epub ----------

export async function readEpub(filePath, sourceLang, targetLang) {
  const files = await openZip(filePath);
  const opfPath = findOpfPath(files);
  const [bookTitle, hrefs, tocPaths] = parseOpf(files, opfPath);
  const tocEntries = parseTocEntries(files, tocPaths);
  const resources = [];
  let resourceIndex = 0;
  for (const href of hrefs) {
    if (!files.has(href)) continue;
    const html = decodeMarkup(files.get(href));
    const [title, segments, template] = annotateEpubResource(html, resourceIndex, href, {
      bookTitle,
      skipNavigation: tocPaths.includes(href),
    });
    resources.push({
      index: resourceIndex,
      href,
      title,
      segments,
      template,
      fragment_anchors: fragmentAnchorMap(template),
    });
    resourceIndex += 1;
  }
  const [chapters, splitStrategy, splitTocPath] = logicalChapters(resources, tocEntries);
  // 状态瘦身：模板不落盘；epub_inline 弹掉（可从原 EPUB 确定性重建）
  for (const chapter of chapters) {
    chapter.template = null;
    for (const seg of chapter.segments) delete seg.meta[_INLINE_META_KEY];
  }
  const stem = path.basename(filePath).replace(/\.[^.]*$/, "");
  return new Document({
    title: bookTitle || stem,
    source_lang: sourceLang,
    target_lang: targetLang,
    fmt: "epub",
    source_path: path.resolve(filePath),
    chapters,
    meta: {
      epub_schema: 4,
      opf_path: opfPath,
      toc_paths: tocPaths,
      toc_entries: tocEntries,
      epub_resources: resources.map((r) => ({ index: r.index, href: r.href })),
      epub_split_strategy: splitStrategy,
      epub_split_toc_path: splitTocPath,
    },
  });
}
