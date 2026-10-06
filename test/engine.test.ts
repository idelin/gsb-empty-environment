import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runEngine, runReference, normalizeResult } from '../src/index.ts';
import type { Rule } from '../src/index.ts';

test('快照语义：规则读不到本轮其他规则的赋值', () => {
  const rules: Rule[] = [
    { id: 'set-x', priority: 1, when: { op: 'eq', field: 'trigger', value: true }, set: { x: 1 } },
    // 若引擎把 set-x 的赋值暴露给本轮，该条件会命中；快照语义下必须不命中。
    { id: 'read-x', priority: 1, when: { op: 'eq', field: 'x', value: 1 }, set: { y: 2 } },
  ];
  const result = runEngine(rules, { trigger: true });
  assert.equal(result.facts.x, 1);
  assert.equal('y' in result.facts, false);
  assert.equal(result.evidence.y.status, 'unset');
  assert.equal(result.evidence.y.candidates[0].outcome, 'not-matched');
});

test('快照语义：条件看到的是输入快照中的旧值', () => {
  const rules: Rule[] = [
    { id: 'overwrite', priority: 1, when: { op: 'exists', field: 'x' }, set: { x: 99 } },
    { id: 'read-old', priority: 1, when: { op: 'eq', field: 'x', value: 0 }, set: { y: 'saw-old' } },
    { id: 'read-new', priority: 1, when: { op: 'eq', field: 'x', value: 99 }, set: { z: 'saw-new' } },
  ];
  const result = runEngine(rules, { x: 0 });
  assert.equal(result.facts.x, 99);
  assert.equal(result.facts.y, 'saw-old');
  assert.equal('z' in result.facts, false);
});

test('优先级仲裁：高优先级压制低优先级', () => {
  const rules: Rule[] = [
    { id: 'low', priority: 1, when: { op: 'exists', field: 'a' }, set: { v: 'low' } },
    { id: 'high', priority: 10, when: { op: 'exists', field: 'a' }, set: { v: 'high' } },
  ];
  const result = runEngine(rules, { a: true });
  assert.equal(result.facts.v, 'high');
  const ev = result.evidence.v;
  assert.equal(ev.status, 'resolved');
  assert.equal(ev.winningPriority, 10);
  const byId = Object.fromEntries(ev.candidates.map((c) => [c.ruleId, c.outcome]));
  assert.deepEqual(byId, { low: 'loser', high: 'winner' });
});

test('同优先级同值：并列胜出，正常解析', () => {
  const rules: Rule[] = [
    { id: 'r1', priority: 5, when: { op: 'exists', field: 'a' }, set: { v: 'same' } },
    { id: 'r2', priority: 5, when: { op: 'exists', field: 'a' }, set: { v: 'same' } },
  ];
  const result = runEngine(rules, { a: 1 });
  assert.equal(result.facts.v, 'same');
  assert.equal(result.conflicts.length, 0);
  const outcomes = result.evidence.v.candidates.map((c) => c.outcome).sort();
  assert.deepEqual(outcomes, ['co-winner', 'co-winner']);
});

test('同优先级异值：报冲突，字段不进入结果', () => {
  const rules: Rule[] = [
    { id: 'r1', priority: 5, when: { op: 'exists', field: 'a' }, set: { v: 'x' } },
    { id: 'r2', priority: 5, when: { op: 'exists', field: 'a' }, set: { v: 'y' } },
    { id: 'r3', priority: 1, when: { op: 'exists', field: 'a' }, set: { v: 'z' } },
  ];
  const result = runEngine(rules, { a: 1 });
  assert.equal('v' in result.facts, false);
  assert.equal(result.conflicts.length, 1);
  assert.deepEqual(result.conflicts[0], {
    field: 'v',
    priority: 5,
    values: ['x', 'y'],
    ruleIds: ['r1', 'r2'],
  });
  const ev = result.evidence.v;
  assert.equal(ev.status, 'conflict');
  const byId = Object.fromEntries(ev.candidates.map((c) => [c.ruleId, c.outcome]));
  assert.deepEqual(byId, { r1: 'conflicted', r2: 'conflicted', r3: 'loser' });
});

test('证据树：条件轨迹与规则树结构完整', () => {
  const rules: Rule[] = [
    {
      id: 'combo',
      priority: 3,
      when: {
        op: 'and',
        all: [
          { op: 'gte', field: 'age', value: 18 },
          { op: 'or', any: [{ op: 'eq', field: 'vip', value: true }, { op: 'gt', field: 'score', value: 900 }] },
          { op: 'not', cond: { op: 'eq', field: 'banned', value: true } },
        ],
      },
      set: { tier: 'gold' },
    },
  ];
  const snapshot = { age: 20, vip: false, score: 950, banned: false };
  const result = runEngine(rules, snapshot);
  assert.equal(result.facts.tier, 'gold');

  const ev = result.evidence.tier;
  assert.equal(ev.candidates.length, 1);
  const candidate = ev.candidates[0];
  assert.equal(candidate.outcome, 'winner');
  assert.equal(candidate.proposedValue, 'gold');

  const trace = candidate.trace;
  assert.equal(trace.result, true);
  assert.equal(trace.condition.op, 'and');
  assert.equal(trace.children.length, 3);
  assert.deepEqual(trace.children.map((c) => c.result), [true, true, true]);
  const orNode = trace.children[1];
  assert.equal(orNode.condition.op, 'or');
  assert.deepEqual(orNode.children.map((c) => c.result), [false, true]);
  const notNode = trace.children[2];
  assert.equal(notNode.children[0].result, false);
});

test('证据树：未命中规则的轨迹解释失败原因', () => {
  const rules: Rule[] = [
    {
      id: 'strict',
      priority: 1,
      when: { op: 'and', all: [{ op: 'eq', field: 'a', value: 1 }, { op: 'eq', field: 'b', value: 2 }] },
      set: { v: 'hit' },
    },
  ];
  const result = runEngine(rules, { a: 1, b: 3 });
  const ev = result.evidence.v;
  assert.equal(ev.status, 'unset');
  const trace = ev.candidates[0].trace;
  assert.equal(trace.result, false);
  assert.deepEqual(trace.children.map((c) => c.result), [true, false]);
});

test('主引擎与朴素两阶段参考模型结果一致', () => {
  const rules: Rule[] = [
    { id: 'a', priority: 2, when: { op: 'gt', field: 'n', value: 3 }, set: { p: 'a', q: 1 } },
    { id: 'b', priority: 2, when: { op: 'lt', field: 'n', value: 10 }, set: { p: 'b' } },
    { id: 'c', priority: 1, when: { op: 'exists', field: 'n' }, set: { p: 'c', q: 2 } },
    { id: 'd', priority: 5, when: { op: 'eq', field: 'flag', value: true }, set: { r: null } },
  ];
  const snapshot = { n: 5, flag: true };
  assert.equal(normalizeResult(runEngine(rules, snapshot)), normalizeResult(runReference(rules, snapshot)));
  // 该配置下 p 存在同优先级异值冲突。
  const result = runEngine(rules, snapshot);
  assert.equal(result.evidence.p.status, 'conflict');
  assert.equal(result.facts.q, 1);
  assert.equal(result.facts.r, null);
});
