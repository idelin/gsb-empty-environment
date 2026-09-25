import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { combine, denyOverrides, firstApplicable, permitOverrides } from '../src/combiners.ts';
import { mergeObligations } from '../src/obligations.ts';
import { deny, notApplicable, permit, type EvaluationResult, type Obligation } from '../src/types.ts';

const mask = (id: string, field: string, format: string): Obligation => ({ kind: 'mask', id, field, format });
const quota = (id: string, limit: number): Obligation => ({ kind: 'quota', id, limit });

describe('NotApplicable 不覆盖 Permit', () => {
  it('permit-overrides: [NotApplicable, Permit] => Permit', () => {
    const r = permitOverrides([notApplicable(), permit([quota('q', 5)])]);
    assert.equal(r.decision, 'Permit');
    assert.deepEqual(r.obligations, [quota('q', 5)]);
  });

  it('deny-overrides: [NotApplicable, Permit] => Permit', () => {
    const r = denyOverrides([notApplicable(), permit()]);
    assert.equal(r.decision, 'Permit');
  });

  it('permit-overrides: 全部 NotApplicable 才是 NotApplicable', () => {
    assert.equal(permitOverrides([notApplicable(), notApplicable()]).decision, 'NotApplicable');
  });
});

describe('first-applicable 顺序有意义', () => {
  const a: EvaluationResult = permit([quota('daily', 10)]);
  const b: EvaluationResult = deny();

  it('[Permit, Deny] => Permit', () => {
    assert.equal(firstApplicable([a, b]).decision, 'Permit');
  });

  it('[Deny, Permit] => Deny', () => {
    assert.equal(firstApplicable([b, a]).decision, 'Deny');
  });

  it('跳过 NotApplicable 取第一个可适用结果', () => {
    const r = firstApplicable([notApplicable(), a, b]);
    assert.equal(r.decision, 'Permit');
    assert.deepEqual(r.obligations, [quota('daily', 10)]);
  });
});

describe('交换律组合器不受遍历顺序影响', () => {
  const results: EvaluationResult[] = [
    notApplicable(),
    permit([quota('daily', 10), mask('m1', 'email', 'hash')]),
    permit([quota('daily', 3), mask('m2', 'phone', 'redact')]),
    notApplicable(),
  ];

  const shuffled: EvaluationResult[] = [results[2], results[3], results[0], results[1]];

  for (const algo of ['deny-overrides', 'permit-overrides'] as const) {
    it(`${algo}: 两种顺序结果一致`, () => {
      const r1 = combine(algo, results);
      const r2 = combine(algo, shuffled);
      assert.equal(r1.decision, r2.decision);
      const sort = (obs: Obligation[]) => [...obs].sort((x, y) => JSON.stringify(x).localeCompare(JSON.stringify(y)));
      assert.deepEqual(sort(r1.obligations), sort(r2.obligations));
    });
  }

  it('同 id 限额合并取更严格值（min）', () => {
    const r = permitOverrides(results);
    assert.equal(r.decision, 'Permit');
    assert.ok(r.obligations.some((o) => o.kind === 'quota' && o.id === 'daily' && o.limit === 3));
  });
});

describe('义务冲突不被允许结论掩盖', () => {
  const left = permit([mask('m1', 'email', 'hash')]);
  const right = permit([mask('m1', 'email', 'plaintext')]);

  it('permit-overrides: 不兼容输出格式 => Indeterminate + 冲突', () => {
    const r = permitOverrides([left, right]);
    assert.equal(r.decision, 'Indeterminate');
    assert.equal(r.conflicts.length, 1);
    assert.match(r.conflicts[0].reason, /incompatible mask formats/);
  });

  it('deny-overrides: 同样暴露冲突而非 Permit', () => {
    const r = denyOverrides([left, right]);
    assert.equal(r.decision, 'Indeterminate');
    assert.equal(r.conflicts.length, 1);
  });

  it('合并代数本身报告冲突', () => {
    const { conflicts } = mergeObligations([[mask('m1', 'email', 'hash')], [mask('m1', 'email', 'plaintext')]]);
    assert.equal(conflicts.length, 1);
  });

  it('相同格式可合并，不产生冲突', () => {
    const { obligations, conflicts } = mergeObligations([[mask('m1', 'email', 'hash')], [mask('m1', 'email', 'hash')]]);
    assert.equal(conflicts.length, 0);
    assert.equal(obligations.length, 1);
  });

  it('deny 优先于义务冲突（deny-overrides）', () => {
    const r = denyOverrides([left, right, deny()]);
    assert.equal(r.decision, 'Deny');
  });
});
