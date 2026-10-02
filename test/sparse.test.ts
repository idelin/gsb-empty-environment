/**
 * 海量稀疏数据与内存紧凑优化器测试：边界条件、自适应转换、
 * 压缩往返、内存估算、并发安全与零泄漏断言。
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import { SparseArray } from "../src/sparse/sparse-array.ts";
import { CHUNK_SIZE, LeakRegistry } from "../src/sparse/containers.ts";
import { AsyncMutex } from "../src/sparse/async-mutex.ts";

// ---------- 边界条件 ----------

test("边界：空数组", () => {
  const a = new SparseArray<number>();
  assert.equal(a.length, 0);
  assert.equal(a.chunkCount, 0);
  assert.equal(a.get(0), undefined);
  assert.equal(a.has(0), false);
  assert.equal(a.delete(0), false);
  assert.deepEqual(a.keys(), []);
  const packed = a.compress();
  const b = SparseArray.decompress<number>(packed);
  assert.equal(b.length, 0);
  a.dispose();
  b.dispose();
  LeakRegistry.assertNoLeaks();
});

test("边界：单元素与下标 0", () => {
  const a = new SparseArray<string>();
  a.set(0, "zero");
  assert.equal(a.get(0), "zero");
  assert.equal(a.length, 1);
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

test("边界：最大安全整数下标", () => {
  const a = new SparseArray<number>();
  a.set(Number.MAX_SAFE_INTEGER, 42);
  assert.equal(a.get(Number.MAX_SAFE_INTEGER), 42);
  assert.equal(a.length, 1);
  const b = SparseArray.decompress<number>(a.compress());
  assert.equal(b.get(Number.MAX_SAFE_INTEGER), 42);
  a.dispose();
  b.dispose();
  LeakRegistry.assertNoLeaks();
});

test("边界：非法下标抛 RangeError", () => {
  const a = new SparseArray<number>();
  for (const bad of [-1, -100, 1.5, NaN, Infinity, 2 ** 53]) {
    assert.throws(() => a.set(bad, 1), RangeError);
    assert.throws(() => a.get(bad), RangeError);
    assert.throws(() => a.delete(bad), RangeError);
  }
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

test("边界：覆写不改变长度，删除释放空块", () => {
  const a = new SparseArray<number>();
  a.set(5, 1);
  a.set(5, 2);
  a.set(5, 3);
  assert.equal(a.length, 1);
  assert.equal(a.get(5), 3);
  assert.equal(a.delete(5), true);
  assert.equal(a.length, 0);
  assert.equal(a.chunkCount, 0); // 空块已释放
  assert.equal(a.delete(5), false);
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

test("边界：dispose 后使用抛错，重复 dispose 幂等", () => {
  const a = new SparseArray<number>();
  a.set(1, 1);
  a.dispose();
  a.dispose();
  assert.throws(() => a.get(1), /disposed/);
  assert.throws(() => a.set(1, 1), /disposed/);
  LeakRegistry.assertNoLeaks();
});

// ---------- 容器自适应转换 ----------

test("自适应：低密度散点 → Map 容器", () => {
  const a = new SparseArray<number>();
  for (let i = 0; i < 100; i++) a.set(i * 37, i); // 跨块低密度
  a.optimize();
  assert.ok(a.chunkKinds().every((k) => k === "map"));
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

test("自适应：连续重复值游程 → RLE 容器", () => {
  const a = new SparseArray<string>();
  for (let i = 0; i < 1000; i++) a.set(i, "same"); // 单块内一条游程
  a.optimize();
  assert.deepEqual(a.chunkKinds(), ["rle"]);
  // RLE 内存远低于 Map
  const rleMem = a.memoryBytes();
  const b = new SparseArray<string>();
  for (let i = 0; i < 1000; i++) b.set(i, `v${i}`); // 无游程结构
  b.optimize();
  assert.ok(rleMem < b.memoryBytes() / 5);
  a.dispose();
  b.dispose();
  LeakRegistry.assertNoLeaks();
});

test("自适应：高密度 → Dense 容器，删除降级回收", () => {
  const a = new SparseArray<number>();
  for (let i = 0; i < CHUNK_SIZE; i++) a.set(i, i); // 整块写满
  a.optimize();
  assert.deepEqual(a.chunkKinds(), ["dense"]);
  // 删除到密度阈值以下 → 自动降级为非 Dense
  for (let i = 0; i < CHUNK_SIZE * 0.5; i++) a.delete(i);
  assert.notDeepEqual(a.chunkKinds(), ["dense"]);
  assert.equal(a.length, CHUNK_SIZE - Math.floor(CHUNK_SIZE * 0.5));
  // 全部删除 → 块释放
  for (let i = Math.floor(CHUNK_SIZE * 0.5); i < CHUNK_SIZE; i++) a.delete(i);
  assert.equal(a.chunkCount, 0);
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

test("RLE：游程分裂与桥接合并", () => {
  const a = new SparseArray<number>();
  for (let i = 0; i < 100; i++) a.set(i, 7); // 一条游程 [0,99]
  a.optimize();
  assert.deepEqual(a.chunkKinds(), ["rle"]);
  a.set(50, 9); // 分裂为 [0,49] [50,50] [51,99]
  assert.equal(a.get(49), 7);
  assert.equal(a.get(50), 9);
  assert.equal(a.get(51), 7);
  a.set(50, 7); // 桥接合并回一条游程
  a.optimize();
  assert.deepEqual(a.chunkKinds(), ["rle"]);
  for (let i = 0; i < 100; i++) assert.equal(a.get(i), 7);
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

test("自适应：跨多种子分布共存于不同块", () => {
  const a = new SparseArray<number>();
  // 块 0：稠密
  for (let i = 0; i < CHUNK_SIZE; i++) a.set(i, i);
  // 块 1：游程
  for (let i = 0; i < 500; i++) a.set(CHUNK_SIZE + i, 1);
  // 块 2：散点
  for (let i = 0; i < 50; i++) a.set(2 * CHUNK_SIZE + i * 61, i);
  a.optimize();
  assert.deepEqual(a.chunkKinds(), ["dense", "rle", "map"]);
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

// ---------- 压缩 ----------

test("压缩：海量稀疏数据往返一致且极致压缩率", () => {
  const a = new SparseArray<unknown>();
  const span = 10_000_000; // 千万级下标空间
  let count = 0;
  for (let i = 0; i < span; i += 1000) {
    a.set(i, `group-${(i / 1000) % 5}`); // 高度重复的值
    count++;
  }
  a.optimize();
  const packed = a.compress();
  const denseBytes = span * 8; // 等效稠密数组
  assert.ok(
    packed.length < denseBytes / 1000,
    `压缩率不足：${packed.length} vs dense ${denseBytes}`,
  );
  const b = SparseArray.decompress<unknown>(a.compress());
  assert.equal(b.length, count);
  for (let i = 0; i < span; i += 1000) {
    assert.equal(b.get(i), `group-${(i / 1000) % 5}`);
  }
  a.dispose();
  b.dispose();
  LeakRegistry.assertNoLeaks();
});

test("压缩：随机稀疏分布往返一致", () => {
  const a = new SparseArray<number>();
  const indices = new Set<number>();
  let seed = 12345;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff);
  for (let i = 0; i < 5000; i++) {
    const idx = rand() % 1_000_000;
    indices.add(idx);
    a.set(idx, rand());
  }
  const b = SparseArray.decompress<number>(a.compress());
  assert.equal(b.length, a.length);
  for (const idx of indices) assert.equal(b.get(idx), a.get(idx));
  a.dispose();
  b.dispose();
  LeakRegistry.assertNoLeaks();
});

test("压缩：损坏数据抛错", () => {
  const a = new SparseArray<number>();
  a.set(1, 10);
  a.set(2, 20);
  const packed = a.compress();
  assert.throws(() => SparseArray.decompress(packed.slice(0, 2))); // 截断
  const trailing = new Uint8Array([...packed, 0xff, 0xff]);
  assert.throws(() => SparseArray.decompress(trailing)); // 尾随字节
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

// ---------- 内存紧凑 ----------

test("内存：稀疏场景远低于等效稠密数组", () => {
  const a = new SparseArray<number>();
  for (let i = 0; i < 10_000; i++) a.set(i * 1000, i);
  a.optimize();
  const denseEquivalent = 10_000_000 * 8;
  assert.ok(a.memoryBytes() < denseEquivalent / 50);
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

test("内存：optimize 幂等且内存不增", () => {
  const a = new SparseArray<number>();
  for (let i = 0; i < 3000; i++) a.set(i * 3, i % 11);
  a.optimize();
  const before = a.memoryBytes();
  a.optimize();
  a.optimize();
  assert.equal(a.memoryBytes(), before);
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

test("内存：clear 释放全部块", () => {
  const a = new SparseArray<number>();
  for (let i = 0; i < 5000; i++) a.set(i, i);
  a.clear();
  assert.equal(a.length, 0);
  assert.equal(a.chunkCount, 0);
  assert.equal(LeakRegistry.chunks, 0);
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

test("零泄漏：全路径（构造/转换/压缩/删除/clear/dispose）", () => {
  const arrays: SparseArray<number>[] = [];
  for (let round = 0; round < 20; round++) {
    const a = new SparseArray<number>();
    for (let i = 0; i < CHUNK_SIZE; i++) a.set(i, i % 3); // map→rle/dense 转换
    for (let i = 0; i < CHUNK_SIZE / 2; i++) a.delete(i); // 降级
    a.compress();
    arrays.push(a);
  }
  for (const a of arrays) a.dispose();
  LeakRegistry.assertNoLeaks();
});

// ---------- 并发安全 ----------

test("并发：1000 个并发事务自增，结果精确无丢失", async () => {
  const a = new SparseArray<number>();
  const workers: Promise<unknown>[] = [];
  for (let w = 0; w < 50; w++) {
    workers.push((async () => {
      for (let i = 0; i < 20; i++) await a.increment(7);
    })());
  }
  await Promise.all(workers);
  assert.equal(a.get(7), 1000);
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

test("并发：事务互斥（无交错重入）", async () => {
  const a = new SparseArray<number>();
  let inside = 0;
  let maxInside = 0;
  const tasks = Array.from({ length: 100 }, (_, i) =>
    a.transaction(async (arr) => {
      inside++;
      maxInside = Math.max(maxInside, inside);
      await new Promise((r) => setImmediate(r)); // 让出事件循环，放大交错窗口
      arr.set(i, i);
      inside--;
    }),
  );
  await Promise.all(tasks);
  assert.equal(maxInside, 1); // 任意时刻最多一个事务在临界区
  assert.equal(a.length, 100);
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

test("并发：多键并发读-改-写最终一致", async () => {
  const a = new SparseArray<number>();
  const keys = [0, 1000, 1_000_000];
  await Promise.all(
    keys.flatMap((k) =>
      Array.from({ length: 100 }, () => a.increment(k)),
    ),
  );
  for (const k of keys) assert.equal(a.get(k), 100);
  a.dispose();
  LeakRegistry.assertNoLeaks();
});

test("并发：AsyncMutex 公平 FIFO 且异常安全释放", async () => {
  const m = new AsyncMutex();
  const order: number[] = [];
  await Promise.all([
    m.runExclusive(async () => { order.push(1); }),
    m.runExclusive(async () => { order.push(2); }),
    m.runExclusive(async () => { order.push(3); }),
  ]);
  assert.deepEqual(order, [1, 2, 3]);
  // 异常后锁可继续获取
  await assert.rejects(m.runExclusive(() => { throw new Error("boom"); }));
  await m.runExclusive(async () => { order.push(4); });
  assert.deepEqual(order, [1, 2, 3, 4]);
});
