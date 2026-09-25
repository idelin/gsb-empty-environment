import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PolicyEngine } from '../src/engine.ts';
import type { Rule } from '../src/engine.ts';
import { verifyAgainstEnumeration } from '../src/verify.ts';
import type { Cond } from '../src/conditions.ts';

const T: Cond = { kind: 'const', value: true };

function rule(partial: Partial<Rule> & { id: string }): Rule {
  return { start: 0, end: 10, priority: 0, version: 1, effect: 'allow', condition: T, ...partial };
}

test('半开边界：[0,5) allow 与 [5,10) deny，t=5 命中 deny', () => {
  const eng = new PolicyEngine('deny');
  eng.applyBatch([
    { type: 'insert', rule: rule({ id: 'A', start: 0, end: 5, effect: 'allow' }) },
    { type: 'insert', rule: rule({ id: 'B', start: 5, end: 10, effect: 'deny' }) },
  ]);
  assert.equal(eng.query(4).effect, 'allow');
  assert.equal(eng.query(4).source, 'A');
  const at5 = eng.query(5);
  assert.equal(at5.effect, 'deny');
  assert.equal(at5.source, 'B');
  assert.deepEqual(at5.segment, [5, 10]);
});

test('撤销规则后恢复默认策略', () => {
  const eng = new PolicyEngine('deny');
  eng.insert(rule({ id: 'A', start: 0, end: 5, effect: 'allow' }));
  assert.equal(eng.query(2).effect, 'allow');
  const rep = eng.delete('A');
  assert.deepEqual(rep.retracted, ['A']);
  const q = eng.query(2);
  assert.equal(q.effect, 'deny');   // 回到默认
  assert.equal(q.source, null);
  assert.equal(eng.getSegments().length, 0);
});

test('相邻同结果不同来源：段不合并，证据各自保留', () => {
  const eng = new PolicyEngine('allow');
  eng.applyBatch([
    { type: 'insert', rule: rule({ id: 'A', start: 0, end: 5, effect: 'deny' }) },
    { type: 'insert', rule: rule({ id: 'B', start: 5, end: 10, effect: 'deny' }) },
  ]);
  const segs = eng.getSegments();
  assert.equal(segs.length, 2); // 结果相同但来源不同 => 不合并
  const q4 = eng.query(4), q5 = eng.query(5);
  assert.equal(q4.effect, 'deny'); assert.equal(q4.source, 'A');
  assert.equal(q5.effect, 'deny'); assert.equal(q5.source, 'B');
  assert.notEqual(q4.chainId, q5.chainId);
  assert.deepEqual(q4.evidence, ['A']);
  assert.deepEqual(q5.evidence, ['B']);
});

test('相邻同源同结构：段合并且共享同一决策链对象', () => {
  const eng = new PolicyEngine('deny');
  eng.applyBatch([
    { type: 'insert', rule: rule({ id: 'A', start: 0, end: 5, effect: 'allow' }) },
    { type: 'insert', rule: rule({ id: 'B', start: 5, end: 10, effect: 'allow' }) },
  ]);
  // A、B 条件与效果相同但 id 不同 => 决策链不同 => 不合并
  assert.equal(eng.getSegments().length, 2);
  // 同一条规则跨越的连续区间只产生一个段
  const eng2 = new PolicyEngine('deny');
  eng2.insert(rule({ id: 'A', start: 0, end: 10, effect: 'allow' }));
  eng2.insert(rule({ id: 'B', start: 3, end: 6, priority: 1, effect: 'deny' }));
  const segs = eng2.getSegments();
  assert.deepEqual(segs.map((s) => [s.start, s.end]), [[0, 3], [3, 6], [6, 10]]);
  assert.equal(segs[0].chain, segs[2].chain); // 结构共享：同一决策链对象
  assert.equal(segs[0].chain.id, segs[2].chain.id);
});

test('批量原子性：任一 op 非法则整体拒绝，状态不变', () => {
  const eng = new PolicyEngine('deny');
  eng.insert(rule({ id: 'A', start: 0, end: 5, effect: 'allow' }));
  const before = eng.query(2);
  assert.throws(() =>
    eng.applyBatch([
      { type: 'insert', rule: rule({ id: 'B', start: 1, end: 3, effect: 'deny' }) },
      { type: 'insert', rule: rule({ id: 'C', start: 4, end: 4 }) }, // 非法：start >= end
    ]), /start < end/);
  assert.throws(() =>
    eng.applyBatch([
      { type: 'insert', rule: rule({ id: 'D', start: 1, end: 3 }) },
      { type: 'delete', id: '不存在' },
    ]), /不存在/);
  const after = eng.query(2);
  assert.deepEqual(after, before); // 状态未被部分修改
  assert.equal(eng.snapshotRules().length, 1);
});

test('端点改变：撤回旧证据，新区间生效，记录触及段与决策节点', () => {
  const eng = new PolicyEngine('deny');
  eng.insert(rule({ id: 'A', start: 0, end: 5, effect: 'allow' }));
  const rep = eng.insert(rule({ id: 'A', start: 0, end: 8, version: 2, effect: 'allow' }));
  assert.deepEqual(rep.retracted, ['A']);       // 旧版本证据被撤回
  assert.deepEqual(rep.inserted, ['A']);
  assert.ok(rep.touchedSegments.length > 0);
  assert.ok(rep.touchedDecisionNodes.length > 0);
  assert.equal(eng.query(6).effect, 'allow');   // 新区间生效
  assert.equal(eng.query(6).source, 'A');
  assert.equal(eng.query(8).effect, 'deny');    // 半开：8 不在内
  // 旧区间 [0,5) 的证据不再独立存在，已并入 [0,8)
  assert.deepEqual(eng.getSegments().map((s) => [s.start, s.end]), [[0, 8]]);
});

test('增量更新只触及受影响范围，范围外段与决策节点保持原样', () => {
  const eng = new PolicyEngine('deny');
  eng.applyBatch([
    { type: 'insert', rule: rule({ id: 'A', start: 0, end: 10, effect: 'allow' }) },
    { type: 'insert', rule: rule({ id: 'B', start: 20, end: 30, effect: 'allow' }) },
  ]);
  const before = eng.getSegments().map((s) => s.chain.id);
  const rep = eng.insert(rule({ id: 'C', start: 4, end: 6, priority: 5, effect: 'deny' }));
  // 触及的段都在 [4,6) 内
  for (const ts of rep.touchedSegments) {
    assert.ok(ts.start >= 4 && ts.end <= 6, `触及段越界: ${JSON.stringify(ts)}`);
  }
  const after = eng.getSegments();
  // [20,30) 的段决策链 id 不变（未被触及、对象共享）
  const far = after.find((s) => s.start === 20)!;
  assert.equal(far.chain.id, before[1]);
  // 未触及的 [0,4) 段仍用原决策链
  const left = after.find((s) => s.start === 0)!;
  assert.equal(left.chain.id, before[0]);
});

test('优先级与条件落穿：高优先级条件不满足时落到下一条，都不中走默认', () => {
  const eng = new PolicyEngine('deny');
  eng.applyBatch([
    { type: 'insert', rule: rule({ id: 'vip', start: 0, end: 10, priority: 10, effect: 'allow',
      condition: { kind: 'in', attr: 'role', values: [1, 2] } }) },
    { type: 'insert', rule: rule({ id: 'staff', start: 0, end: 10, priority: 5, effect: 'allow',
      condition: { kind: 'and', children: [
        { kind: 'cmp', attr: 'level', op: 'ge', value: 3 },
        { kind: 'not', child: { kind: 'cmp', attr: 'level', op: 'eq', value: 7 } },
      ] } }) },
  ]);
  assert.equal(eng.query(1, { role: 2, level: 0 }).source, 'vip');
  assert.equal(eng.query(1, { role: 9, level: 5 }).source, 'staff');
  const miss = eng.query(1, { role: 9, level: 7 });
  assert.equal(miss.effect, 'deny');
  assert.equal(miss.source, null);
  assert.deepEqual(miss.evidence, ['vip', 'staff']); // 候选证据仍在
});

test('条件只读显式属性：属性缺失 => 原子条件为 false，不外查', () => {
  const eng = new PolicyEngine('deny');
  eng.insert(rule({ id: 'A', start: 0, end: 5, effect: 'allow',
    condition: { kind: 'between', attr: 'age', lo: 18, hi: 60 } }));
  assert.equal(eng.query(1, {}).effect, 'deny');          // 缺属性 => 不命中
  assert.equal(eng.query(1, { age: 30 }).effect, 'allow');
  assert.equal(eng.query(1, { age: 60 }).effect, 'deny'); // between 半开上界
});

test('非法条件被拒绝：非整数、空集合、未排序集合', () => {
  const eng = new PolicyEngine('deny');
  assert.throws(() => eng.insert(rule({ id: 'X', condition: { kind: 'cmp', attr: 'a', op: 'eq', value: 1.5 } })), /整数/);
  assert.throws(() => eng.insert(rule({ id: 'Y', condition: { kind: 'in', attr: 'a', values: [] } })), /非空/);
  assert.throws(() => eng.insert(rule({ id: 'Z', condition: { kind: 'in', attr: 'a', values: [3, 1] } })), /升序/);
  assert.equal(eng.snapshotRules().length, 0);
});

test('版本替换：同 id 新版本生效，旧版本证据撤回', () => {
  const eng = new PolicyEngine('deny');
  eng.insert(rule({ id: 'A', start: 0, end: 10, version: 1, effect: 'allow' }));
  eng.insert(rule({ id: 'A', start: 0, end: 10, version: 2, effect: 'deny' }));
  assert.equal(eng.snapshotRules().length, 1);
  assert.equal(eng.query(3).effect, 'deny');
  const chain = eng.getSegments()[0].chain;
  assert.equal(chain.entries[0].version, 2);
});

test('同优先级版本高者优先', () => {
  const eng = new PolicyEngine('deny');
  eng.applyBatch([
    { type: 'insert', rule: rule({ id: 'old', start: 0, end: 10, version: 1, effect: 'allow' }) },
    { type: 'insert', rule: rule({ id: 'new', start: 0, end: 10, version: 2, effect: 'deny' }) },
  ]);
  const q = eng.query(3);
  assert.equal(q.effect, 'deny');
  assert.equal(q.source, 'new');
});

test('有限时间格 × 有限属性域全枚举对照', () => {
  const eng = new PolicyEngine('deny');
  eng.applyBatch([
    { type: 'insert', rule: rule({ id: 'r1', start: 0, end: 7, priority: 3, effect: 'allow',
      condition: { kind: 'in', attr: 'role', values: [1, 2] } }) },
    { type: 'insert', rule: rule({ id: 'r2', start: 3, end: 12, priority: 9, effect: 'deny',
      condition: { kind: 'cmp', attr: 'level', op: 'ge', value: 5 } }) },
    { type: 'insert', rule: rule({ id: 'r3', start: 5, end: 15, priority: 1, effect: 'allow',
      condition: { kind: 'or', children: [
        { kind: 'between', attr: 'level', lo: 2, hi: 4 },
        { kind: 'and', children: [
          { kind: 'cmp', attr: 'role', op: 'eq', value: 3 },
          { kind: 'not', child: { kind: 'in', attr: 'level', values: [0, 9] } },
        ] },
      ] } }) },
    { type: 'insert', rule: rule({ id: 'r4', start: 10, end: 20, priority: 2, effect: 'deny' }) },
  ]);
  // 再做一次增量端点变更与删除，验证增量路径同样正确
  eng.insert(rule({ id: 'r1', start: 1, end: 8, version: 2, priority: 3, effect: 'allow',
    condition: { kind: 'in', attr: 'role', values: [1, 2] } }));
  eng.delete('r4');
  const rep = verifyAgainstEnumeration(eng, {
    timeMin: 0,
    timeMax: 20,
    domains: { role: [0, 1, 2, 3], level: [0, 2, 3, 5, 9] },
  });
  assert.equal(rep.checked, 20 * 4 * 5);
  assert.deepEqual(rep.mismatches, []);
});
