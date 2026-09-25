import { test } from "node:test";
import assert from "node:assert/strict";
import { link } from "../src/linker.ts";
import { ModuleCache } from "../src/cache.ts";
import type { ModuleSource } from "../src/types.ts";

function worldWithB(bVersion: string): ModuleSource[] {
  return [
    {
      name: "b",
      version: bVersion,
      imports: [],
      exports: [{ name: "x", kind: "decl", signature: "number", decl: 1 }],
    },
    {
      name: "a",
      version: "1.0.0",
      imports: [{ module: "b", version: bVersion, names: [{ name: "x" }], loc: "a.ts:1:1" }],
      exports: [{ name: "x2", kind: "value", signature: "number", make: (ctx) => (ctx.get("x") as number) * 2 }],
    },
  ];
}

test("缓存按完整依赖版本向量隔离，不能跨版本混用", () => {
  const w1 = link(worldWithB("1.0.0"));
  const w2 = link(worldWithB("2.0.0"));
  const a1 = w1.get("a@1.0.0");
  const a2 = w2.get("a@1.0.0");
  assert.ok(a1 && a2);

  // 同一个 a@1.0.0，但传递依赖 b 的版本不同 => 版本向量不同
  assert.match(a1.versionVector, /b@1\.0\.0/);
  assert.match(a2.versionVector, /b@2\.0\.0/);
  assert.notEqual(ModuleCache.keyOf(a1), ModuleCache.keyOf(a2));

  const cache = new ModuleCache();
  cache.set(a1, "computed", "result-for-b1");
  assert.equal(cache.has(a2, "computed"), false, "b@2 世界中的 a 不得命中 b@1 世界的缓存");
  assert.equal(cache.get(a1, "computed"), "result-for-b1", "原向量缓存仍然有效");

  cache.set(a2, "computed", "result-for-b2");
  assert.equal(cache.get(a1, "computed"), "result-for-b1", "两个版本向量的缓存互不污染");
  assert.equal(cache.get(a2, "computed"), "result-for-b2");
});
