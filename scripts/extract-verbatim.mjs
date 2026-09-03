#!/usr/bin/env node
// 从分册 markdown 提取逐字文本（prompt 模板等）到 wenyi-multi 的嵌入目录。
// 用法: node scripts/extract-verbatim.mjs
// 规则: 每个目标由「锚点正则」定位，取其后第一个代码围栏的内容（去掉围栏结尾多出的一个换行）。
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const SPEC = "D:/Projects/wenyi-spec-export";
const OUT = join(dirname(fileURLToPath(import.meta.url)), "..", "internal", "agents", "promptdata");

// [文件, 锚点正则(行首匹配), 输出文件名]
const TARGETS = [
  ["分册/05-智能体模块精读.md", /^- \*\*`PUNCT_RULE`/, "punct_rule.txt"],
  ["分册/05-智能体模块精读.md", /^- \*\*`REVIEW_EVIDENCE_TOOLS`/, "review_evidence_tools.txt"],
  ["分册/01-CLI 与配置模块精读.md", /^### 3\.2 常量：`_DEFAULT_CONFIG_YAML`/, "default_config.yaml"],
];

// 分册 05 §4.3.1–4.3.27 的模板键（权威清单）
const TEMPLATE_KEYS = [
  "translator_system", "translator_user",
  "reviewer_system", "reviewer_user",
  "review_agent_system", "review_agent_user",
  "review_arbiter_system", "review_arbiter_user",
  "review_fixer_system", "review_fixer_user",
  "polisher_system", "polisher_user",
  "title_translator_system", "title_translator_user",
  "analyzer_system", "analyzer_user",
  "glossary_extractor_system", "glossary_extractor_user",
  "glossary_history_system", "glossary_user_placeholder_skip",
  "glossary_history_user",
  "backtranslate_system", "backtranslate_user",
  "consistency_system",
  "chapter_digest_system", "chapter_digest_user",
  "book_synopsis_system", "book_synopsis_user",
];

function extractBlocks(lines, anchorRe) {
  // 返回锚点行之后第一个代码围栏的内容（字符串）
  let i = lines.findIndex((l) => anchorRe.test(l));
  if (i < 0) throw new Error(`anchor not found: ${anchorRe}`);
  for (let j = i + 1; j < lines.length; j++) {
    if (lines[j].startsWith("```")) {
      const end = lines.findIndex((l, k) => k > j && l.startsWith("```"));
      if (end < 0) throw new Error(`fence not closed after ${anchorRe}`);
      return lines.slice(j + 1, end).join("\n");
    }
  }
  throw new Error(`no fence after ${anchorRe}`);
}

mkdirSync(OUT, { recursive: true });
const cache = new Map();
function linesOf(rel) {
  if (!cache.has(rel)) cache.set(rel, readFileSync(join(SPEC, rel), "utf8").split(/\r?\n/));
  return cache.get(rel);
}

for (const [rel, anchorRe, outName] of TARGETS) {
  const text = extractBlocks(linesOf(rel), anchorRe);
  writeFileSync(join(OUT, outName), text + "\n", "utf8");
  console.log(`extracted ${outName} (${text.length} chars)`);
}

// 提取 4.3.x 模板：按标题行逐个定位
const f05 = linesOf("分册/05-智能体模块精读.md");
const keyRe = /^#### 4\.3\.\d+ `([a-z_]+)`（Template）/;
const headings = [];
f05.forEach((l, idx) => {
  const m = l.match(keyRe);
  if (m) headings.push({ key: m[1], idx });
});
console.log(`found ${headings.length} template headings: ${headings.map((h) => h.key).join(", ")}`);
for (const h of headings) {
  // 找该标题之后、下一个 #### 或 ### 标题之前的第一个围栏
  let end = f05.findIndex((l, k) => k > h.idx && /^#{3,4} /.test(l));
  if (end < 0) end = f05.length;
  let captured = null;
  for (let j = h.idx + 1; j < end; j++) {
    if (f05[j].startsWith("```")) {
      let close = -1;
      for (let k = j + 1; k < end; k++) {
        if (f05[k].startsWith("```")) { close = k; break; }
      }
      if (close > j) { captured = f05.slice(j + 1, close).join("\n"); break; }
    }
  }
  if (captured == null) throw new Error(`no fence found for ${h.key}`);
  writeFileSync(join(OUT, `${h.key}.txt`), captured + "\n", "utf8");
}
console.log("done");
