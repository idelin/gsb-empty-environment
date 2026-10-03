import { test } from "node:test";
import assert from "node:assert/strict";
import { SparseVector } from "../src/sparse/sparse-vector.ts";
import {
  decodeDeltaIndices,
  decodeRleValues,
  decodeVarint,
  encodeDeltaIndices,
  encodeRleValues,
  encodeVarint,
  zigzagDecode,
  zigzagEncode,
} from "../src/sparse/codec.ts";
import { AsyncMutex } from "../src/sparse/async-mutex.ts";

// ---------- 编解码器边界 ----------

test("varint 边界：0、1、2^53-1 往返", () => {
  for (const n of [0, 1, 127, 128, 300, 2 ** 31, 2 ** 53 - 1]) {
    const out: number[] = [];
    encodeVarint(n, out);
    const buf = Uint8Array.from(out);
    assert.equal(decodeVarint(buf, { p: 0 }), n);
  }
  assert.throws(() => encodeVarint(-1, []), RangeError);
  assert.throws(() => encodeVarint(2 ** 53, []), RangeError);
  assert.throws(() => decodeVarint(new Uint8Array([]), { p: 0 }), RangeError);
});

test("zigzag 边界：正负极值往返", () => {
  for (const n of [0, 1, -1, 2 ** 52, -(2 ** 52)]) {
    assert.equal(zigzagDecode(zigzagEncode(n)), n);
  }
});

test("delta 下标与 RLE 值编码往返（含空序列与长游程）", () => {
  assert.deepEqual(decodeDeltaIndices(encodeDeltaIndices([]), 0), []);
  const idx = [0, 3, 3, 100, 100000, 2 ** 40];
  assert.deepEqual(decodeDeltaIndices(encodeDeltaIndices(idx), idx.length), idx);
  const vals = [7, 7, 7, 7, -3, 0, 0, 42];
  assert.deepEqual(decodeRleValues(encodeRleValues(vals), vals.length), vals);
  const longRun = new Array(100000).fill(9);
  assert.deepEqual(decodeRleValues(encodeRleValues(longRun), longRun.length), longRun);
});

// ---------- 自适应容器 ----------

test("稀疏模式：基本读写删与默认值", () => {
  const v = new SparseVector();
  assert.equal(v.mode, "sparse");
  assert.equal(v.get(5), 0);
  assert.equal(v.has(5), false);
  v.set(5, 42);
  assert.equal(v.get(5), 42);
  assert.equal(v.has(5), true);
  assert.equal(v.size, 1);
  v.set(5, 0); // 写 0 等价删除
  assert.equal(v.has(5), false);
  assert.equal(v.size, 0);
  v.set(100, 1);
  v.delete(100);
  assert.equal(v.size, 0);
  assert.throws(() => v.set(-1, 1), RangeError);
  assert.throws(() => v.set(1.5, 1), RangeError);
});

test("海量稀疏数据：10 万元素散布在千万级下标空间", () => {
  const v = new SparseVector({ autoCompactAfter: 0 }); // 关闭自动压缩，聚焦稀疏表示
  const ref = new Map<number, number>();
  let seed = 12345;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let i = 0; i < 100000; i++) {
    const idx = Math.floor(rand() * 10_000_000);
    const val = Math.floor(rand() * 2000) - 1000;
    v.set(idx, val);
    if (val === 0) ref.delete(idx);
    else ref.set(idx, val);
  }
  assert.equal(v.mode, "sparse"); // 密度极低，保持稀疏
  assert.equal(v.size, ref.size);
  for (const [i, val] of ref) assert.equal(v.get(i), val);
  // 内存估算：稀疏表示远小于 dense 平铺
  assert.ok(v.estimateBytes() < 10_000_000 * 8);
});

test("自适应转换：密度升高转 dense，删除后降回 sparse", () => {
  const v = new SparseVector();
  for (let i = 0; i < 100; i++) v.set(i, i + 1); // 密度 100%
  assert.equal(v.mode, "dense");
  for (let i = 0; i < 100; i++) assert.equal(v.get(i), i + 1);
  for (let i = 0; i < 90; i++) v.delete(i); // 密度降到 10%
  assert.equal(v.mode, "sparse");
  for (let i = 90; i < 100; i++) assert.equal(v.get(i), i + 1);
  assert.equal(v.size, 10);
});

test("dense 扩容：稀疏增长时退回 sparse 而非无限扩容", () => {
  const v = new SparseVector();
  for (let i = 0; i < 64; i++) v.set(i, 1); // dense
  assert.equal(v.mode, "dense");
  v.set(1_000_000, 9); // 远端孤点：扩容不划算
  assert.equal(v.mode, "sparse");
  assert.equal(v.get(1_000_000), 9);
  assert.equal(v.get(63), 1);
});

test("压缩：compact 显著降低占用且数据无损", () => {
  const v = new SparseVector();
  for (let i = 0; i < 1000; i++) v.set(i * 10, 7); // 长游程同值
  const before = v.estimateBytes();
  const { after } = v.compact();
  assert.equal(v.mode, "compressed");
  assert.ok(after < before / 4, `expected compression, ${before} -> ${after}`);
  for (let i = 0; i < 1000; i++) {
    assert.equal(v.get(i * 10), 7);
    assert.equal(v.get(i * 10 + 1), 0);
  }
  // 压缩态写入：惰性解压，行为一致
  v.set(5000, 1);
  assert.notEqual(v.mode, "compressed");
  assert.equal(v.get(5000), 1);
  assert.equal(v.get(0), 7);
});

test("冷数据自动压缩：连续只读触发 compact", () => {
  const v = new SparseVector({ autoCompactAfter: 100 });
  for (let i = 0; i < 50; i++) v.set(i * 100, i);
  assert.equal(v.mode, "sparse");
  for (let i = 0; i < 100; i++) v.get(0);
  assert.equal(v.mode, "compressed");
  for (let i = 0; i < 50; i++) assert.equal(v.get(i * 100), i);
});

test("三种模式下迭代结果一致（按下标升序）", () => {
  const build = () => {
    const v = new SparseVector();
    v.set(3, 30);
    v.set(1, 10);
    v.set(2, 20);
    return v;
  };
  const expected = [[1, 10], [2, 20], [3, 30]];
  const sparse = build();
  assert.deepEqual([...sparse.entries()], expected);
  const dense = new SparseVector();
  for (let i = 0; i < 10; i++) dense.set(i, i * 10);
  dense.delete(0);
  assert.equal(dense.mode, "dense");
  assert.deepEqual([...dense.entries()].slice(0, 3), expected);
  const packed = build();
  packed.compact();
  assert.deepEqual([...packed.entries()], expected);
});

// ---------- 并发安全 ----------

test("并发交错写入：互斥锁下复合操作无丢失更新", async () => {
  const v = new SparseVector();
  const mutex = new AsyncMutex();
  const workers = 16;
  const perWorker = 500;
  // read-modify-write 必须整体位于临界区内
  const tasks = Array.from({ length: workers }, () =>
    (async () => {
      for (let i = 0; i < perWorker; i++) {
        await mutex.runExclusive(() => {
          v.set(0, v.get(0) + 1);
        });
        if (i % 7 === 0) await Promise.resolve(); // 制造交错点
      }
    })(),
  );
  await Promise.all(tasks);
  assert.equal(v.get(0), workers * perWorker);
});

test("并发读写与压缩交错：结果与参考模型一致", async () => {
  const v = new SparseVector({ autoCompactAfter: 0 });
  const ref = new Map<number, number>();
  let seed = 999;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  const tasks = Array.from({ length: 8 }, (_, w) =>
    (async () => {
      for (let i = 0; i < 300; i++) {
        const idx = Math.floor(rand() * 5000);
        const r = rand();
        if (r < 0.4) {
          v.set(idx, w * 1000 + i);
          ref.set(idx, w * 1000 + i);
        } else if (r < 0.6) {
          v.delete(idx);
          ref.delete(idx);
        } else if (r < 0.8) {
          v.compact();
        } else {
          assert.equal(v.get(idx), ref.get(idx) ?? 0);
        }
        if (i % 5 === 0) await Promise.resolve();
      }
    })(),
  );
  await Promise.all(tasks);
  assert.equal(v.size, ref.size);
  for (const [i, val] of ref) assert.equal(v.get(i), val);
});

// ---------- 严格无泄漏 ----------

test("无泄漏：dispose 后内存回到基线且可复用", () => {
  const v = new SparseVector();
  const baseline = v.estimateBytes();
  for (let i = 0; i < 10000; i++) v.set(i * 3, i);
  v.compact();
  assert.ok(v.estimateBytes() > 0);
  v.dispose();
  assert.equal(v.estimateBytes(), baseline);
  assert.equal(v.size, 0);
  assert.equal(v.capacity, 0);
  // dispose 后对象仍可正常使用
  v.set(1, 1);
  assert.equal(v.get(1), 1);
});

test("无泄漏：dense/sparse/compressed 循环转换后 dispose 归零", () => {
  const v = new SparseVector();
  for (let round = 0; round < 5; round++) {
    for (let i = 0; i < 200; i++) v.set(i, i); // -> dense
    v.compact(); // -> compressed
    for (let i = 0; i < 190; i++) v.delete(i); // 解压 -> sparse
    assert.equal(v.mode, "sparse");
  }
  const baseline = new SparseVector().estimateBytes();
  v.dispose();
  assert.equal(v.estimateBytes(), baseline);
  assert.equal(v.size, 0);
});
