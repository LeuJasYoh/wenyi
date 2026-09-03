// 纯文本 / Markdown 解析（主规格 §7.2 / 分册 03 §1.9）。
import { promises as fs } from "node:fs";
import path from "node:path";
import { KIND_HEADING, KIND_TEXT, Chapter, Document, Segment } from "./models.js";

const _MD_HEADING = /^(#{1,3})\s+(.*\S)\s*$/;
const _JA_CHAPTER = /^\s*(?:第[0-9０-９一二三四五六七八九十百千]+[章話节節回部巻]|序章|終章|序幕|終幕|プロローグ|エピローグ|あとがき|まえがき)/;

/** 标题行识别：MD 命中 → [title, level]；日文标记 → [line, 1]；否则 null。 */
export function isChapterHeading(line) {
  const md = line.match(_MD_HEADING);
  if (md) return [md[2].trim(), md[1].length];
  if (_JA_CHAPTER.test(line)) return [line.trim(), 1];
  return null;
}

/** 空行分段；块内单换行保留。 */
export function splitParagraphs(block) {
  return block
    .split(/\n\s*\n/)
    .map((b) => b.replace(/^\n+|\n+$/g, ""))
    .filter((b) => b !== "");
}

export async function readText(filePath, sourceLang, targetLang) {
  const content = await fs.readFile(filePath, "utf-8");
  const bookTitle = path.basename(filePath).replace(/\.[^.]*$/, "");
  // (explicit_title | null, level, body_lines)
  const chaptersRaw = [];
  let currentTitle = null;
  let currentLevel = 0;
  let currentBody = [];
  for (const line of content.split("\n")) {
    const heading = isChapterHeading(line);
    if (heading != null) {
      chaptersRaw.push([currentTitle, currentLevel, currentBody]);
      currentTitle = heading[0];
      currentLevel = heading[1];
      currentBody = [];
    } else {
      currentBody.push(line);
    }
  }
  chaptersRaw.push([currentTitle, currentLevel, currentBody]);

  const chapters = [];
  for (const [explicitTitle, level, bodyLines] of chaptersRaw) {
    const title = explicitTitle ?? bookTitle;
    const segments = [];
    let idx = 0;
    if (explicitTitle != null) {
      segments.push(new Segment({ index: idx++, source: title, kind: KIND_HEADING }));
    }
    for (const para of splitParagraphs(bodyLines.join("\n"))) {
      segments.push(new Segment({ index: idx++, source: para, kind: KIND_TEXT }));
    }
    if (segments.length === 0) continue;
    chapters.push(
      new Chapter({
        index: chapters.length,
        title,
        segments,
        meta: { heading_level: level },
      }),
    );
  }
  return new Document({
    title: bookTitle,
    source_lang: sourceLang,
    target_lang: targetLang,
    fmt: "text",
    source_path: path.resolve(filePath),
    chapters,
  });
}
