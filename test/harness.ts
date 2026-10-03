/**
 * 测试入口统一封装：测试文件按 Node 内置 node:test 的标准 API 编写。
 * Node >= 18 直接使用内置 node:test；旧版本 Node（如本机 v16）自动降级到
 * 仓库内置的兼容执行器 fallback-runner（同一套 test/describe/it API）。
 */
declare const require: (id: string) => any;

interface TestModule {
  test: (name: string, fn: () => unknown) => void;
  describe?: (name: string, fn: () => unknown) => void;
  it?: (name: string, fn: () => unknown) => void;
}

let mod: TestModule;
try {
  mod = require('node:test');
} catch {
  mod = require('./fallback-runner');
}

export const test = mod.test;
export const describe: (name: string, fn: () => unknown) => void = mod.describe ?? mod.test;
export const it: (name: string, fn: () => unknown) => void = mod.it ?? mod.test;
