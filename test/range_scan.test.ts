import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BufferPool, DiskManager } from '../src/buffer_pool.ts';
import { BPlusTree, LEAF_CAPACITY } from '../src/btree.ts';

const RECORD_COUNT = 10_000;
const POOL_CAPACITY = 8; // 远小于叶子页总数，任何 Pin 泄漏都会立刻耗尽缓冲池

function buildTree(n: number, capacity = POOL_CAPACITY) {
  const pool = new BufferPool(new DiskManager(), capacity);
  const tree = new BPlusTree(pool);
  for (let i = 0; i < n; i++) {
    tree.insert(i, i * 10);
  }
  return { pool, tree };
}

test('建树后无 Pin 泄漏，点查抽样正确', () => {
  const { pool, tree } = buildTree(RECORD_COUNT);
  assert.equal(pool.pinnedCount, 0);
  for (const k of [0, 1, 123, 2500, 7499, 9999]) {
    assert.equal(tree.get(k), k * 10);
  }
  assert.equal(tree.get(10_000), undefined);
  assert.equal(pool.pinnedCount, 0);
});

test('Scan(2500, 7500) 顺序输出 5000 条不重复记录，且无页面泄漏', () => {
  const { pool, tree } = buildTree(RECORD_COUNT);

  const fetchBefore = pool.fetchCount;
  const keys: number[] = [];
  const values: number[] = [];
  for (const entry of tree.scan(2500, 7500)) {
    keys.push(entry.key);
    values.push(entry.value);
  }

  // 数量、顺序、内容、唯一性
  assert.equal(keys.length, 5000);
  assert.equal(new Set(keys).size, 5000, '记录不重复');
  for (let i = 0; i < keys.length; i++) {
    assert.equal(keys[i], 2500 + i, '严格按序输出');
    assert.equal(values[i], (2500 + i) * 10, 'key/value 对应正确');
  }

  // 确实发生了跨页迭代：5000 条记录分布在约 79 个叶子页上
  const expectedLeafPages = Math.ceil(5000 / (LEAF_CAPACITY / 2));
  assert.ok(
    pool.fetchCount - fetchBefore >= expectedLeafPages,
    '扫描跨越了多个物理页',
  );

  // 迭代器消费完毕后缓冲池无残留 Pin（无泄漏、无溢出）
  assert.equal(pool.pinnedCount, 0, 'BufferPool 无页面泄漏');
  assert.ok(pool.residentPages <= pool.capacity, '缓冲池未超出容量');
});

test('提前终止（break + close）同样不泄漏页面', () => {
  const { pool, tree } = buildTree(RECORD_COUNT);
  const it = tree.scan(100, 9900);
  let count = 0;
  for (const _entry of it) {
    count++;
    if (count === 10) break; // 只消费 10 条就退出
  }
  it.close();
  assert.equal(count, 10);
  assert.equal(pool.pinnedCount, 0, 'close 后无残留 Pin');
  it.close(); // 幂等
  assert.equal(pool.pinnedCount, 0);
});

test('边界：起始键落在页中间 / 空区间 / 超出最大键', () => {
  const { pool, tree } = buildTree(RECORD_COUNT);

  // 半开区间 [start, end)：单条记录
  assert.deepEqual(
    [...tree.scan(4096, 4097)].map((e) => e.key),
    [4096],
  );

  // 空区间
  assert.equal([...tree.scan(5000, 5000)].length, 0);
  assert.equal([...tree.scan(7000, 3000)].length, 0);

  // 起始键超出最大键
  assert.equal([...tree.scan(10_000, 20_000)].length, 0);

  // 覆盖整棵树
  assert.equal([...tree.scan(0, RECORD_COUNT)].length, RECORD_COUNT);

  // endKey 超过最大键：扫到链表末尾自动停止
  const tail = [...tree.scan(9990, Number.MAX_SAFE_INTEGER)].map((e) => e.key);
  assert.deepEqual(tail, [9990, 9991, 9992, 9993, 9994, 9995, 9996, 9997, 9998, 9999]);

  assert.equal(pool.pinnedCount, 0);
});

test('缓冲池容量极小（2 帧）时扫描依然正确且无泄漏', () => {
  const { pool, tree } = buildTree(RECORD_COUNT, 2);
  const keys = [...tree.scan(2500, 7500)].map((e) => e.key);
  assert.equal(keys.length, 5000);
  assert.equal(keys[0], 2500);
  assert.equal(keys[keys.length - 1], 7499);
  assert.equal(pool.pinnedCount, 0);
});
