import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PeakOverlapDetector } from '../src/peakOverlapDetector.ts';

test('空检测器峰值为 0，无峰值区间', () => {
  const d = new PeakOverlapDetector();
  const snap = d.snapshot();
  assert.equal(snap.peak, 0);
  assert.deepEqual(snap.segments, []);
  assert.deepEqual(snap.atomicSegments, []);
});

test('验收边界：端点相接的 [0,5) 与 [5,9) 并发为 1，端点闭合无多计', () => {
  const d = new PeakOverlapDetector();
  d.add('a', 0, 5);
  const snap = d.add('b', 5, 9);
  assert.equal(snap.peak, 1);
  assert.deepEqual(snap.atomicSegments, [
    { start: 0, end: 5 },
    { start: 5, end: 9 },
  ]);
  assert.deepEqual(snap.segments, [{ start: 0, end: 9 }]);
});

test('验收边界：补录迟到区间 [4,6) 后峰值升为 2，覆盖 [4,5) 与 [5,6)', () => {
  const d = new PeakOverlapDetector();
  d.add('a', 0, 5);
  d.add('b', 5, 9);
  const snap = d.add('late', 4, 6);
  assert.equal(snap.peak, 2);
  assert.deepEqual(snap.atomicSegments, [
    { start: 4, end: 5 },
    { start: 5, end: 6 },
  ]);
  assert.deepEqual(snap.segments, [{ start: 4, end: 6 }]);
});

test('验收边界：撤回补录区间后峰值恢复为 1', () => {
  const d = new PeakOverlapDetector();
  d.add('a', 0, 5);
  d.add('b', 5, 9);
  d.add('late', 4, 6);
  const snap = d.remove('late');
  assert.equal(snap.peak, 1);
  assert.deepEqual(snap.atomicSegments, [
    { start: 0, end: 5 },
    { start: 5, end: 9 },
  ]);
  assert.deepEqual(snap.segments, [{ start: 0, end: 9 }]);
});

test('乱序到达：先登记 [5,9) 再补登 [0,5)，结果与顺序无关', () => {
  const d = new PeakOverlapDetector();
  d.add('b', 5, 9);
  d.add('late', 4, 6);
  const snap = d.add('a', 0, 5);
  assert.equal(snap.peak, 2);
  assert.deepEqual(snap.atomicSegments, [
    { start: 4, end: 5 },
    { start: 5, end: 6 },
  ]);
});

test('撤回不存在的 id 静默忽略，快照不变', () => {
  const d = new PeakOverlapDetector();
  d.add('a', 0, 5);
  const snap = d.remove('ghost');
  assert.equal(snap.peak, 1);
  assert.deepEqual(snap.segments, [{ start: 0, end: 5 }]);
});

test('多段互不相连的峰值区域分别输出', () => {
  const d = new PeakOverlapDetector();
  d.add('a', 0, 2);
  d.add('b', 1, 3);
  d.add('c', 5, 7);
  const snap = d.add('d', 6, 8);
  assert.equal(snap.peak, 2);
  assert.deepEqual(snap.segments, [
    { start: 1, end: 2 },
    { start: 6, end: 7 },
  ]);
});

test('嵌套区间端点相接不多计：[2,3) 与 [3,8) 在 3 处不重叠', () => {
  const d = new PeakOverlapDetector();
  d.add('outer', 0, 10);
  d.add('x', 2, 3);
  const snap = d.add('y', 3, 8);
  assert.equal(snap.peak, 2);
  assert.deepEqual(snap.atomicSegments, [
    { start: 2, end: 3 },
    { start: 3, end: 8 },
  ]);
  assert.deepEqual(snap.segments, [{ start: 2, end: 8 }]);
});

test('相同 id 重复登记视为覆盖修正', () => {
  const d = new PeakOverlapDetector();
  d.add('a', 0, 5);
  d.add('b', 4, 6);
  const snap = d.add('b', 8, 9);
  assert.equal(d.size, 2);
  assert.equal(snap.peak, 1);
  assert.deepEqual(snap.segments, [
    { start: 0, end: 5 },
    { start: 8, end: 9 },
  ]);
});

test('非法区间（end <= start 或非有限数）抛出 RangeError', () => {
  const d = new PeakOverlapDetector();
  assert.throws(() => d.add('bad', 5, 5), RangeError);
  assert.throws(() => d.add('bad', 6, 5), RangeError);
  assert.throws(() => d.add('bad', 0, Number.NaN), RangeError);
  assert.equal(d.size, 0);
});

test('clear 清空后峰值归零', () => {
  const d = new PeakOverlapDetector();
  d.add('a', 0, 5);
  d.add('b', 4, 6);
  const snap = d.clear();
  assert.equal(snap.peak, 0);
  assert.deepEqual(snap.segments, []);
});
