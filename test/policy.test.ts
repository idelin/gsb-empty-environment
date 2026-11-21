import { test } from 'node:test';
import assert from 'node:assert/strict';

import { evaluatePolicySet } from '../src/policy.ts';
import type { EvalContext, Policy } from '../src/types.ts';

const context: EvalContext = {
  principal: {
    id: 'arn:aws:iam::1111:user/alice',
    type: 'user',
    account: '1111',
    tags: { department: 'finance' },
  },
  action: 's3:GetObject',
  resourceId: 'arn:aws:s3:::bucket/key',
  resourceTags: { classification: 'secret' },
};

test('explicit deny wins over a matching allow', () => {
  const policies: { policy: Policy }[] = [
    {
      policy: {
        id: 'p1',
        statements: [
          {
            sid: 'AllowAll',
            effect: 'Allow',
            actions: ['s3:*'],
            resources: ['arn:aws:s3:::*'],
          },
        ],
      },
    },
    {
      policy: {
        id: 'p2',
        statements: [
          {
            sid: 'DenySecret',
            effect: 'Deny',
            actions: ['s3:GetObject'],
            resources: ['arn:aws:s3:::bucket/*'],
            condition: {
              kind: 'leaf',
              operator: 'StringEquals',
              key: 'aws:ResourceTag/classification',
              values: ['secret'],
            },
          },
        ],
      },
    },
  ];

  const result = evaluatePolicySet(policies, context);
  assert.equal(result.decision, 'deny');
  assert.equal(result.reason, 'explicit-deny');
  assert.equal(result.deniedPolicyId, 'p2');
  assert.equal(result.deniedStatementSid, 'DenySecret');
});

test('allow succeeds when no deny applies', () => {
  const result = evaluatePolicySet(
    [
      {
        policy: {
          id: 'p1',
          statements: [
            {
              effect: 'Allow',
              actions: ['s3:GetObject'],
              resources: ['arn:aws:s3:::bucket/*'],
            },
          ],
        },
      },
    ],
    context,
  );
  assert.equal(result.decision, 'allow');
  assert.equal(result.reason, 'allowed');
});

test('missing condition key produces condition-key-missing, not explicit-deny', () => {
  const result = evaluatePolicySet(
    [
      {
        policy: {
          id: 'p1',
          statements: [
            {
              sid: 'AllowWithMfa',
              effect: 'Allow',
              actions: ['s3:GetObject'],
              resources: ['arn:aws:s3:::bucket/*'],
              condition: {
                kind: 'leaf',
                operator: 'Bool',
                key: 'aws:MultiFactorAuthPresent',
                values: [true],
              },
            },
          ],
        },
      },
    ],
    context,
  );
  assert.equal(result.decision, 'deny');
  assert.equal(result.reason, 'condition-key-missing');
  assert.deepEqual(result.missingKeys, ['aws:MultiFactorAuthPresent']);
});

test('no matching statement yields no-matching-statement', () => {
  const result = evaluatePolicySet(
    [
      {
        policy: {
          id: 'p1',
          statements: [
            {
              effect: 'Allow',
              actions: ['ec2:RunInstances'],
              resources: ['*'],
            },
          ],
        },
      },
    ],
    context,
  );
  assert.equal(result.decision, 'deny');
  assert.equal(result.reason, 'no-matching-statement');
});

test('condition false is distinct from condition missing', () => {
  const result = evaluatePolicySet(
    [
      {
        policy: {
          id: 'p1',
          statements: [
            {
              effect: 'Allow',
              actions: ['s3:GetObject'],
              resources: ['*'],
              condition: {
                kind: 'leaf',
                operator: 'StringEquals',
                key: 'aws:ResourceTag/classification',
                values: ['public'],
              },
            },
          ],
        },
      },
    ],
    context,
  );
  assert.equal(result.decision, 'deny');
  assert.equal(result.reason, 'no-matching-statement');
  assert.equal(
    result.trace[0].status,
    'condition-false',
  );
});

test('explicit deny with missing key does not fire', () => {
  const result = evaluatePolicySet(
    [
      {
        policy: {
          id: 'p1',
          statements: [
            {
              sid: 'ConditionalDeny',
              effect: 'Deny',
              actions: ['s3:GetObject'],
              resources: ['*'],
              condition: {
                kind: 'leaf',
                operator: 'StringEquals',
                key: 'aws:ResourceTag/absent',
                values: ['x'],
              },
            },
            {
              sid: 'AllowRead',
              effect: 'Allow',
              actions: ['s3:GetObject'],
              resources: ['*'],
            },
          ],
        },
      },
    ],
    context,
  );
  assert.equal(result.decision, 'allow');
  assert.equal(result.matchedStatementSid, 'AllowRead');
});
