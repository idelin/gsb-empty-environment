import { test } from "node:test";
import assert from "node:assert/strict";
import {
  createDefaultMatrix,
  enumerateKeys,
  lookup,
  serializeKey,
  validateMatrix,
} from "../index.ts";

test("默认矩阵覆盖全部 648 个有限组合", () => {
  const matrix = createDefaultMatrix();
  const keys = enumerateKeys();
  assert.equal(keys.length, 3 * 3 * 3 * 3 * 4 * 2);
  assert.deepEqual(validateMatrix(matrix), []);
  for (const key of keys) {
    assert.ok(
      lookup(matrix, key) === "allow" || lookup(matrix, key) === "deny",
    );
  }
});

test("validateMatrix 对完备矩阵返回空错误列表", () => {
  const errors = validateMatrix(createDefaultMatrix());
  assert.deepEqual(errors, []);
});

test("validateMatrix 能发现缺失组合与未知组合", () => {
  const matrix = createDefaultMatrix() as Record<string, string>;
  const firstKey = serializeKey(enumerateKeys()[0]);
  delete matrix[firstKey];
  matrix["bogus|key"] = "allow";
  const errors = validateMatrix(matrix);
  assert.ok(errors.some((e) => e.includes("缺少策略组合")));
  assert.ok(errors.some((e) => e.includes("未知策略组合")));
});

test("同源响应在任意嵌入策略下均获许", () => {
  const matrix = createDefaultMatrix();
  for (const key of enumerateKeys()) {
    if (key.relationship !== "same-origin") {
      continue;
    }
    assert.equal(lookup(matrix, key), "allow", serializeKey(key));
  }
});

test("顶层导航不受 COEP 嵌入门控约束", () => {
  const matrix = createDefaultMatrix();
  for (const key of enumerateKeys()) {
    if (key.resourceMode !== "navigate" || key.relationship === "same-origin") {
      continue;
    }
    assert.equal(lookup(matrix, key), "allow", serializeKey(key));
  }
});

test("require-corp 下跨源 no-cors 必须由 CORP 显式获许", () => {
  const matrix = createDefaultMatrix();
  for (const key of enumerateKeys()) {
    if (
      key.resourceMode !== "no-cors" ||
      key.embedding !== "require-corp" ||
      key.relationship === "same-origin"
    ) {
      continue;
    }
    const expected =
      key.corp === "cross-origin" ||
      (key.corp === "same-site" && key.relationship === "same-site") ||
      (key.corp === "same-origin" && key.relationship === "same-origin");
    assert.equal(
      lookup(matrix, key),
      expected ? "allow" : "deny",
      serializeKey(key),
    );
  }
});

test("CORS 模式仅在校验通过时获许", () => {
  const matrix = createDefaultMatrix();
  for (const key of enumerateKeys()) {
    if (key.resourceMode !== "cors" || key.relationship === "same-origin") {
      continue;
    }
    assert.equal(
      lookup(matrix, key),
      key.corsAccess ? "allow" : "deny",
      serializeKey(key),
    );
  }
});

test("unsafe-none 未启用隔离时跨源 no-cors 允许", () => {
  const matrix = createDefaultMatrix();
  for (const key of enumerateKeys()) {
    if (
      key.resourceMode === "no-cors" &&
      key.embedding === "unsafe-none" &&
      key.relationship !== "same-origin"
    ) {
      assert.equal(lookup(matrix, key), "allow", serializeKey(key));
    }
  }
});

test("矩阵是纯函数：同一键多次查找结果一致", () => {
  const matrix = createDefaultMatrix();
  for (const key of enumerateKeys()) {
    const first = lookup(matrix, key);
    for (let i = 0; i < 5; i += 1) {
      assert.equal(lookup(matrix, key), first);
    }
  }
});
