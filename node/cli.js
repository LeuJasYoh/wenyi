#!/usr/bin/env node
// wenyi-node 入口：node cli.js <doc|web> ...
// 子命令契约见架构分册 §3.2 与分册 10（WebUI）。
import { main } from "./src/cli-main.js";

main(process.argv.slice(2)).then(
  (code) => process.exit(code ?? 0),
  (err) => {
    console.error(err?.message ?? String(err));
    process.exit(1);
  },
);
