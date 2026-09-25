import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  dependentLimit, limit, mergeObligations, minimalUnsatCore, limitsSatisfiable,
  type Obligation,
} from '../src/index.ts';

test('依赖限额：合并求出满足全部义务的方案并生成来源证据', () => {
  const result = mergeObligations([
    limit('budget', 'max', 1000, 'ruleA'),
    limit('reserve', 'min', 100, 'ruleB'),
    dependentLimit('budget', 'reserve', 200, 'ruleC'), // budget >= reserve + 200
  ]);
  assert.equal(result.status, 'ok');
  if (result.status !== 'ok') return;
  const { budget, reserve } = result.solution.limits;
  assert.ok(budget! <= 1000, '满足上限');
  assert.ok(reserve! >= 100, '满足下限');
  assert.ok(budget! >= reserve! + 200, '满足依赖约束');
  // 来源证据：依赖关系与贡献者均被记录
  assert.deepEqual(result.evidence.dependencies, [
    { key: 'budget', relativeTo: 'reserve', minGap: 200, source: 'ruleC' },
  ]);
  assert.ok(result.evidence.limits['budget']!.contributors.includes('ruleA'));
  assert.ok(result.evidence.limits['reserve']!.contributors.includes('ruleB'));
});

test('依赖限额不可满足时返回子集最小核', () => {
  const obligations: Obligation[] = [
    limit('budget', 'max', 100, 'ruleA'),
    limit('reserve', 'min', 90, 'ruleB'),
    dependentLimit('budget', 'reserve', 50, 'ruleC'), // budget >= reserve + 50 -> 矛盾
    limit('unrelated', 'max', 5, 'ruleD'),            // 与冲突无关，不应入核
  ];
  const result = mergeObligations(obligations);
  assert.equal(result.status, 'unsat');
  if (result.status !== 'unsat') return;
  const coreSources = result.core.map((o) => o.source).sort();
  assert.deepEqual(coreSources, ['ruleA', 'ruleB', 'ruleC']);
  // 核不可满足
  assert.equal(limitsSatisfiable(result.core), false);
  // 子集最小性：移除任意一条后变得可满足
  for (const ob of result.core) {
    const rest = result.core.filter((o) => o !== ob);
    assert.equal(limitsSatisfiable(rest), true, `移除 ${ob.source} 后可满足`);
  }
});

test('上下界直接冲突（lo > hi）的最小核', () => {
  const obligations: Obligation[] = [
    limit('rows', 'max', 10, 'ruleA'),
    limit('rows', 'min', 20, 'ruleB'),
    limit('rows', 'max', 30, 'ruleC'),
  ];
  const result = mergeObligations(obligations);
  assert.equal(result.status, 'unsat');
  if (result.status !== 'unsat') return;
  // ruleC 的上限 30 比 ruleA 的 10 宽松，最小核只含 ruleA 与 ruleB
  assert.deepEqual(result.core.map((o) => o.source).sort(), ['ruleA', 'ruleB']);
});

test('minimalUnsatCore 对可满足输入抛错', () => {
  assert.throws(
    () => minimalUnsatCore([limit('x', 'max', 5, 'r')], limitsSatisfiable),
    /不可满足/,
  );
});

test('链式依赖限额可解', () => {
  const result = mergeObligations([
    limit('a', 'max', 100, 'ruleA'),
    dependentLimit('a', 'b', 10, 'ruleB'), // a >= b + 10
    dependentLimit('b', 'c', 10, 'ruleC'), // b >= c + 10
    limit('c', 'min', 5, 'ruleD'),
  ]);
  assert.equal(result.status, 'ok');
  if (result.status !== 'ok') return;
  const { a, b, c } = result.solution.limits;
  assert.ok(a! >= b! + 10 && b! >= c! + 10 && c! >= 5 && a! <= 100);
});
