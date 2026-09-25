import { test } from "node:test";
import assert from "node:assert/strict";
import { FenwickIndex, NaiveIndex } from "../src/height-index.ts";

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("Fenwick 与全量 Naive 在随机操作下完全一致", () => {
  for (let seed = 1; seed <= 20; seed++) {
    const rand = mulberry32(seed);
    const fen = new FenwickIndex();
    const naive = new NaiveIndex();
    for (let step = 0; step < 2000; step++) {
      const op = rand();
      if (op < 0.4 || naive.size === 0) {
        const i = Math.floor(rand() * (naive.size + 1));
        const h = Math.floor(rand() * 5) === 0 ? 0 : Math.floor(rand() * 200);
        fen.insert(i, h);
        naive.insert(i, h);
      } else if (op < 0.6) {
        const i = Math.floor(rand() * naive.size);
        fen.remove(i);
        naive.remove(i);
      } else {
        const i = Math.floor(rand() * naive.size);
        const h = Math.floor(rand() * 5) === 0 ? 0 : Math.floor(rand() * 200);
        fen.set(i, h);
        naive.set(i, h);
      }
      assert.equal(fen.size, naive.size);
      assert.equal(fen.version, naive.version);
      assert.equal(fen.total(), naive.total());
      const probes = 5;
      for (let p = 0; p < probes; p++) {
        const off = Math.floor(rand() * (naive.total() + 50)) - 10;
        assert.equal(fen.findIndex(off), naive.findIndex(off), `findIndex(${off}) seed=${seed} step=${step}`);
        const c = Math.floor(rand() * (naive.size + 1));
        assert.equal(fen.prefix(c), naive.prefix(c));
      }
    }
  }
});

test("零高行不会导致 findIndex 死循环，且会被跳过", () => {
  const idx = new FenwickIndex();
  [0, 0, 0, 50, 0, 0, 30, 0].forEach((h, i) => idx.insert(i, h));
  assert.equal(idx.findIndex(0), 3);
  assert.equal(idx.findIndex(49), 3);
  assert.equal(idx.findIndex(50), 6);
  assert.equal(idx.findIndex(79), 6);
  assert.equal(idx.findIndex(80), 8);
  assert.equal(idx.findIndex(10_000), 8);
  assert.equal(idx.findIndex(-5), 0);
});

test("全零高列表查找立即终止并返回 size", () => {
  const idx = new FenwickIndex();
  for (let i = 0; i < 100; i++) idx.insert(i, 0);
  assert.equal(idx.total(), 0);
  assert.equal(idx.findIndex(0), 100);
  assert.equal(idx.findIndex(1), 100);
});

test("version 随修改单调递增", () => {
  const idx = new FenwickIndex();
  let last = idx.version;
  idx.insert(0, 10);
  assert.ok(idx.version > last);
  last = idx.version;
  idx.set(0, 20);
  assert.ok(idx.version > last);
  last = idx.version;
  idx.set(0, 20); // 无变化不递增
  assert.equal(idx.version, last);
  idx.remove(0);
  assert.ok(idx.version > last);
});
