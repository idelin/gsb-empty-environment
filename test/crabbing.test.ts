import { test } from "node:test";
import assert from "node:assert/strict";
import { RwLatch, latchStats } from "../src/latch.ts";
import { BPlusTree } from "../src/bptree.ts";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const tick = () => new Promise((r) => setImmediate(r));

function mulberry32(seed: number) {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("RwLatch: 读锁共享、写锁互斥、FIFO 公平", async () => {
  const latch = new RwLatch();
  await latch.acquireRead();
  let secondReaderIn = false;
  const p2 = latch.acquireRead().then(() => { secondReaderIn = true; });
  await tick();
  assert.equal(secondReaderIn, true, "第二个读者应能共享读锁");

  let writerIn = false;
  const pw = latch.acquireWrite().then(() => { writerIn = true; });
  await tick();
  assert.equal(writerIn, false, "读锁持有期间写者必须等待");

  latch.releaseRead();
  await tick();
  assert.equal(writerIn, false, "仍有一个读者，写者继续等待");
  await p2;
  latch.releaseRead();
  await tick();
  assert.equal(writerIn, true, "全部读者释放后写者获得锁");

  // FIFO：写者等待期间后来的读者不得插队
  let readerIn = false;
  const pr = latch.acquireRead().then(() => { readerIn = true; });
  await tick();
  assert.equal(readerIn, false, "写锁持有期间读者等待");
  latch.releaseWrite();
  await tick();
  assert.equal(readerIn, true);
  latch.releaseRead();
  await pw; await pr;
});

test("顺序插入/查询正确性（2000 随机键对照 Map）", async () => {
  const tree = new BPlusTree<string>(4);
  const rand = mulberry32(7);
  const ref = new Map<number, string>();
  for (let i = 0; i < 2000; i++) {
    const key = Math.floor(rand() * 5000);
    const val = `v-${key}`;
    await tree.insert(key, val);
    ref.set(key, val);
  }
  for (const [k, v] of ref) assert.equal(await tree.get(k), v);
  for (let i = 0; i < 200; i++) {
    const k = Math.floor(rand() * 5000);
    assert.equal(await tree.get(k), ref.get(k));
  }
  const keys = tree.collect().map(([k]) => k);
  assert.deepEqual(keys, [...ref.keys()].sort((a, b) => a - b), "中序遍历应严格递增");
});

test("螃蟹脚并发：10 读 + 5 写，无死锁、结果正确、读者高速并行", { timeout: 30000 }, async () => {
  const tree = new BPlusTree<string>(4);
  const READERS = 10, WRITERS = 5, PER_WRITER = 400, PER_READER = 2000;
  const KEY_SPACE = 1000 + WRITERS * PER_WRITER;
  for (let i = 0; i < 1000; i++) await tree.insert(i, `val-${i}`);

  latchStats.reset();
  const rand = mulberry32(99);

  const writers = Array.from({ length: WRITERS }, (_, w) => (async () => {
    for (let i = 0; i < PER_WRITER; i++) {
      const key = 1000 + w * PER_WRITER + i;
      await tree.insert(key, `val-${key}`);
      if (i % 5 === 0) await sleep(0);
    }
  })());

  const readers = Array.from({ length: READERS }, () => (async () => {
    for (let i = 0; i < PER_READER; i++) {
      const key = Math.floor(rand() * KEY_SPACE);
      const v = await tree.get(key);
      if (v !== undefined) assert.equal(v, `val-${key}`, "读到的值必须与键对应");
      if (i % 50 === 0) await sleep(0);
    }
  })());

  // 若发生死锁，Promise.all 永不返回，node:test 的 timeout 会使测试失败
  await Promise.all([...writers, ...readers]);

  for (let k = 0; k < KEY_SPACE; k++) {
    assert.equal(await tree.get(k), `val-${k}`, `键 ${k} 必须存在`);
  }
  const keys = tree.collect().map(([k]) => k);
  assert.equal(keys.length, KEY_SPACE);
  assert.ok(keys.every((k, i) => i === 0 || keys[i - 1] < k), "中序遍历严格递增");

  assert.ok(
    latchStats.maxConcurrentReaders > 1,
    `应观察到读者并行穿越（max=${latchStats.maxConcurrentReaders}）`,
  );
  console.log(
    `    [stats] 读获取=${latchStats.readAcquires} 读等待=${latchStats.readWaits} ` +
    `写获取=${latchStats.writeAcquires} 写等待=${latchStats.writeWaits} ` +
    `最大并发读者=${latchStats.maxConcurrentReaders} 树高=${tree.height()}`,
  );
});

test("高争用小树：并发插入同一 key 空间不丢不错", { timeout: 30000 }, async () => {
  const tree = new BPlusTree<number>(3); // 最小阶，分裂最频繁
  const rand = mulberry32(1234);
  const writers = Array.from({ length: 8 }, () => (async () => {
    for (let i = 0; i < 300; i++) {
      await tree.insert(Math.floor(rand() * 500), i);
      if (i % 3 === 0) await sleep(0);
    }
  })());
  await Promise.all(writers);
  const keys = tree.collect().map(([k]) => k);
  assert.ok(keys.every((k, i) => i === 0 || keys[i - 1] < k), "键唯一且有序");
  for (const k of keys) assert.equal(await tree.has(k), true);
});
