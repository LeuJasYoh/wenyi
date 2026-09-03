// wenyi-node 命令分派。
export const VERSION = "0.4.1";

export async function main(argv) {
  const [group, sub, ...rest] = argv;
  if (!group || group === "--help" || group === "-h") {
    printHelp();
    return 0;
  }
  if (group === "--version" || group === "-v") {
    console.log(VERSION);
    return 0;
  }
  if (group === "doc") {
    if (sub === "parse") {
      const { cmdDocParse } = await import("./doc/cmd-parse.js");
      return cmdDocParse(rest);
    }
    if (sub === "assemble") {
      const { cmdDocAssemble } = await import("./doc/cmd-assemble.js");
      return cmdDocAssemble(rest);
    }
  }
  if (group === "web" && sub === "serve") {
    const { cmdWebServe } = await import("./web/serve.js");
    return cmdWebServe(rest);
  }
  console.error(`未知命令：${argv.join(" ")}（可用：doc parse|assemble、web serve、--help、--version）`);
  return 2;
}

function printHelp() {
  console.log(`wenyi-node ${VERSION}

用法：
  node cli.js doc parse    --input X --state-dir Y [--split N] [--lang SRC,TGT]
  node cli.js doc assemble --input X --state-dir Y [--format F] [--bilingual] ...
  node cli.js web serve    [--port 8731] [--no-open]
  node cli.js --version`);
}
