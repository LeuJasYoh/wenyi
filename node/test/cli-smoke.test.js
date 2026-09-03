import { test } from "node:test";
import assert from "node:assert/strict";
import { main, VERSION } from "../src/cli-main.js";

test("--version 输出版本", async () => {
  let out = "";
  const orig = console.log;
  console.log = (s) => (out = s);
  try {
    const code = await main(["--version"]);
    assert.equal(code, 0);
  } finally {
    console.log = orig;
  }
  assert.equal(out, VERSION);
});

test("未知命令退出码 2", async () => {
  const orig = console.error;
  console.error = () => {};
  try {
    const code = await main(["bogus"]);
    assert.equal(code, 2);
  } finally {
    console.error = orig;
  }
});
