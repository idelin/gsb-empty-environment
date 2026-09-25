import { test } from 'node:test';
import assert from 'node:assert/strict';
import { evaluate, type Rule } from '../src/policy.ts';
import { UnknownAttributeError } from '../src/expr.ts';
import type { Attrs } from '../src/expr.ts';

const rules: Rule[] = [
  {
    id: 'r-analyst',
    when: { op: 'in', attr: 'role', values: ['analyst', 'auditor'] },
    effect: 'allow',
    obligations: [
      { kind: 'limit', id: 'ob-lim-analyst', name: 'dailyAmount', bound: 'upper', value: 10000 },
      { kind: 'mask', id: 'ob-mask-analyst', fields: ['ssn'] },
      { kind: 'auditTags', id: 'ob-audit-analyst', tags: ['export'] },
      { kind: 'outputFormat', id: 'ob-fmt-analyst', allowed: ['csv', 'json'] },
    ],
  },
  {
    id: 'r-finance',
    when: { op: 'intCmp', attr: 'amount', cmp: '>=', value: 1000 },
    effect: 'allow',
    obligations: [
      { kind: 'limit', id: 'ob-lim-finance', name: 'dailyAmount', bound: 'upper', value: 5000 },
      { kind: 'mask', id: 'ob-mask-finance', fields: ['iban'] },
      { kind: 'outputFormat', id: 'ob-fmt-finance', allowed: ['json', 'parquet'] },
    ],
  },
  {
    id: 'r-emergency',
    when: { op: 'eq', attr: 'emergency', value: true },
    effect: 'allow',
    suppresses: ['auditTags'],
  },
  {
    id: 'r-deny-huge',
    when: { op: 'intCmp', attr: 'amount', cmp: '>', value: 50000 },
    effect: 'deny',
    reason: 'amount too large',
  },
];

const attrs: Attrs = { role: 'analyst', amount: 4200, emergency: false };

test('allow merges obligations from all matched rules with per-kind algebras', () => {
  const d = evaluate(rules, attrs);
  assert.equal(d.effect, 'allow');
  if (d.effect !== 'allow') return;
  assert.equal(d.merged.limits.dailyAmount.value, 5000); // stricter cap wins
  assert.deepEqual(d.merged.maskFields.fields.sort(), ['iban', 'ssn']); // union
  assert.deepEqual(d.merged.auditTags.tags, ['export']);
  assert.deepEqual(d.merged.outputFormat.allowed, ['json']); // intersection
  assert.deepEqual(d.suppressed, []);
});

test('deny overrides allow and never executes allow obligations', () => {
  const d = evaluate(rules, { ...attrs, amount: 60000 });
  assert.equal(d.effect, 'deny');
  if (d.effect !== 'deny') return;
  assert.deepEqual(d.denyRules, ['r-deny-huge']);
  // transparency: the allow obligations that were not executed are listed
  assert.ok(d.notExecuted.length > 0);
  assert.ok(d.notExecuted.every((r) => r.obligation.kind !== undefined));
});

test('suppression is recorded explicitly, and withdrawal restores obligations', () => {
  const emergencyAttrs = { ...attrs, emergency: true };
  const suppressed = evaluate(rules, emergencyAttrs);
  assert.equal(suppressed.effect, 'allow');
  if (suppressed.effect !== 'allow') return;
  assert.deepEqual(suppressed.merged.auditTags.tags, []); // suppressed by r-emergency
  assert.deepEqual(
    suppressed.suppressed.map((s) => [s.obligation.id, s.byRule]),
    [['ob-audit-analyst', 'r-emergency']],
  );

  // withdraw the suppressing rule: the obligation comes back
  const restored = evaluate(rules, emergencyAttrs, { withdrawn: ['r-emergency'] });
  assert.equal(restored.effect, 'allow');
  if (restored.effect !== 'allow') return;
  assert.deepEqual(restored.merged.auditTags.tags, ['export']);
  assert.deepEqual(restored.suppressed, []);
});

test('mutually exclusive obligations are an explicit failure, not a silent pick', () => {
  const conflicting: Rule[] = [
    {
      id: 'r-csv', when: { op: 'in', attr: 'role', values: ['analyst'] }, effect: 'allow',
      obligations: [{ kind: 'outputFormat', id: 'ob-csv', allowed: ['csv'] }],
    },
    {
      id: 'r-xlsx', when: { op: 'intCmp', attr: 'amount', cmp: '>=', value: 1000 }, effect: 'allow',
      obligations: [{ kind: 'outputFormat', id: 'ob-xlsx', allowed: ['xlsx'] }],
    },
  ];
  const d = evaluate(conflicting, attrs);
  assert.equal(d.effect, 'failure');
  if (d.effect !== 'failure') return;
  assert.equal(d.conflict.kind, 'outputFormat');
  assert.deepEqual(d.conflict.core.map((o) => o.id).sort(), ['ob-csv', 'ob-xlsx']);
});

test('conditions read only the explicit attribute set; unknown attributes throw', () => {
  const bad: Rule[] = [
    { id: 'r-bad', when: { op: 'eq', attr: 'department', value: 'it' }, effect: 'allow' },
  ];
  assert.throws(() => evaluate(bad, attrs), UnknownAttributeError);
});

test('evaluation is pure: frozen attributes are accepted and never mutated', () => {
  const frozen = Object.freeze({ ...attrs });
  const before = JSON.stringify(frozen);
  const d1 = evaluate(rules, frozen);
  const d2 = evaluate(rules, frozen);
  assert.equal(JSON.stringify(frozen), before);
  assert.deepEqual(d1, d2); // deterministic, no external services involved
});
