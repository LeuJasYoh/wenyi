// doc assemble 子命令：从状态目录组装输出（主规格 §13）。
import { assemble } from "./writer.js";

function parseArgs(argv) {
  const opts = {
    input: null, stateDir: null, format: "epub", out: null,
    bilingual: false, order: "target_first", preserveSourceStyle: false,
    aboutPage: true,
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const next = () => argv[++i];
    if (a === "--input" || a === "-i") opts.input = next();
    else if (a.startsWith("--input=")) opts.input = a.slice(8);
    else if (a === "--state-dir" || a === "--state") opts.stateDir = next();
    else if (a.startsWith("--state-dir=")) opts.stateDir = a.slice(12);
    else if (a === "--format" || a === "-f") opts.format = next();
    else if (a.startsWith("--format=")) opts.format = a.slice(9);
    else if (a === "--out" || a === "-o") opts.out = next();
    else if (a.startsWith("--out=")) opts.out = a.slice(6);
    else if (a === "--bilingual") opts.bilingual = true;
    else if (a === "--order") opts.order = next();
    else if (a.startsWith("--order=")) opts.order = a.slice(8);
    else if (a === "--preserve-source-style") opts.preserveSourceStyle = true;
    else if (a === "--about-page") opts.aboutPage = true;
    else if (a === "--no-about-page") opts.aboutPage = false;
    else {
      console.error(`未知参数：${a}`);
      return { ...opts, __error: true };
    }
  }
  return opts;
}

export async function cmdDocAssemble(argv) {
  const opts = parseArgs(argv);
  if (opts.__error) return 2;
  if (!opts.input || !opts.stateDir) {
    console.error("用法：doc assemble --input <文件> --state-dir <目录> [--format F] [--out P] [--bilingual] [--order O] [--preserve-source-style] [--about-page|--no-about-page]");
    return 2;
  }
  const valid = ["epub", "txt", "html", "markdown"];
  if (!valid.includes(opts.format)) {
    console.error(`不支持的输出格式：${opts.format}（可选 ${valid.join("/")}）`);
    return 2;
  }
  try {
    const out = await assemble(opts.stateDir, opts.input, {
      outPath: opts.out,
      outFormat: opts.format,
      bilingual: opts.bilingual,
      order: opts.order,
      preserveSourceStyle: opts.preserveSourceStyle,
      aboutPage: opts.aboutPage,
    });
    console.log("OUTPUT: " + out);
    return 0;
  } catch (err) {
    console.error(err?.message ?? String(err));
    return 1;
  }
}
