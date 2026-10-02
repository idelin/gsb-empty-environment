import { test } from "node:test";
import assert from "node:assert/strict";
import { chunk, DEFAULTS } from "../src/chunker.ts";
import { pseudoRandomBytes } from "../src/util.ts";

test("分块完整覆盖原始数据", () => {
  const data = pseudoRandomBytes(1_000_000, 42);
  const chunks = chunk(data);
  assert.equal(chunks.reduce((n, c) => n + c.length, 0), data.length);
  assert.equal(Buffer.concat(chunks.map((c) => c.data)).equals(data), true);
  // 偏移连续
  let offset = 0;
  for (const c of chunks) {
    assert.equal(c.offset, offset);
    offset += c.length;
  }
});

test("分块大小在 [min, max] 约束内（末块除外）", () => {
  const data = pseudoRandomBytes(2_000_000, 7);
  const chunks = chunk(data);
  for (const c of chunks.slice(0, -1)) {
    assert.ok(c.length >= DEFAULTS.minSize, `分块过小: ${c.length}`);
    assert.ok(c.length <= DEFAULTS.maxSize, `分块过大: ${c.length}`);
  }
});

test("平均分块大小接近期望值 2^maskBits", () => {
  const data = pseudoRandomBytes(10 * 1024 * 1024, 99);
  const chunks = chunk(data);
  const avg = data.length / chunks.length;
  const target = 1 << DEFAULTS.maskBits;
  assert.ok(
    avg > target * 0.5 && avg < target * 2,
    `平均分块 ${avg.toFixed(0)} 偏离目标 ${target}`
  );
});

test("空输入产生一个空分块", () => {
  const chunks = chunk(Buffer.alloc(0));
  assert.equal(chunks.length, 1);
  assert.equal(chunks[0].length, 0);
});

test("分块哈希确定性", () => {
  const data = pseudoRandomBytes(500_000, 1234);
  const a = chunk(data).map((c) => c.hash);
  const b = chunk(data).map((c) => c.hash);
  assert.deepEqual(a, b);
});
