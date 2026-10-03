import { test } from "node:test";
import assert from "node:assert/strict";
import { BPlusTree } from "../src/bplustree.ts";
import { runConcurrentWorkload, makeRng } from "../src/workload.ts";

test("顺序插入/查找正确性 + 结构不变量", async () => {
  const tree = new BPlusTree({ maxKeys: 4 });
  const keys: number[] = [];
  const rng = makeRng(7);
  const seen = new Set<number>();
  while (keys.length < 500) {
    const k = Math.floor(rng() * 100000);
    if (!seen.has(k)) {
      seen.add(k);
      keys.push(k);
    }
  }
  for (const k of keys) assert.equal(await tree.insert(k), true);
  tree.validate();
  assert.equal(tree.size(), 500);

  for (const k of keys) assert.equal(await tree.find(k), true);
  for (let i = 0; i < 200; i++) {
    const probe = 100000 + i;
    assert.equal(await tree.find(probe), false);
  }

  // 重复插入返回 false 且不改变规模
  assert.equal(await tree.insert(keys[0]!), false);
  assert.equal(tree.size(), 500);
  tree.validate();
});

test("读耦合：任意时刻最多持有 2 个闩，到达叶子时只持有 1 个", async () => {
  const tree = new BPlusTree({ maxKeys: 4, interleave: true });
  for (let i = 0; i < 300; i++) await tree.insert(i * 2);

  for (let i = 0; i < 200; i++) await tree.find(Math.floor(makeRng(i)() * 600));
  assert.ok(
    tree.maxHeldDuringFind <= 2,
    `reader held ${tree.maxHeldDuringFind} latches`,
  );
  assert.equal(tree.maxHeldAtLeafFind, 1);
});

test("提前释放：叶子安全时，插入到达叶子只持有叶子自身的闩", async () => {
  // 先构造一棵稳定的树（节点普遍不满），再做安全的插入
  const tree = new BPlusTree({ maxKeys: 8, interleave: true });
  for (let i = 0; i < 100; i++) await tree.insert(i * 4); // 稀疏预填
  tree.validate();

  const rng = makeRng(99);
  for (let i = 0; i < 60; i++) {
    await tree.insert(Math.floor(rng() * 400) * 2 + 1); // 插入空隙中的 key
  }
  tree.validate();
  assert.equal(
    tree.minHeldAtLeafInsert,
    1,
    "safe insert should release all ancestors before reaching the leaf",
  );
});

test("无写者时读者零阻塞：高层节点读闩全部立即获得", async () => {
  const tree = new BPlusTree({ maxKeys: 8, interleave: true });
  for (let i = 0; i < 500; i++) await tree.insert(i);

  const readers = Array.from({ length: 10 }, (_, r) =>
    (async () => {
      const rng = makeRng(r + 1);
      for (let i = 0; i < 300; i++) {
        await tree.find(Math.floor(rng() * 500));
      }
    })(),
  );
  await Promise.all(readers);

  for (const level of tree.statsByLevel()) {
    assert.equal(
      level.readWait,
      0,
      `level ${level.level} saw ${level.readWait} blocked reads`,
    );
    assert.ok(level.readImmediate > 0);
  }
});

test(
  "验收：10 读线程 + 5 写线程并发，无死锁且结果正确",
  { timeout: 30000 },
  async () => {
    const tree = new BPlusTree({ maxKeys: 8, interleave: true });
    // 预填一部分数据，让树先长高，读线程能穿越多层高层节点
    for (let i = 0; i < 1000; i++) await tree.insert(i * 3 + 1500);

    const result = await runConcurrentWorkload(tree, {
      readerCount: 10,
      writerCount: 5,
      keysPerWriter: 300,
      keySpace: 6000,
      seed: 42,
    });
    // 能运行到这里即证明无死锁（否则 30s 超时判负）
    assert.equal(result.insertedKeys, 1500);
    assert.ok(result.totalReads > 0);

    // 写线程插入的所有 key 最终都必须可查
    for (let w = 0; w < 5; w++) {
      for (let i = 0; i < 300; i++) {
        assert.equal(await tree.find(w * 300 + i), true);
      }
    }
    tree.validate();
    assert.equal(tree.size(), 1000 + 1500);

    // 协议行为断言：读耦合峰值 <= 2
    assert.ok(tree.maxHeldDuringFind <= 2);
    assert.equal(tree.maxHeldAtLeafFind, 1);
  },
);

test(
  "压力：多写线程下分裂传播正确，无丢 key",
  { timeout: 30000 },
  async () => {
    const tree = new BPlusTree({ maxKeys: 4, interleave: true });
    const result = await runConcurrentWorkload(tree, {
      readerCount: 4,
      writerCount: 8,
      keysPerWriter: 250,
      keySpace: 4000,
      seed: 1234,
    });
    assert.equal(result.insertedKeys, 2000);
    tree.validate();
    assert.equal(tree.size(), 2000);
    for (let k = 0; k < 2000; k++) {
      assert.equal(await tree.find(k), true, `missing key ${k}`);
    }
  },
);
