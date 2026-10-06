import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  checkOrderIndependence,
  normalizeResult,
  runEngine,
  runReference,
} from '../src/index.ts';
import type { Condition, FactValue, Rule } from '../src/index.ts';

const fixture: Rule[] = [
  { id: 'base', priority: 0, when: { op: 'exists', field: 'env' }, set: { mode: 'base', retries: 1 } },
  { id: 'prod', priority: 10, when: { op: 'eq', field: 'env', value: 'prod' }, set: { mode: 'prod', retries: 5 } },
  { id: 'staging', priority: 10, when: { op: 'eq', field: 'env', value: 'staging' }, set: { mode: 'staging' } },
  {
    id: 'vip-boost',
    priority: 20,
    when: { op: 'and', all: [{ op: 'eq', field: 'vip', value: true }, { op: 'gte', field: 'score', value: 80 }] },
    set: { retries: 9 },
  },
  // 与 prod 同优先级异值：env=prod 时 mode 冲突。
  { id: 'rogue', priority: 10, when: { op: 'eq', field: 'env', value: 'prod' }, set: { mode: 'rogue' } },
];

test('排序无关性：小规模规则集全排列核对通过', () => {
  const report = checkOrderIndependence(fixture, { env: 'prod', vip: true, score: 90 });
  assert.equal(report.exhaustive, true);
  assert.equal(report.checkedPermutations, 120); // 5! = 120
  assert.equal(report.consistent, true, JSON.stringify(report.mismatches, null, 2));
});

test('排序无关性：冲突结果本身也与顺序无关', () => {
  const result = runEngine(fixture, { env: 'prod', vip: true, score: 90 });
  assert.equal(result.evidence.mode.status, 'conflict');
  assert.equal(result.facts.retries, 9);
  const reversed = runEngine([...fixture].reverse(), { env: 'prod', vip: true, score: 90 });
  assert.equal(normalizeResult(result), normalizeResult(reversed));
});

test('排序无关性：大规则集使用确定性洗牌核对', () => {
  const many: Rule[] = Array.from({ length: 30 }, (_, i) => ({
    id: `rule-${i}`,
    priority: i % 7,
    when: { op: 'gte', field: 'n', value: i } as Condition,
    set: { [`f${i % 5}`]: i },
  }));
  const report = checkOrderIndependence(many, { n: 15 }, { shuffles: 24 });
  assert.equal(report.exhaustive, false);
  assert.equal(report.checkedPermutations, 28); // 4 固定排列 + 24 洗牌
  assert.equal(report.consistent, true, JSON.stringify(report.mismatches, null, 2));
});

test('双模型一致性：确定性随机规则集上主引擎等于参考模型', () => {
  // 简单可重现的伪随机生成器。
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  const fields = ['fa', 'fb', 'fc', 'fd'];
  const values: FactValue[] = [0, 1, 'x', 'y', true, null];

  for (let round = 0; round < 50; round++) {
    const rules: Rule[] = Array.from({ length: 8 }, (_, i) => {
      const condField = fields[Math.floor(rand() * fields.length)];
      const cond: Condition =
        rand() < 0.5
          ? { op: 'eq', field: condField, value: values[Math.floor(rand() * values.length)] }
          : { op: 'gte', field: 'n', value: Math.floor(rand() * 10) };
      return {
        id: `r${round}-${i}`,
        priority: Math.floor(rand() * 4),
        when: cond,
        set: { [fields[Math.floor(rand() * fields.length)]]: values[Math.floor(rand() * values.length)] },
      };
    });
    const snapshot = { n: Math.floor(rand() * 10), fa: 'x', fb: 1 };
    assert.equal(
      normalizeResult(runEngine(rules, snapshot)),
      normalizeResult(runReference(rules, snapshot)),
      `round ${round} 双模型结果不一致`,
    );
  }
});
