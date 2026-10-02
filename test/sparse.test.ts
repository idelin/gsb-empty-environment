/**
 * 海量稀疏数据与内存紧凑优化器：边界条件、自适应转换、RLE 压缩、
 * 并发安全性与严格无泄漏断言。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  AllocationTracker,
  LatchedSparsePage,
  SparsePage,
} from '../src/sparse.ts';

const CAP = SparsePage.CAPACITY;
// 转换阈值：sparse 每条目 32B，dense 固定 CAP*8B，滞后带 10%
const DENSIFY_AT = Math.floor((CAP * 8 * 1.1) / 32) + 1; // sparse->dense 触发条目数
const SPARSIFY_BELOW = Math.floor((CAP * 8 * 0.9) / 32); // dense->sparse 触发条目数

test('空页：默认稀疏编码，全默认值，RLE 单块', () => {
  const page = new SparsePage(1);
  assert.equal(page.encoding, 'sparse');
  assert.equal(page.size, 0);
  assert.equal(page.memoryBytes(), 0);
  assert.equal(page.read(0), 0);
  assert.equal(page.read(CAP - 1), 0);
  assert.deepEqual(page.toRLE(), [{ start: 0, length: CAP, value: 0 }]);
  page.dispose();
});

test('边界槽位：offset 0 与 CAPACITY-1 可写，越界抛异常', () => {
  const page = new SparsePage(2);
  page.write(0, 11);
  page.write(CAP - 1, 99);
  assert.equal(page.read(0), 11);
  assert.equal(page.read(CAP - 1), 99);
  assert.equal(page.size, 2);
  assert.throws(() => page.read(-1), RangeError);
  assert.throws(() => page.read(CAP), RangeError);
  assert.throws(() => page.write(1.5, 1), RangeError);
  assert.throws(() => page.write(CAP + 100, 1), RangeError);
  page.dispose();
});

test('写默认值即删除条目；覆盖写不重复计数', () => {
  const page = new SparsePage(3);
  page.write(10, 5);
  page.write(10, 6); // 覆盖
  assert.equal(page.size, 1);
  assert.equal(page.read(10), 6);
  page.write(10, 0); // 写回默认值 -> 删除
  assert.equal(page.size, 0);
  assert.equal(page.read(10), 0);
  page.write(20, 0); // 对默认值写默认值：无操作
  assert.equal(page.size, 0);
  page.dispose();
});

test('自适应转换：越过阈值 sparse->dense，回落后 dense->sparse，滞后带防抖', () => {
  const page = new SparsePage(4);
  assert.equal(page.encoding, 'sparse');

  // 低于阈值：保持 sparse
  for (let i = 0; i < DENSIFY_AT - 1; i++) page.write(i, i + 1);
  assert.equal(page.encoding, 'sparse');

  // 越过阈值：转 dense
  page.write(DENSIFY_AT - 1, DENSIFY_AT);
  assert.equal(page.encoding, 'dense');
  assert.equal(page.memoryBytes(), CAP * 8);

  // 滞后带内：删到阈值附近仍保持 dense
  for (let i = 0; i < DENSIFY_AT - 1 - SPARSIFY_BELOW; i++) page.write(i, 0);
  assert.equal(page.encoding, 'dense');

  // 跌破下阈值：转回 sparse
  page.write(DENSIFY_AT - 1 - SPARSIFY_BELOW, 0);
  assert.equal(page.encoding, 'sparse');
  assert.ok(page.memoryBytes() < CAP * 8);

  // 数据在转换前后保持一致
  for (let i = DENSIFY_AT - SPARSIFY_BELOW; i < DENSIFY_AT; i++) {
    assert.equal(page.read(i), i + 1);
  }
  page.dispose();
});

test('全满页与全同值页的 RLE 压缩', () => {
  const full = new SparsePage(5);
  for (let i = 0; i < CAP; i++) full.write(i, 7);
  assert.equal(full.encoding, 'dense');
  assert.deepEqual(full.toRLE(), [{ start: 0, length: CAP, value: 7 }]);
  full.dispose();

  // 交替值：RLE 最坏情况，每槽一块
  const alt = new SparsePage(6);
  for (let i = 0; i < CAP; i += 2) alt.write(i, 1);
  const blocks = alt.toRLE();
  assert.equal(blocks.length, CAP);
  alt.dispose();
});

test('RLE 往返：任意分布序列化后完全还原（含 pageLSN）', () => {
  const page = new SparsePage(7);
  const expected = new Map<number, number>();
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff);
  for (let i = 0; i < 500; i++) {
    const offset = rand() % CAP;
    const value = rand() % 1000;
    page.write(offset, value);
    expected.set(offset, value);
  }
  page.pageLSN = 1234;

  const restored = SparsePage.fromRLE(7, page.toRLE(), page.pageLSN);
  assert.equal(restored.pageLSN, 1234);
  assert.equal(restored.size, page.size);
  for (let i = 0; i < CAP; i++) assert.equal(restored.read(i), page.read(i));
  page.dispose();
  restored.dispose();
});

test('RLE 校验：非法流抛异常且不泄漏', () => {
  const baseline = AllocationTracker.live;
  assert.throws(() => SparsePage.fromRLE(8, [{ start: 1, length: CAP, value: 0 }]));
  assert.throws(() => SparsePage.fromRLE(8, [{ start: 0, length: CAP - 1, value: 0 }]));
  assert.throws(() => SparsePage.fromRLE(8, [{ start: 0, length: 0, value: 0 }]));
  assert.equal(AllocationTracker.live, baseline, 'failed parse must not leak');
});

test('dispose：释放内存、拒绝后续访问、幂等', () => {
  const baseline = AllocationTracker.live;
  const page = new SparsePage(9);
  page.write(1, 1);
  assert.ok(page.memoryBytes() > 0);
  page.dispose();
  assert.equal(page.memoryBytes(), 0);
  assert.throws(() => page.read(1), /disposed/);
  assert.throws(() => page.write(1, 2), /disposed/);
  page.dispose(); // 幂等
  assert.equal(AllocationTracker.live, baseline);
});

test('并发安全：闩锁保护下 64 任务 x 500 次自增无丢失', async () => {
  const baseline = AllocationTracker.live;
  const page = new SparsePage(10);
  const latched = new LatchedSparsePage(page);
  const TASKS = 64;
  const INCREMENTS = 500;
  await Promise.all(
    Array.from({ length: TASKS }, async () => {
      for (let i = 0; i < INCREMENTS; i++) await latched.update(100, (v) => v + 1);
    }),
  );
  assert.equal(page.read(100), TASKS * INCREMENTS);
  page.dispose();
  assert.equal(AllocationTracker.live, baseline);
});

test('并发安全：多槽位交错写后数据与串行执行一致', async () => {
  const page = new SparsePage(11);
  const latched = new LatchedSparsePage(page);
  const offsets = [0, 1, 2, CAP - 1];
  await Promise.all(
    offsets.map(async (offset, k) => {
      for (let i = 0; i < 200; i++) await latched.update(offset, (v) => v + k + 1);
    }),
  );
  for (const [k, offset] of offsets.entries()) {
    assert.equal(page.read(offset), 200 * (k + 1));
  }
  assert.equal(page.size, offsets.length);
  page.dispose();
});

test('对照实验：无闩锁的 read-modify-write 在交错下丢失更新', async () => {
  const page = new SparsePage(12);
  const yieldControl = () => new Promise((r) => setImmediate(r));
  async function racyIncrement() {
    const cur = page.read(0);
    await yieldControl();
    page.write(0, cur + 1);
  }
  await Promise.all([racyIncrement(), racyIncrement()]);
  assert.equal(page.read(0), 1, '两个并发自增只生效一次 => 证明闩锁必要');
  page.dispose();
});

test('严格无泄漏：批量创建/转换/序列化/释放后存活数归零', () => {
  const baseline = AllocationTracker.live;
  const pages: SparsePage[] = [];
  for (let p = 0; p < 50; p++) {
    const page = new SparsePage(100 + p);
    for (let i = 0; i < DENSIFY_AT + 10; i++) page.write(i, i); // 触发 dense
    for (let i = 0; i < DENSIFY_AT; i++) page.write(i, 0);      // 触发回 sparse
    page.toRLE();
    pages.push(page);
  }
  assert.equal(AllocationTracker.live, baseline + 50);
  for (const page of pages) page.dispose();
  assert.equal(AllocationTracker.live, baseline);
});
