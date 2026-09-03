// doc parse 子命令：解析输入 → source/doc.json + chapters/ch{N}.json（不写 manifest）。
import { promises as fs } from "node:fs";
import path from "node:path";
import { loadDocument } from "./segmenter.js";

function parseArgs(argv) {
  const opts = { input: null, stateDir: null, split: 0, lang: "auto,zh" };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const next = () => argv[++i];
    if (a === "--input" || a === "-i") opts.input = next();
    else if (a.startsWith("--input=")) opts.input = a.slice(8);
    else if (a === "--state-dir" || a === "--state") opts.stateDir = next();
    else if (a.startsWith("--state-dir=")) opts.stateDir = a.slice(12);
    else if (a === "--split") opts.split = Number(next());
    else if (a.startsWith("--split=")) opts.split = Number(a.slice(8));
    else if (a === "--lang") opts.lang = next();
    else if (a.startsWith("--lang=")) opts.lang = a.slice(7);
    else {
      console.error(`未知参数：${a}`);
    }
  }
  return opts;
}

export async function cmdDocParse(argv) {
  const opts = parseArgs(argv);
  if (!opts.input || !opts.stateDir) {
    console.error("用法：doc parse --input <文件> --state-dir <目录> [--split N] [--lang SRC,TGT]");
    return 2;
  }
  const [sourceLang, targetLang] = opts.lang.split(",");
  try {
    const doc = await loadDocument(opts.input, sourceLang || "auto", targetLang || "zh", opts.split, {
      cacheDir: path.join(opts.stateDir, "source"),
    });
    const sourceDir = path.join(opts.stateDir, "source");
    const chaptersDir = path.join(opts.stateDir, "chapters");
    await fs.mkdir(sourceDir, { recursive: true });
    await fs.mkdir(chaptersDir, { recursive: true });
    await fs.writeFile(path.join(sourceDir, "doc.json"), JSON.stringify(doc.toDict(), null, 2) + "\n", "utf-8");
    for (const chapter of doc.chapters) {
      await fs.writeFile(
        path.join(chaptersDir, `ch${chapter.index}.json`),
        JSON.stringify(chapter.toDict(), null, 2) + "\n",
        "utf-8",
      );
    }
    console.log(`已解析 ${doc.chapters.length} 章 → ${opts.stateDir}`);
    return 0;
  } catch (err) {
    console.error(err?.message ?? String(err));
    return 1;
  }
}
