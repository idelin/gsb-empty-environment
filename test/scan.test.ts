import { test } from "node:test";
import assert from "node:assert/strict";
import { BufferPool } from "../src/buffer_pool.ts";
import { BPlusTree, LEAF_MAX } from "../src/bplustree.ts";

const TOTAL = 10_000;
// 缓冲池容量刻意压小（8 帧），远小于叶子页总数，
// 扫描过程必然发生大量页面置换，任何 Pin 泄漏都会立刻导致池耗尽报错。
const POOL_CAPACITY = 8;

function buildTree(total: number): { pool: BufferPool; tree: BPlusTree } {
  const pool = new BufferPool(POOL_CAPACITY);
  const tree = new BPlusTree(pool);
  for (let i = 0; i < total; i++) {
    tree.insert(i, i * 10);
  }
  return { pool, tree };
}

test("批量插入 10000 条记录后无 Pin 泄漏", () => {
  const { pool, tree } = buildTree(TOTAL);
  assert.equal(pool.totalPins, 0, "插入结束后不应有残留 Pin");
  assert.ok(pool.frameCount <= POOL_CAPACITY, "页框数不得超过缓冲池容量");
  // 抽查点查
  assert.equal(tree.search(0), 0);
  assert.equal(tree.search(9999), 99990);
  assert.equal(tree.search(5000), 50000);
  assert.equal(tree.search(10000), undefined);
});

test("Scan(2500, 7500) 顺序输出 5000 条不重复记录", () => {
  const { pool, tree } = buildTree(TOTAL);
  const evictionsBefore = pool.evictionCount;

  const result: Array<[number, number]> = [];
  for (const [key, value] of tree.scan(2500, 7500)) {
    result.push([key, value]);
  }

  // 数量与区间
  assert.equal(result.length, 5000);
  assert.equal(result[0][0], 2500);
  assert.equal(result[4999][0], 7499);

  // 严格升序、无重复、值正确
  const seen = new Set<number>();
  for (let i = 0; i < result.length; i++) {
    const [key, value] = result[i];
    assert.ok(!seen.has(key), `key ${key} 重复出现`);
    seen.add(key);
    assert.equal(value, key * 10);
    if (i > 0) {
      assert.ok(result[i][0] > result[i - 1][0], "输出必须严格升序");
    }
  }

  // 扫描跨越了大量物理页边界（叶子页容量 LEAF_MAX，5000 条必然跨页）
  assert.ok(
    pool.evictionCount > evictionsBefore,
    "小容量缓冲池下扫描应触发页面置换，证明发生了跨页读取",
  );
  // 无页面泄漏：迭代结束后所有 Pin 均已释放
  assert.equal(pool.totalPins, 0, "扫描结束后不应有残留 Pin（页面泄漏）");
  assert.ok(pool.frameCount <= POOL_CAPACITY, "缓冲池未发生内存溢出");
});

test("全表扫描输出 10000 条且与插入顺序一致", () => {
  const { pool, tree } = buildTree(TOTAL);
  let expected = 0;
  let count = 0;
  for (const [key, value] of tree.scan(0, TOTAL)) {
    assert.equal(key, expected);
    assert.equal(value, expected * 10);
    expected++;
    count++;
  }
  assert.equal(count, TOTAL);
  assert.equal(pool.totalPins, 0);
});

test("跨页边界扫描：范围与页边界不对齐时结果仍完整", () => {
  const { pool, tree } = buildTree(TOTAL);
  // 故意选取不落在页边界上的起止点
  const start = 37;
  const end = 37 + LEAF_MAX * 3 + 11;
  const keys: number[] = [];
  for (const [key] of tree.scan(start, end)) {
    keys.push(key);
  }
  assert.equal(keys.length, end - start);
  for (let i = 0; i < keys.length; i++) {
    assert.equal(keys[i], start + i);
  }
  assert.equal(pool.totalPins, 0);
});

test("边界与空范围", () => {
  const { pool, tree } = buildTree(TOTAL);
  // 空范围
  assert.deepEqual([...tree.scan(500, 500)], []);
  assert.deepEqual([...tree.scan(600, 500)], []);
  // 单条记录
  assert.deepEqual([...tree.scan(42, 43)], [[42, 420]]);
  // 起点超出最大 Key
  assert.deepEqual([...tree.scan(TOTAL, TOTAL + 100)], []);
  // 终点超出最大 Key：扫到链表末尾自动结束
  const tail = [...tree.scan(TOTAL - 3, TOTAL + 1000)];
  assert.deepEqual(
    tail.map(([k]) => k),
    [TOTAL - 3, TOTAL - 2, TOTAL - 1],
  );
  assert.equal(pool.totalPins, 0);
});

test("提前 break 的迭代也会释放 Pin（无泄漏）", () => {
  const { pool, tree } = buildTree(TOTAL);
  let visited = 0;
  for (const _ of tree.scan(0, TOTAL)) {
    visited++;
    if (visited === 10) {
      break; // for...of 的 break 会触发 iterator.return()
    }
  }
  assert.equal(visited, 10);
  assert.equal(pool.totalPins, 0, "break 后不应有残留 Pin");
});

test("乱序插入后范围扫描结果依然有序", () => {
  const pool = new BufferPool(POOL_CAPACITY);
  const tree = new BPlusTree(pool);
  const keys: number[] = [];
  for (let i = 0; i < TOTAL; i++) {
    keys.push(i);
  }
  // 确定性伪随机洗牌
  let seed = 42;
  for (let i = keys.length - 1; i > 0; i--) {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    const j = seed % (i + 1);
    [keys[i], keys[j]] = [keys[j], keys[i]];
  }
  for (const k of keys) {
    tree.insert(k, k * 10);
  }
  let expected = 2500;
  let count = 0;
  for (const [key, value] of tree.scan(2500, 7500)) {
    assert.equal(key, expected);
    assert.equal(value, expected * 10);
    expected++;
    count++;
  }
  assert.equal(count, 5000);
  assert.equal(pool.totalPins, 0);
});
