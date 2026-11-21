import { test } from 'node:test';
import assert from 'node:assert/strict';

import { evaluateCondition } from '../src/condition.ts';
import type { ConditionNode, EvalContext } from '../src/types.ts';

function makeContext(overrides: Partial<EvalContext> = {}): EvalContext {
  return {
    principal: {
      id: 'arn:aws:iam::1111:user/alice',
      type: 'user',
      account: '1111',
      tags: { department: 'finance', clearance: 'secret' },
    },
    action: 's3:GetObject',
    resourceId: 'arn:aws:s3:::vault/top-secret.txt',
    resourceTags: { classification: 'secret', owner: 'alice' },
    requestTags: { env: 'prod' },
    ...overrides,
  };
}

test('undefined condition evaluates to true', () => {
  const result = evaluateCondition(undefined, makeContext());
  assert.deepEqual(result, { outcome: true, missingKeys: [] });
});

test('StringEquals with resource tag', () => {
  const node: ConditionNode = {
    kind: 'leaf',
    operator: 'StringEquals',
    key: 'aws:ResourceTag/classification',
    values: ['secret'],
  };
  assert.equal(evaluateCondition(node, makeContext()).outcome, true);
});

test('StringLike wildcard on principal tag', () => {
  const node: ConditionNode = {
    kind: 'leaf',
    operator: 'StringLike',
    key: 'aws:PrincipalTag/department',
    values: ['fin*'],
  };
  assert.equal(evaluateCondition(node, makeContext()).outcome, true);
});

test('negative operator succeeds when none of the values match', () => {
  const node: ConditionNode = {
    kind: 'leaf',
    operator: 'StringNotEquals',
    key: 'aws:PrincipalTag/department',
    values: ['ops', 'sales'],
  };
  assert.equal(evaluateCondition(node, makeContext()).outcome, true);
});

test('numeric operators compare numerically', () => {
  const context = makeContext({ extra: { 'aws:MultiFactorAuthAge': '900' } });
  const lt: ConditionNode = {
    kind: 'leaf',
    operator: 'NumericLessThan',
    key: 'aws:MultiFactorAuthAge',
    values: [1000],
  };
  const gt: ConditionNode = {
    kind: 'leaf',
    operator: 'NumericGreaterThan',
    key: 'aws:MultiFactorAuthAge',
    values: [1000],
  };
  assert.equal(evaluateCondition(lt, context).outcome, true);
  assert.equal(evaluateCondition(gt, context).outcome, false);
});

test('Bool operator parses string booleans', () => {
  const context = makeContext({ extra: { 'aws:MultiFactorAuthPresent': 'true' } });
  const node: ConditionNode = {
    kind: 'leaf',
    operator: 'Bool',
    key: 'aws:MultiFactorAuthPresent',
    values: [true],
  };
  assert.equal(evaluateCondition(node, context).outcome, true);
});

test('missing condition key yields null and records the key', () => {
  const node: ConditionNode = {
    kind: 'leaf',
    operator: 'StringEquals',
    key: 'aws:ResourceTag/nonexistent',
    values: ['x'],
  };
  const result = evaluateCondition(node, makeContext());
  assert.equal(result.outcome, null);
  assert.deepEqual(result.missingKeys, ['aws:ResourceTag/nonexistent']);
});

test('nested and/or/not conditions evaluate correctly', () => {
  const tree: ConditionNode = {
    kind: 'and',
    children: [
      {
        kind: 'leaf',
        operator: 'StringEquals',
        key: 'aws:PrincipalTag/department',
        values: ['finance'],
      },
      {
        kind: 'or',
        children: [
          {
            kind: 'leaf',
            operator: 'StringEquals',
            key: 'aws:ResourceTag/classification',
            values: ['public'],
          },
          {
            kind: 'not',
            children: [
              {
                kind: 'leaf',
                operator: 'Bool',
                key: 'aws:SecureTransport',
                values: [true],
              },
            ],
          },
        ],
      },
    ],
  };

  const insecure = makeContext({ extra: { 'aws:SecureTransport': 'false' } });
  assert.equal(evaluateCondition(tree, insecure).outcome, true);

  const secure = makeContext({ extra: { 'aws:SecureTransport': 'true' } });
  assert.equal(evaluateCondition(tree, secure).outcome, false);
});

test('repeated key inside an AND takes the strictest constraint', () => {
  const tree: ConditionNode = {
    kind: 'and',
    children: [
      {
        kind: 'leaf',
        operator: 'StringLike',
        key: 'aws:PrincipalTag/department',
        values: ['fin*'],
      },
      {
        kind: 'leaf',
        operator: 'StringEquals',
        key: 'aws:PrincipalTag/department',
        values: ['engineering'],
      },
    ],
  };
  assert.equal(evaluateCondition(tree, makeContext()).outcome, false);
});

test('false inside an AND overrides missing-key null', () => {
  const tree: ConditionNode = {
    kind: 'and',
    children: [
      {
        kind: 'leaf',
        operator: 'StringEquals',
        key: 'aws:PrincipalTag/department',
        values: ['ops'],
      },
      {
        kind: 'leaf',
        operator: 'StringEquals',
        key: 'aws:ResourceTag/missing-key',
        values: ['x'],
      },
    ],
  };
  const result = evaluateCondition(tree, makeContext());
  assert.equal(result.outcome, false);
  assert.deepEqual(result.missingKeys, ['aws:ResourceTag/missing-key']);
});

test('ForAnyValue passes when at least one context value matches', () => {
  const context = makeContext({ extra: { 'aws:PrincipalOrgPaths': ['ou-a', 'ou-b'] } });
  const node: ConditionNode = {
    kind: 'leaf',
    quantifier: 'ForAnyValue',
    operator: 'StringEquals',
    key: 'aws:PrincipalOrgPaths',
    values: ['ou-b'],
  };
  assert.equal(evaluateCondition(node, context).outcome, true);
});

test('ForAllValues requires every context value to match', () => {
  const context = makeContext({ extra: { 'aws:PrincipalOrgPaths': ['ou-a', 'ou-b'] } });
  const node: ConditionNode = {
    kind: 'leaf',
    quantifier: 'ForAllValues',
    operator: 'StringEquals',
    key: 'aws:PrincipalOrgPaths',
    values: ['ou-a'],
  };
  assert.equal(evaluateCondition(node, context).outcome, false);
});

test('NOT of a missing key stays missing rather than becoming true', () => {
  const node: ConditionNode = {
    kind: 'not',
    children: [
      {
        kind: 'leaf',
        operator: 'StringEquals',
        key: 'aws:ResourceTag/absent',
        values: ['x'],
      },
    ],
  };
  const result = evaluateCondition(node, makeContext());
  assert.equal(result.outcome, null);
  assert.deepEqual(result.missingKeys, ['aws:ResourceTag/absent']);
});

test('policy variable is resolved from the live context', () => {
  const node: ConditionNode = {
    kind: 'leaf',
    operator: 'StringEquals',
    key: 'aws:ResourceTag/owner',
    values: ['${aws:PrincipalArn}'],
  };
  const owned = makeContext({
    resourceTags: { owner: 'arn:aws:iam::1111:user/alice' },
  });
  assert.equal(evaluateCondition(node, owned).outcome, true);
});

test('missing policy variable is reported as a missing key', () => {
  const node: ConditionNode = {
    kind: 'leaf',
    operator: 'StringEquals',
    key: 'aws:ResourceTag/owner',
    values: ['${aws:PrincipalTag/vpc-id}'],
  };
  const result = evaluateCondition(node, makeContext());
  assert.equal(result.outcome, null);
  assert.deepEqual(result.missingKeys, ['aws:PrincipalTag/vpc-id']);
});
