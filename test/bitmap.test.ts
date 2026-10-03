import { test } from "node:test";
import assert from "node:assert/strict";
import { Bitmap } from "../src/bitmap.ts";

test("置位、读取与基数统计", () => {
  const bitmap = Bitmap.zeroes(100);
  assert.equal(bitmap.cardinality(), 0);
  bitmap.set(0);
  bitmap.set(31);
  bitmap.set(32);
  bitmap.set(99);
  assert.equal(bitmap.cardinality(), 4);
  assert.ok(bitmap.get(31));
  assert.ok(bitmap.get(32));
  assert.ok(!bitmap.get(33));
  assert.deepEqual(bitmap.toIndices(), [0, 31, 32, 99]);
});

test("ones 构造并清理尾部越界位", () => {
  const bitmap = Bitmap.ones(70);
  assert.equal(bitmap.cardinality(), 70);
  assert.equal(bitmap.wordCount, 3);
  assert.ok(bitmap.get(69));
});

test("向量化 and / or / andNot", () => {
  const a = Bitmap.zeroes(64);
  const b = Bitmap.zeroes(64);
  for (const i of [1, 2, 40, 63]) a.set(i);
  for (const i of [2, 3, 40]) b.set(i);
  assert.deepEqual(a.and(b).toIndices(), [2, 40]);
  assert.deepEqual(a.or(b).toIndices(), [1, 2, 3, 40, 63]);
  assert.deepEqual(a.andNot(b).toIndices(), [1, 63]);
});

test("尺寸不匹配的位运算抛错", () => {
  assert.throws(() => Bitmap.zeroes(10).and(Bitmap.zeroes(11)));
});

test("RLE 压缩往返无损", () => {
  const bitmap = Bitmap.zeroes(10_000);
  for (let i = 500; i < 600; i++) bitmap.set(i);
  bitmap.set(9_999);
  const compressed = bitmap.toCompressed();
  assert.ok(
    compressed.compressedWordCount < bitmap.wordCount,
    "稀疏位图应被显著压缩",
  );
  const restored = compressed.decompress();
  assert.equal(restored.cardinality(), bitmap.cardinality());
  assert.deepEqual(restored.toIndices(), bitmap.toIndices());
});

test("indices 按升序产出且不超过 size", () => {
  const bitmap = Bitmap.zeroes(33);
  bitmap.set(32);
  assert.deepEqual([...bitmap.indices()], [32]);
});
