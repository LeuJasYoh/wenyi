// 单文件 HTML 解析（主规格 §7.3 / 分册 03 §1.8）。
import { promises as fs } from "node:fs";
import path from "node:path";
import { parseHTML, findAllElements, isTag, isText, isComment, serializeNode, getText } from "./dom.js";
import { HEADING_TAGS } from "./dom.js";
import { KIND_TEXT, Chapter, Document, Segment } from "./models.js";
import { annotateEpubResource } from "./epub-reader.js";

const _DEFAULT_CHAPTER_TAGS = new Set([...HEADING_TAGS]);
const _MEDIA_TAGS = ["img", "picture", "svg", "object", "video", "audio", "canvas", "iframe"];

/** 委托 annotate_epub_resource（假资源路径），随后清空 resource_href。 */
export function extractChapter(html, chapterIndex, { chapterTitle = "" } = {}) {
  const [detectedTitle, segments, template] = annotateEpubResource(
    html,
    chapterIndex,
    `html-chapter-${chapterIndex}.xhtml`,
  );
  for (const seg of segments) seg.resource_href = null;
  const title = chapterTitle.trim() || detectedTitle;
  return [title, segments, template];
}

export async function readHtml(filePath, sourceLang, targetLang, { chapterTags = _DEFAULT_CHAPTER_TAGS, encoding = "utf-8" } = {}) {
  const buffer = await fs.readFile(filePath);
  let content;
  if (encoding === "utf-8") {
    content = buffer.toString("utf-8");
  } else {
    const iconv = (await import("iconv-lite")).default;
    content = iconv.decode(buffer, encoding);
  }
  const doc = parseHTML(content);
  const bookTitle = path.basename(filePath).replace(/\.[^.]*$/, "");
  const headTag = findAllElements(doc, (n) => n.name === "head")[0];
  const headHtml = headTag ? (headTag.children ?? []).map(serializeNode).join("") : "";
  const body = findAllElements(doc, (n) => n.name === "body")[0] ?? doc;
  const children = body.children ?? [];

  // 章边界：连续标题只取第一个断章
  const boundaries = [];
  if (chapterTags && chapterTags.size > 0) {
    let i = 0;
    for (const child of children) {
      i++;
      if (!isTag(child) || !chapterTags.has(child.name)) continue;
      // 回溯找前一个非空白元素
      let prevIsHeading = false;
      let determined = false;
      for (let j = children.indexOf(child) - 1; j >= 0; j--) {
        const prev = children[j];
        if (isText(prev) && prev.data.trim() === "") continue;
        if (isComment(prev)) continue;
        if (isTag(prev)) {
          prevIsHeading = chapterTags.has(prev.name);
          determined = true;
        }
        break; // 非空白元素（Tag 或非空文本）
      }
      if (!determined) prevIsHeading = false;
      if (!prevIsHeading) boundaries.push(i - 1);
    }
  }

  // 区间
  const ranges = [];
  if (boundaries.length === 0) {
    ranges.push([0, children.length]);
  } else {
    if (boundaries[0] > 0) ranges.push([0, boundaries[0]]);
    for (let k = 0; k < boundaries.length; k++) {
      const start = boundaries[k];
      const end = k + 1 < boundaries.length ? boundaries[k + 1] : children.length;
      ranges.push([start, end]);
    }
  }

  const chapters = [];
  for (const [start, end] of ranges) {
    const slice = children.slice(start, end);
    const fragmentHtml = slice.map(serializeNode).join("");
    // 章标题：首子元素是 heading 时收集连续 heading，" / " 连接
    let chapterTitle = "";
    const first = slice.find((c) => !(isText(c) && !c.data.trim()) && !isComment(c));
    if (isTag(first) && HEADING_TAGS.has(first.name)) {
      const titles = [];
      for (const c of slice) {
        if (isText(c) && !c.data.trim()) continue;
        if (isTag(c) && HEADING_TAGS.has(c.name)) {
          titles.push(getText(c).split(/\s+/).join(" "));
        } else {
          break;
        }
      }
      chapterTitle = titles.join(" / ");
    }
    const [title, segments, template] = extractChapter(fragmentHtml, chapters.length, { chapterTitle });
    // 空章跳过：无文字段且无可见媒体
    const hasText = segments.some((s) => s.source.trim());
    const parsed = parseHTML(template);
    const hasMedia = _MEDIA_TAGS.some((t) => findAllElements(parsed, (n) => n.name === t).length > 0);
    if (!hasText && !hasMedia) continue;
    chapters.push(
      new Chapter({
        index: chapters.length,
        title,
        segments,
        href: null,
        template,
      }),
    );
  }
  return new Document({
    title: bookTitle,
    source_lang: sourceLang,
    target_lang: targetLang,
    fmt: "html",
    source_path: path.resolve(filePath),
    chapters,
    meta: {
      chapter_tags: chapterTags ? [...chapterTags] : null,
      head_html: headHtml,
    },
  });
}
