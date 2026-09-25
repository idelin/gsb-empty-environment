import type { Input, Policy, Schema } from './model.ts';

/**
 * Demo scenario: an access policy migrated from rule v2025-Q4 to v2026-Q1.
 * The new rule additionally denies heavy beta users with a high quota
 * override, so inputs exist where the two versions disagree.
 */
export const schema: Schema = {
  attrs: {
    userTier: { kind: 'enum', values: ['free', 'pro', 'enterprise'] },
    regions: { kind: 'set', members: ['us', 'eu', 'apac', 'latam'], minSize: 1 },
    requestCount: { kind: 'int', min: 0, max: 10000 },
    betaOptIn: { kind: 'bool' },
    experimentGroup: { kind: 'enum', values: ['A', 'B'] },
    legacyMode: { kind: 'bool' },
    quotaOverride: {
      kind: 'int',
      min: 0,
      max: 100,
      dependsOn: { attr: 'betaOptIn', equals: true },
    },
    tags: { kind: 'set', members: ['internal', 'vip', 'trial'] },
  },
  constraints: [
    // quotaOverride, when present, must be at least 10
    {
      op: 'or',
      args: [
        { op: 'not', arg: { op: 'present', attr: 'quotaOverride' } },
        { op: 'int', attr: 'quotaOverride', cmp: '>=', value: 10 },
      ],
    },
  ],
};

export const policyA: Policy = {
  version: 'v2025-Q4',
  cond: { op: 'int', attr: 'requestCount', cmp: '<=', value: 100 },
};

export const policyB: Policy = {
  version: 'v2026-Q1',
  cond: {
    op: 'and',
    args: [
      { op: 'int', attr: 'requestCount', cmp: '<=', value: 100 },
      {
        op: 'not',
        arg: {
          op: 'and',
          args: [
            { op: 'eq', attr: 'betaOptIn', value: true },
            { op: 'int', attr: 'quotaOverride', cmp: '>=', value: 40 },
            { op: 'int', attr: 'requestCount', cmp: '>=', value: 50 },
          ],
        },
      },
    ],
  },
};

export const failingInput: Input = {
  userTier: 'pro',
  regions: ['us', 'eu', 'apac', 'latam'],
  requestCount: 87,
  betaOptIn: true,
  experimentGroup: 'B',
  legacyMode: false,
  quotaOverride: 64,
  tags: ['internal', 'vip', 'trial'],
};
