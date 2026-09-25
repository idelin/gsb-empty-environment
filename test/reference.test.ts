import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  checkByFiniteEnumeration, dependentLimit, limit, mergeObligations,
  type Obligation,
} from '../src/index.ts';

/** 用有限枚举参考检查验证求解器判定与解。 */
function crossCheck(obligations: Obligation[]) {
  const merged = mergeObligations(obligations);
  const solution =
    merged.status === 'ok' ? merged.solution.limits : null;
  const ref = checkByFiniteEnumeration(obligations, solution);
  assert.equal(ref.verdictsAgree, true, '求解器与有限枚举参考判定一致');
  assert.equal(ref.solverSolutionValid, true, '求解器的解在枚举出的合法解之中');
  return { merged, ref };
}

test('有限枚举参考检查：可满足情形', () => {
  const { ref } = crossCheck([
    limit('budget', 'max', 1000, 'ruleA'),
    limit('reserve', 'min', 100, 'ruleB'),
    dependentLimit('budget', 'reserve', 200, 'ruleC'),
  ]);
  assert.equal(ref.referenceVerdict, 'sat');
  assert.ok(ref.satisfyingAssignments.length > 0);
});

test('有限枚举参考检查：不可满足情形', () => {
  const { merged, ref } = crossCheck([
    limit('budget', 'max', 100, 'ruleA'),
    limit('reserve', 'min', 90, 'ruleB'),
    dependentLimit('budget', 'reserve', 50, 'ruleC'),
  ]);
  assert.equal(merged.status, 'unsat');
  assert.equal(ref.referenceVerdict, 'unsat');
  assert.equal(ref.satisfyingAssignments.length, 0);
});

test('有限枚举参考检查：随机生成的限额义务批量交叉验证', () => {
  // 确定性伪随机，覆盖可满足与不可满足两类
  let seed = 42;
  const rand = (n: number) => (seed = (seed * 1103515245 + 12345) % 2 ** 31) % n;
  for (let round = 0; round < 50; round++) {
    const obligations: Obligation[] = [];
    const keys = ['x', 'y', 'z'];
    for (const key of keys) {
      obligations.push(limit(key, 'max', 50 + rand(200), `r-max-${key}`));
      obligations.push(limit(key, 'min', rand(60), `r-min-${key}`));
    }
    if (rand(2) === 0) {
      obligations.push(dependentLimit('x', 'y', rand(80), `r-dep-${round}`));
    }
    if (rand(2) === 0) {
      obligations.push(dependentLimit('y', 'z', rand(80), `r-dep2-${round}`));
    }
    crossCheck(obligations);
  }
});
