import { test } from 'node:test';
import assert from 'node:assert/strict';

import { PermissionEngine } from '../src/engine.ts';
import { ResourceTree } from '../src/hierarchy.ts';
import type {
  AccessRequest,
  Policy,
  Principal,
  ResourceNode,
} from '../src/types.ts';

const alice: Principal = {
  id: 'arn:aws:iam::1111:user/alice',
  type: 'user',
  account: '1111',
  tags: { department: 'finance' },
};

const identityAllow: Policy = {
  id: 'identity-allow',
  statements: [
    {
      sid: 'AllowS3Read',
      effect: 'Allow',
      actions: ['s3:GetObject'],
      resources: ['*'],
    },
  ],
};

test('parent explicit deny cannot be overridden by a child allow', () => {
  const nodes: ResourceNode[] = [
    {
      id: 'bucket',
      policies: [
        {
          id: 'parent-deny',
          statements: [
            {
              sid: 'DenySecretPrefix',
              effect: 'Deny',
              actions: ['s3:GetObject'],
              resources: ['bucket/secret/*'],
            },
          ],
        },
      ],
    },
    {
      id: 'bucket/secret',
      parent: 'bucket',
      policies: [
        {
          id: 'child-allow',
          statements: [
            {
              sid: 'ChildAllow',
              effect: 'Allow',
              actions: ['s3:GetObject'],
              resources: ['bucket/secret/*'],
            },
          ],
        },
      ],
    },
    { id: 'bucket/secret/file.txt', parent: 'bucket/secret' },
  ];

  const engine = new PermissionEngine(new ResourceTree(nodes));
  const request: AccessRequest = {
    principal: alice,
    action: 's3:GetObject',
    resourceId: 'bucket/secret/file.txt',
    identityPolicies: [identityAllow],
  };

  const result = engine.evaluate(request);
  assert.equal(result.decision, 'deny');
  assert.equal(result.reason, 'explicit-deny');
  assert.equal(result.deniedPolicyId, 'parent-deny');
});

test('inherited tags flow into resource tag conditions', () => {
  const tagScopedPolicy: Policy = {
    id: 'tag-policy',
    statements: [
      {
        sid: 'AllowSameDepartment',
        effect: 'Allow',
        actions: ['s3:GetObject'],
        resources: ['*'],
        condition: {
          kind: 'leaf',
          operator: 'StringEquals',
          key: 'aws:ResourceTag/department',
          values: ['${aws:PrincipalTag/department}'],
        },
      },
    ],
  };

  const engine = new PermissionEngine(
    new ResourceTree([
      {
        id: 'bucket',
        tags: { department: 'finance' },
        policies: [tagScopedPolicy],
      },
      { id: 'bucket/key', parent: 'bucket' },
    ]),
  );

  const allowed = engine.evaluate({
    principal: alice,
    action: 's3:GetObject',
    resourceId: 'bucket/key',
    identityPolicies: [identityAllow],
  });
  assert.equal(allowed.decision, 'allow');

  const denied = engine.evaluate({
    principal: { ...alice, tags: { department: 'ops' } },
    action: 's3:GetObject',
    resourceId: 'bucket/key',
    identityPolicies: [identityAllow],
  });
  assert.equal(denied.decision, 'deny');
  assert.equal(denied.reason, 'no-matching-statement');
});

test('child tag override changes the inherited-tag decision', () => {
  const engine = new PermissionEngine(
    new ResourceTree([
      {
        id: 'bucket',
        tags: { department: 'finance' },
        policies: [
          {
            id: 'tag-policy',
            statements: [
              {
                effect: 'Allow',
                actions: ['s3:GetObject'],
                resources: ['*'],
                condition: {
                  kind: 'leaf',
                  operator: 'StringEquals',
                  key: 'aws:ResourceTag/department',
                  values: ['finance'],
                },
              },
            ],
          },
        ],
      },
      {
        id: 'bucket/key',
        parent: 'bucket',
        tags: { department: 'ops' },
      },
    ]),
  );

  const result = engine.evaluate({
    principal: alice,
    action: 's3:GetObject',
    resourceId: 'bucket/key',
    identityPolicies: [identityAllow],
  });
  assert.equal(result.decision, 'deny');
});

test('nested condition with inherited principal and resource tags', () => {
  const engine = new PermissionEngine(
    new ResourceTree([
      {
        id: 'bucket',
        tags: { classification: 'secret' },
        policies: [
          {
            id: 'nested-policy',
            statements: [
              {
                effect: 'Allow',
                actions: ['s3:GetObject'],
                resources: ['*'],
                condition: {
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
                },
              },
            ],
          },
        ],
      },
      { id: 'bucket/key', parent: 'bucket' },
    ]),
  );

  const insecure = engine.evaluate({
    principal: alice,
    action: 's3:GetObject',
    resourceId: 'bucket/key',
    identityPolicies: [identityAllow],
    extra: { 'aws:SecureTransport': 'false' },
  });
  assert.equal(insecure.decision, 'allow');

  const secure = engine.evaluate({
    principal: alice,
    action: 's3:GetObject',
    resourceId: 'bucket/key',
    identityPolicies: [identityAllow],
    extra: { 'aws:SecureTransport': 'true' },
  });
  assert.equal(secure.decision, 'deny');
  assert.equal(secure.reason, 'no-matching-statement');
});

test('identity and resource policies must both allow', () => {
  const engine = new PermissionEngine(
    new ResourceTree([
      {
        id: 'bucket',
        policies: [
          {
            id: 'resource-allow',
            statements: [
              {
                effect: 'Allow',
                actions: ['s3:GetObject'],
                resources: ['*'],
              },
            ],
          },
        ],
      },
      { id: 'bucket/key', parent: 'bucket' },
    ]),
  );

  const noIdentity = engine.evaluate({
    principal: alice,
    action: 's3:GetObject',
    resourceId: 'bucket/key',
  });
  assert.equal(noIdentity.decision, 'deny');
  assert.equal(noIdentity.reason, 'no-matching-statement');

  const both = engine.evaluate({
    principal: alice,
    action: 's3:GetObject',
    resourceId: 'bucket/key',
    identityPolicies: [identityAllow],
  });
  assert.equal(both.decision, 'allow');
});

test('identity-only tree still evaluates and preserves missing-key reason', () => {
  const engine = new PermissionEngine(
    new ResourceTree([{ id: 'bucket' }, { id: 'bucket/key', parent: 'bucket' }]),
  );

  const mfaPolicy: Policy = {
    id: 'mfa-required',
    statements: [
      {
        effect: 'Allow',
        actions: ['s3:GetObject'],
        resources: ['*'],
        condition: {
          kind: 'leaf',
          operator: 'Bool',
          key: 'aws:MultiFactorAuthPresent',
          values: [true],
        },
      },
    ],
  };

  const missing = engine.evaluate({
    principal: alice,
    action: 's3:GetObject',
    resourceId: 'bucket/key',
    identityPolicies: [mfaPolicy],
  });
  assert.equal(missing.decision, 'deny');
  assert.equal(missing.reason, 'condition-key-missing');
  assert.deepEqual(missing.missingKeys, ['aws:MultiFactorAuthPresent']);

  const present = engine.evaluate({
    principal: alice,
    action: 's3:GetObject',
    resourceId: 'bucket/key',
    identityPolicies: [mfaPolicy],
    extra: { 'aws:MultiFactorAuthPresent': 'true' },
  });
  assert.equal(present.decision, 'allow');
});
