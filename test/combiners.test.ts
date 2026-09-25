import { test } from 'node:test';
import assert from 'node:assert/strict';
import { combine, type BranchOutcome, type CombinedOutcome } from '../src/combiners.ts';
import type { Decision } from '../src/decision.ts';
import type { Obligation } from '../src/obligations.ts';

function branch(source: string, decision: Decision, obligations: readonly Obligation[] = []): BranchOutcome {
  return {
    source,
    decision,
    obligations,
    missingEvidence: [],
    missingValues: [],
    typeErrors: [],
    accessed: [],
  };
}

function permutations<T>(items: readonly T[]): T[][] {
  if (items.length <= 1) return [[...items]];
  return items.flatMap((item, index) =>
    permutations([...items.slice(0, index), ...items.slice(index + 1)]).map((rest) => [item, ...rest]),
  );
}

/** Order-insensitive projection: trace order is intentionally excluded. */
function projection(outcome: CombinedOutcome) {
  return {
    decision: outcome.decision,
    obligations: outcome.obligations,
    conflict: outcome.conflict ?? null,
    missingEvidence: outcome.missingEvidence,
    missingValues: outcome.missingValues,
    typeErrors: outcome.typeErrors,
    accessed: outcome.accessed,
  };
}

test('不适用不覆盖允许（deny-overrides / permit-overrides / first-applicable）', () => {
  const permit = branch('permit-branch', 'Permit', [{ kind: 'quota', resource: 'api', limit: 10 }]);
  const na = branch('na-branch', 'NotApplicable');
  for (const name of ['deny-overrides', 'permit-overrides', 'first-applicable'] as const) {
    const outcome = combine(name, [na, permit]);
    assert.equal(outcome.decision, 'Permit', name);
    assert.deepEqual(outcome.obligations, [{ kind: 'quota', resource: 'api', limit: 10 }], name);
  }
});

test('first-applicable 顺序有意义', () => {
  const permit = branch('permit-rule', 'Permit');
  const deny = branch('deny-rule', 'Deny');
  assert.equal(combine('first-applicable', [permit, deny]).decision, 'Permit');
  assert.equal(combine('first-applicable', [deny, permit]).decision, 'Deny');
});

test('交换律组合（deny/permit-overrides）不受遍历顺序影响', () => {
  const inputs = [
    branch('a', 'Permit', [
      { kind: 'quota', resource: 'api', limit: 10 },
      { kind: 'field-mask', fields: ['ssn', 'email'] },
    ]),
    branch('b', 'Permit', [
      { kind: 'quota', resource: 'api', limit: 5 },
      { kind: 'field-mask', fields: ['dob'] },
    ]),
    branch('c', 'NotApplicable'),
    branch('d', 'Permit', [{ kind: 'output-format', format: 'json' }]),
  ];
  for (const name of ['deny-overrides', 'permit-overrides'] as const) {
    const baseline = projection(combine(name, inputs));
    assert.equal(baseline.decision, 'Permit');
    for (const permuted of permutations(inputs)) {
      assert.deepEqual(projection(combine(name, permuted)), baseline, `${name} @ ${permuted.map((b) => b.source)}`);
    }
  }
});

test('deny-overrides 的 Deny 结论同样与遍历顺序无关', () => {
  const inputs = [
    branch('a', 'Permit', [{ kind: 'quota', resource: 'api', limit: 10 }]),
    branch('b', 'Deny', [{ kind: 'field-mask', fields: ['ssn'] }]),
    branch('c', 'Deny', [{ kind: 'field-mask', fields: ['email'] }]),
    branch('d', 'NotApplicable'),
  ];
  const baseline = projection(combine('deny-overrides', inputs));
  assert.equal(baseline.decision, 'Deny');
  for (const permuted of permutations(inputs)) {
    assert.deepEqual(projection(combine('deny-overrides', permuted)), baseline);
  }
});

test('两个允许分支给出不兼容输出格式 → 义务冲突，不被允许结论掩盖', () => {
  const json = branch('json-branch', 'Permit', [{ kind: 'output-format', format: 'json' }]);
  const xml = branch('xml-branch', 'Permit', [{ kind: 'output-format', format: 'xml' }]);
  for (const name of ['deny-overrides', 'permit-overrides'] as const) {
    const outcome = combine(name, [json, xml]);
    assert.notEqual(outcome.decision, 'Permit', `${name} 不得用 Permit 掩盖义务冲突`);
    assert.equal(outcome.decision, 'Indeterminate', name);
    assert.deepEqual(outcome.conflict, { kind: 'output-format', formats: ['json', 'xml'] });
  }
});
