/**
 * node:test 兼容运行器。
 *
 * 测试文件使用标准的 node:test / node:assert 内置 API 编写：
 *  - Node.js >= 18：可直接 `node --test dist/test/` 运行；
 *  - 更老的 Node（如本机 Node 16，无 node:test 内建模块）：
 *    本运行器拦截 require('node:test')，提供最小兼容实现后依次执行。
 *
 * 用法：node test/compat-runner.cjs
 */
"use strict";

const fs = require("fs");
const path = require("path");
const Module = require("module");

let hasBuiltin = true;
try {
  require("node:test");
} catch (_) {
  hasBuiltin = false;
}

if (hasBuiltin) {
  console.log("检测到内建 node:test，建议使用: node --test dist/test/");
}

const tests = [];
const suiteStack = [];

function fullName(name) {
  return suiteStack.concat([name]).join(" > ");
}

const shim = {
  test(name, fn) {
    tests.push({ name: fullName(name), fn });
  },
  describe(name, fn) {
    suiteStack.push(name);
    fn();
    suiteStack.pop();
  },
};
shim.it = shim.test;

const originalLoad = Module._load;
Module._load = function (request) {
  if (request === "node:test") return shim;
  return originalLoad.apply(this, arguments);
};

const testDir = path.join(__dirname, "..", "dist", "test");
const files = fs
  .readdirSync(testDir)
  .filter((f) => f.endsWith(".test.js"))
  .sort();

if (files.length === 0) {
  console.error("未找到编译后的测试文件，请先运行: npm run build");
  process.exit(1);
}

for (const file of files) {
  require(path.join(testDir, file));
}

(async () => {
  let pass = 0;
  let fail = 0;
  for (const t of tests) {
    try {
      await t.fn();
      pass++;
      console.log(`ok ${pass + fail} - ${t.name}`);
    } catch (err) {
      fail++;
      console.log(`not ok ${pass + fail} - ${t.name}`);
      console.error(err && err.stack ? err.stack : err);
    }
  }
  console.log(`\n# tests ${pass + fail}`);
  console.log(`# pass ${pass}`);
  console.log(`# fail ${fail}`);
  process.exitCode = fail === 0 ? 0 : 1;
})();
