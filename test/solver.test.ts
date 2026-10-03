import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  compose,
  converse,
  relationBetween,
  relationSetOf,
  singleton,
  toArray,
  UNIVERSAL,
} from '../src/allen.ts';
import { AllenNetwork } from '../src/network.ts';
import { hullConstraints, Scheduler } from '../src/solver.ts';

// --- Allen 代数基础 -------------------------------------------------------

test('relationBetween 识别全部 13 个基本关系', () => {
  const cases: [number, number, number, number, string][] = [
    [0, 1, 2, 3, 'b'],
    [0, 1, 1, 2, 'm'],
    [0, 2, 1, 3, 'o'],
    [1, 2, 1, 3, 's'],
    [1, 2, 0, 3, 'd'],
    [1, 3, 0, 3, 'f'],
    [1, 2, 1, 2, 'eq'],
    [2, 3, 0, 1, 'bi'],
    [1, 2, 0, 1, 'mi'],
    [1, 3, 0, 2, 'oi'],
    [1, 3, 1, 2, 'si'],
    [0, 3, 1, 2, 'di'],
    [0, 3, 1, 3, 'fi'],
  ];
  for (const [as, ae, bs, be, expected] of cases) {
    assert.equal(
      relationBetween({ start: as, end: ae }, { start: bs, end: be }),
      expected,
    );
  }
});

test('converse 互逆且对合', () => {
  assert.equal(converse('b'), 'bi');
  assert.equal(converse('bi'), 'b');
  assert.equal(converse('o'), 'oi');
  assert.equal(converse('eq'), 'eq');
  for (const r of toArray(UNIVERSAL)) {
    assert.equal(converse(converse(r)), r);
  }
});

test('合成表（运行时枚举生成）与经典结果一致', () => {
  assert.deepEqual(toArray(compose('b', 'b')), ['b']);
  assert.deepEqual(toArray(compose('m', 'o')), ['b']);
  assert.deepEqual(toArray(compose('b', 'o')), ['b']);
  assert.deepEqual(toArray(compose('o', 'o')).sort(), ['b', 'm', 'o']);
  assert.equal(compose('b', 'bi'), UNIVERSAL); // before ∘ after = 全集
  assert.deepEqual(toArray(compose('d', 'd')), ['d']);
  assert.deepEqual(toArray(compose('eq', 'o')), ['o']);
});

// --- 定性网络：路径相容性 / 传递闭包 -------------------------------------

test('路径相容性通过传递闭包收紧间接关系', () => {
  const net = new AllenNetwork(3); // 0=A, 1=B, 2=C
  assert.ok(net.constrain(0, 1, singleton('b'))); // A before B
  assert.ok(net.constrain(1, 2, singleton('o'))); // B overlaps C
  // b ∘ o = b：A 必定在 C 之前
  assert.equal(net.get(0, 2), singleton('b'));
  assert.equal(net.get(2, 0), singleton('bi'));
});

test('定性层检测不可满足的循环约束', () => {
  const net = new AllenNetwork(2);
  assert.ok(net.constrain(0, 1, singleton('b')));
  assert.equal(net.constrain(1, 0, singleton('b')), false);
});

// --- 凸包转换 -------------------------------------------------------------

test('析取关系 {before, meets} 的凸包为 A.end <= B.start', () => {
  const hull = hullConstraints(relationSetOf('b', 'm'));
  assert.deepEqual(hull, [[1, 2, 0]]);
});

// --- 验收场景 -------------------------------------------------------------

function acceptanceScenario() {
  const s = new Scheduler();
  s.addTask('A', 2).addTask('B', 3).addTask('C', 4);
  s.addRelation('A', 'before', 'B'); // A 必须在 B 之前结束
  s.addRelation('B', 'overlaps', 'C'); // B 与 C 部分重叠
  s.startBefore('C', 5); // C 必须在第 5 天前开始
  return s.solve();
}

test('验收场景可解，且定性传播推出 A before C', () => {
  const r = acceptanceScenario();
  assert.ok(r.consistent);
  assert.ok(r.qualitativelyConsistent);
  assert.equal(r.relationBetween('A', 'C'), singleton('b'));
});

test('验收场景：任务 A 的最迟结束时间为第 3 天', () => {
  const r = acceptanceScenario();
  const a = r.tasks.find((t) => t.name === 'A')!;
  assert.equal(a.latestEnd, 3);
  assert.equal(a.earliestStart, 0);
  assert.equal(a.latestStart, 1);
  assert.equal(a.earliestEnd, 2);
});

test('验收场景：B、C 的可行窗口由传播链决定', () => {
  const r = acceptanceScenario();
  const b = r.tasks.find((t) => t.name === 'B')!;
  const c = r.tasks.find((t) => t.name === 'C')!;
  assert.deepEqual(
    [b.earliestStart, b.latestStart, b.earliestEnd, b.latestEnd],
    [2, 3, 5, 6],
  );
  assert.deepEqual(
    [c.earliestStart, c.latestStart, c.earliestEnd, c.latestEnd],
    [3, 4, 7, 8],
  );
});

test('验收场景：项目全局关键路径窗口为 [7, 8]，关键链为 A-B-C', () => {
  const r = acceptanceScenario();
  assert.deepEqual(r.projectWindow, [7, 8]);
  assert.deepEqual(r.criticalPath, ['A', 'B', 'C']);
});

// --- 一致性与析取 ---------------------------------------------------------

test('度量层检测截止时间导致的不可满足', () => {
  const s = new Scheduler();
  s.addTask('A', 2).addTask('B', 3).addTask('C', 4);
  s.addRelation('A', 'before', 'B');
  s.addRelation('B', 'overlaps', 'C');
  s.startBefore('C', 3); // C.start <= 2，但传播要求 C.start >= 3
  const r = s.solve();
  assert.equal(r.consistent, false);
  assert.equal(r.qualitativelyConsistent, true); // 仅度量层冲突
});

test('析取约束 {before, meets} 与非严格 finish-to-start 等价', () => {
  const s = new Scheduler();
  s.addTask('A', 2).addTask('B', 3);
  s.addRelation('A', ['before', 'meets'], 'B');
  const r = s.solve();
  assert.ok(r.consistent);
  const a = r.tasks.find((t) => t.name === 'A')!;
  const b = r.tasks.find((t) => t.name === 'B')!;
  assert.equal(a.earliestEnd, 2);
  assert.equal(b.earliestStart, 2); // A.end <= B.start，无下界间隔
});

test('meets 强制首尾相接，duration 区间产生浮动窗口', () => {
  const s = new Scheduler();
  s.addTask('A', { min: 2, max: 4 }).addTask('B', 3);
  s.addRelation('A', 'meets', 'B');
  s.endOnOrBefore('B', 8);
  const r = s.solve();
  assert.ok(r.consistent);
  const a = r.tasks.find((t) => t.name === 'A')!;
  assert.equal(a.earliestStart, 0);
  assert.equal(a.latestStart, 3); // B.end <= 8，B 长 3，A.end=B.start<=5，A.start<=5-2
  assert.equal(a.earliestEnd, 2);
  assert.equal(a.latestEnd, 5);
});
