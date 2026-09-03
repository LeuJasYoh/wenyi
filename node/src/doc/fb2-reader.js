// FB2 (FictionBook XML) 解析（主规格 §7.4 / 分册 03 §1.7）。
import { promises as fs } from "node:fs";
import path from "node:path";
import { parseXML, findAllElements, directChildren, localName, isTag, isText } from "./dom.js";
import { KIND_HEADING, KIND_TEXT, Chapter, Document, Segment } from "./models.js";
import { decodeFB2 } from "./encoding.js";

const _CONTAINER_BLOCKS = new Set(["epigraph", "cite", "poem", "stanza", "title", "annotation"]);

function local(el) {
  return localName(el.name);
}

/** 保留内部空白的全文（等价 "".join(el.itertext())）。 */
function stripMarkup(el) {
  let out = "";
  const visit = (node) => {
    if (isText(node)) out += node.data;
    for (const c of node.children ?? []) visit(c);
  };
  visit(el);
  return out;
}

/** xlink:href 属性值（去掉 # 前缀）。 */
function imageId(el) {
  for (const [k, v] of Object.entries(el.attribs ?? {})) {
    if (localName(k) === "href") return String(v).replace(/^#/, "").trim();
  }
  return "";
}

/** 提取本 section 的直接内容（不下钻子 section）。返回 [标题文本, segments, images]。 */
function directSegments(section, chapterIndex) {
  let idx = 0;
  const segments = [];
  const images = [];
  let titleText = "";
  const add = (text, kind) => {
    if (text.trim() === "") return;
    segments.push(
      new Segment({ index: idx, source: text, kind, anchor: `tn${chapterIndex}_${idx}` }),
    );
    idx += 1;
  };
  const emitBlock = (el) => {
    const name = local(el);
    if (name === "image") {
      const id = imageId(el);
      if (id) images.push({ id, position: segments.length });
      return;
    }
    if (name === "subtitle") {
      add(stripMarkup(el), KIND_HEADING);
      return;
    }
    if (name === "p" || name === "v" || name === "text-author") {
      for (const d of findAllElements(el, (n) => localName(n.name) === "image")) emitBlock(d);
      add(stripMarkup(el), KIND_TEXT);
      return;
    }
    if (_CONTAINER_BLOCKS.has(name)) {
      for (const child of directChildren(el)) emitBlock(child);
    }
    // 其它（empty-line 等）跳过
  };
  for (const child of directChildren(section)) {
    const name = local(child);
    if (name === "section") continue;
    if (name === "title") {
      titleText = stripMarkup(child).trim();
      add(titleText, KIND_HEADING);
    } else {
      emitBlock(child);
    }
  }
  return [titleText, segments, images];
}

function makeChapter(ci, titleText, segments, images) {
  let title = titleText;
  if (!title && segments.length > 0) title = Array.from(segments[0].source).slice(0, 80).join("");
  if (!title) title = `第${ci + 1}章`;
  const meta = {};
  if (images.length > 0) meta.fb2_images = images;
  return new Chapter({ index: ci, title, segments, meta });
}

function walkSections(section, chapters) {
  const ci = chapters.length;
  const [titleText, segs, images] = directSegments(section, ci);
  const childSections = directChildren(section).filter((c) => local(c) === "section");
  if (childSections.length > 0) {
    const hasBody = segs.some((s) => s.kind === KIND_TEXT);
    if (hasBody || titleText) chapters.push(makeChapter(ci, titleText, segs, images));
    for (const child of childSections) walkSections(child, chapters);
  } else if (segs.length > 0) {
    chapters.push(makeChapter(ci, titleText, segs, images));
  }
}

/** 正文 <body><title> 解析为独立标题页章节。 */
function bodyTitleChapter(body) {
  const title = directChildren(body).find((c) => local(c) === "title") ?? null;
  if (!title) return null;
  const lines = directChildren(title)
    .filter((c) => local(c) === "p")
    .map((c) => stripMarkup(c).trim())
    .filter((t) => t !== "");
  if (lines.length === 0) return null;
  const segments = lines.map((line, idx) => new Segment({ index: idx, source: line, kind: KIND_HEADING, anchor: `tn0_${idx}` }));
  return new Chapter({ index: 0, title: lines[lines.length - 1], segments });
}

export async function readFb2(filePath, sourceLang, targetLang) {
  const raw = await fs.readFile(filePath);
  const text = decodeFB2(raw);
  const root = parseXML(text).children.find(isTag);
  if (root == null) throw new Error("FB2 损坏：无法解析 XML 根元素");

  // binary 资源清单
  const resources = [];
  for (const el of findAllElements(root, () => true)) {
    if (local(el) !== "binary") continue;
    const id = el.attribs?.["id"];
    if (!id) continue;
    resources.push({ id, content_type: el.attribs?.["content-type"] ?? "application/octet-stream" });
  }
  // 封面
  let coverImage = "";
  outer: for (const el of findAllElements(root, (n) => localName(n.name) === "coverpage")) {
    for (const img of findAllElements(el, (n) => localName(n.name) === "image")) {
      coverImage = imageId(img);
      break outer;
    }
  }
  // 书名
  let bookTitle = path.basename(filePath).replace(/\.[^.]*$/, "");
  for (const ti of findAllElements(root, (n) => localName(n.name) === "title-info")) {
    const bt = directChildren(ti).find((c) => local(c) === "book-title");
    if (bt && stripMarkup(bt)) {
      bookTitle = stripMarkup(bt);
      break;
    }
  }
  // 章节：第一个无 name 的 body
  const chapters = [];
  for (const child of directChildren(root)) {
    if (local(child) !== "body") continue;
    if (child.attribs?.["name"]) continue;
    const titlePage = bodyTitleChapter(child);
    if (titlePage) chapters.push(titlePage);
    for (const section of directChildren(child)) {
      if (local(section) === "section") walkSections(section, chapters);
    }
    break;
  }
  // 兜底：全部 p 成一段一章
  if (chapters.length === 0) {
    const ps = findAllElements(root, (n) => localName(n.name) === "p");
    const segs = ps.map((p, idx) => new Segment({ index: idx, source: stripMarkup(p), kind: KIND_TEXT, anchor: `tn0_${idx}` }));
    if (segs.length > 0) {
      chapters.push(new Chapter({ index: 0, title: segs[0].source, segments: segs }));
    }
  }
  const meta = {};
  if (resources.length > 0) meta.fb2_resources = resources;
  if (coverImage) meta.fb2_cover_image = coverImage;
  return new Document({
    title: bookTitle,
    source_lang: sourceLang,
    target_lang: targetLang,
    fmt: "fb2",
    source_path: path.resolve(filePath),
    chapters,
    meta,
  });
}

/** 导出用：读回 binary 资源（base64 解码）。 */
export async function readFb2Binaries(filePath) {
  const raw = await fs.readFile(filePath);
  const text = decodeFB2(raw);
  const root = parseXML(text).children.find(isTag);
  const out = {};
  if (root == null) return out;
  for (const el of findAllElements(root, () => true)) {
    if (local(el) !== "binary") continue;
    const id = el.attribs?.["id"];
    let encoded = "";
    for (const node of el.children ?? []) if (isText(node)) encoded += node.data;
    encoded = encoded.split(/\s+/).join("");
    if (!id || !encoded) continue;
    try {
      out[id] = [el.attribs?.["content-type"] ?? "application/octet-stream", Buffer.from(encoded, "base64")];
    } catch {
      // 非法 base64 跳过
    }
  }
  return out;
}
