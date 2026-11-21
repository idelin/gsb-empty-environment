import { test } from 'node:test';
import assert from 'node:assert/strict';

import { evaluateTrust } from '../src/trust.ts';
import type { Policy, Principal } from '../src/types.ts';

const externalPrincipal: Principal = {
  id: 'arn:aws:iam::2222:role/deployer',
  type: 'role',
  account: '2222',
  tags: { team: 'platform' },
};

const roleAccount = '1111';

test('cross-account assume role allowed when principal matches and condition recomputed', () => {
  const trustPolicy: Policy = {
    id: 'trust',
    statements: [
      {
        effect: 'Allow',
        actions: ['sts:AssumeRole'],
        resources: ['arn:aws:iam::1111:role/deploy-role'],
        principals: ['arn:aws:iam::2222:role/*'],
        condition: {
          kind: 'leaf',
          operator: 'StringEquals',
          key: 'aws:PrincipalTag/team',
          values: ['platform'],
        },
      },
    ],
  };

  const result = evaluateTrust({
    trustPolicy,
    roleAccount,
    principal: externalPrincipal,
    resourceId: 'arn:aws:iam::1111:role/deploy-role',
    action: 'sts:AssumeRole',
  });
  assert.equal(result.decision, 'allow');
  assert.equal(result.reason, 'allowed');
});

test('trust condition is evaluated against the actual principal, not the role owner', () => {
  const trustPolicy: Policy = {
    id: 'trust',
    statements: [
      {
        effect: 'Allow',
        actions: ['sts:AssumeRole'],
        resources: ['*'],
        principals: ['*'],
        condition: {
          kind: 'leaf',
          operator: 'StringEquals',
          key: 'aws:PrincipalTag/team',
          values: ['security'],
        },
      },
    ],
  };

  const result = evaluateTrust({
    trustPolicy,
    roleAccount,
    principal: externalPrincipal,
    resourceId: 'arn:aws:iam::1111:role/deploy-role',
    action: 'sts:AssumeRole',
  });
  assert.equal(result.decision, 'deny');
  assert.equal(result.reason, 'trust-account-mismatch');
});

test('unlisted principal is denied with trust-principal-mismatch', () => {
  const trustPolicy: Policy = {
    id: 'trust',
    statements: [
      {
        effect: 'Allow',
        actions: ['sts:AssumeRole'],
        resources: ['*'],
        principals: ['arn:aws:iam::3333:root'],
      },
    ],
  };

  const result = evaluateTrust({
    trustPolicy,
    roleAccount,
    principal: externalPrincipal,
    resourceId: 'arn:aws:iam::1111:role/deploy-role',
    action: 'sts:AssumeRole',
  });
  assert.equal(result.decision, 'deny');
  assert.equal(result.reason, 'trust-principal-mismatch');
});

test('missing principal tag on trust condition yields condition-key-missing', () => {
  const trustPolicy: Policy = {
    id: 'trust',
    statements: [
      {
        effect: 'Allow',
        actions: ['sts:AssumeRole'],
        resources: ['*'],
        principals: ['*'],
        condition: {
          kind: 'leaf',
          operator: 'StringEquals',
          key: 'aws:PrincipalTag/clearance',
          values: ['secret'],
        },
      },
    ],
  };

  const result = evaluateTrust({
    trustPolicy,
    roleAccount,
    principal: externalPrincipal,
    resourceId: 'arn:aws:iam::1111:role/deploy-role',
    action: 'sts:AssumeRole',
  });
  assert.equal(result.decision, 'deny');
  assert.equal(result.reason, 'condition-key-missing');
  assert.deepEqual(result.missingKeys, ['aws:PrincipalTag/clearance']);
});
