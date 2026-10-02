import { test } from 'node:test';
import assert from 'node:assert/strict';
import { signal, computed, effect, batch } from '../src/reactive.ts';

/**
 * 钻石图：
 *       A
 *      / \
 *     B   C
 *      \ /
 *       D
 *       |
 *     Effect
 */
function buildDiamond() {
  const a = signal(1);
  const b = computed(() => a.value * 2);
  const c = computed(() => a.value + 10);

  // D 每次计算时快照它读到的 B/C 的值与版本号
  const dSnapshots: Array<{ b: number; c: number; bVersion: number; cVersion: number }> = [];
  const d = computed(() => {
    const snapshot = {
      b: b.value,
      c: c.value,
      bVersion: b.version,
      cVersion: c.version,
    };
    dSnapshots.push(snapshot);
    return snapshot.b + snapshot.c;
  });

  const effectLog: number[] = [];
  const dispose = effect(() => {
    effectLog.push(d.value);
  });

  return { a, b, c, d, dSnapshots, effectLog, dispose };
}

test('钻石依赖：更新 A 后 D 仅计算一次，且读到一致的 B/C 最新值', () => {
  const { a, b, c, d, dSnapshots, effectLog, dispose } = buildDiamond();

  // 初始结算
  assert.equal(d.value, 13); // 2 + 11
  assert.equal(b.computeCount, 1);
  assert.equal(c.computeCount, 1);
  assert.equal(d.computeCount, 1);
  assert.deepEqual(effectLog, [13]);

  const aVersionBefore = a.version;

  // 更新源信号
  a.value = 2;

  // B、C 各按需更新一次
  assert.equal(b.computeCount, 2);
  assert.equal(c.computeCount, 2);
  // D 仅计算一次（钻石汇合点无重复计算）
  assert.equal(d.computeCount, 2);
  assert.equal(d.value, 16); // 4 + 12

  // D 本次计算读到的 B/C 均为最新版本（版本号均新于 A 写入前）
  const last = dSnapshots[dSnapshots.length - 1];
  assert.equal(last.b, 4);
  assert.equal(last.c, 12);
  assert.ok(last.bVersion > aVersionBefore, 'D 读到的 B 必须是最新版本');
  assert.ok(last.cVersion > aVersionBefore, 'D 读到的 C 必须是最新版本');

  // D 从未观察到 B 新 C 旧（或反之）的中间瞬态
  for (const s of dSnapshots) {
    assert.equal(s.c, s.b / 2 + 10, 'D 观察到的 B/C 必须来自同一代 A');
  }

  // Effect 在图一致后恰好运行一次
  assert.deepEqual(effectLog, [13, 16]);

  dispose();
});

test('无瞬态脏值：Effect 观察 B/C 组合，每次观察都必须自洽', () => {
  const a = signal(0);
  const b = computed(() => a.value * 2);
  const c = computed(() => a.value + 10);

  const observed: Array<[number, number]> = [];
  const dispose = effect(() => {
    observed.push([b.value, c.value]);
  });

  for (let i = 1; i <= 5; i++) {
    a.value = i;
  }

  // 每次写入只产生一次观察，且 (B, C) 始终满足同一来源代际的不变式
  assert.equal(observed.length, 6);
  for (const [bv, cv] of observed) {
    assert.equal(cv, bv / 2 + 10, '绝不出现 B 已更新而 C 仍旧值（或反之）的瞬态');
  }

  dispose();
});

test('连续多次更新：每次写入 D 恰好重算一次', () => {
  const { a, d, dispose } = buildDiamond();
  d.value; // 初始结算
  const before = d.computeCount;

  a.value = 2;
  a.value = 3;
  a.value = 4;

  assert.equal(d.computeCount, before + 3);
  assert.equal(d.value, 8 + 14);

  dispose();
});

test('batch：多次写入合并为一次传播，D 与 Effect 各执行一次', () => {
  const { a, b, c, d, effectLog, dispose } = buildDiamond();
  d.value; // 初始结算
  const counts = [b.computeCount, c.computeCount, d.computeCount];

  batch(() => {
    a.value = 2;
    a.value = 3;
  });

  assert.equal(b.computeCount, counts[0] + 1);
  assert.equal(c.computeCount, counts[1] + 1);
  assert.equal(d.computeCount, counts[2] + 1);
  assert.equal(d.value, 6 + 13);
  assert.deepEqual(effectLog, [13, 19]);

  dispose();
});

test('batch 期间读取：同步拉取刷新，读不到脏值', () => {
  const { a, d, dispose } = buildDiamond();
  d.value; // 初始结算

  batch(() => {
    a.value = 5;
    // 即使传播被推迟到 batch 结束，中途读取也必须是最新一致值
    assert.equal(d.value, 10 + 15);
  });

  dispose();
});

test('值未变不传播：版本校验阻断无谓的下游重算', () => {
  const a = signal(1);
  const parity = computed(() => a.value % 2); // 奇偶性
  const downstream = computed(() => parity.value * 100);

  assert.equal(downstream.value, 100);
  assert.equal(downstream.computeCount, 1);

  a.value = 3; // parity 重算但值不变（仍为 1）
  assert.equal(parity.computeCount, 2);
  assert.equal(downstream.computeCount, 1, '下游不应因中间值未变而重算');
  assert.equal(downstream.value, 100);
});

test('同值写入不触发任何传播', () => {
  const { a, b, d, effectLog, dispose } = buildDiamond();
  d.value; // 初始结算
  const counts = [b.computeCount, d.computeCount, effectLog.length];

  a.value = 1; // 与当前值相同

  assert.deepEqual([b.computeCount, d.computeCount, effectLog.length], counts);

  dispose();
});

test('dispose 后 Effect 不再触发', () => {
  const { a, effectLog, dispose } = buildDiamond();
  assert.equal(effectLog.length, 1);

  dispose();
  a.value = 9;

  assert.equal(effectLog.length, 1);
});
