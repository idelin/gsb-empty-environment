import type { RuleSet } from '../src/types.ts';

const MINUTE = 60_000;

export const ruleSetV1: RuleSet = {
  version: 'v1',
  effectiveFrom: 0,
  rules: [
    {
      id: 'R1',
      name: '10分钟内转账超3次且金额超1万',
      decision: 'BLOCK',
      priority: 100,
      tx: { actionIn: ['transfer'], amountGt: 10_000 },
      windows: [{ action: 'transfer', windowMs: 10 * MINUTE, countGt: 3 }],
    },
    {
      id: 'R2',
      name: '单笔转账超5万需人工复核',
      decision: 'REVIEW',
      priority: 50,
      tx: { actionIn: ['transfer'], amountGt: 50_000 },
    },
  ],
};

export const ruleSetV2: RuleSet = {
  version: 'v2',
  effectiveFrom: 100 * MINUTE,
  rules: [
    {
      id: 'R1',
      name: '10分钟内转账超2次且金额超5千(收紧)',
      decision: 'BLOCK',
      priority: 100,
      tx: { actionIn: ['transfer'], amountGt: 5_000 },
      windows: [{ action: 'transfer', windowMs: 10 * MINUTE, countGt: 2 }],
    },
    {
      id: 'R2',
      name: '单笔转账超2万需人工复核(收紧)',
      decision: 'REVIEW',
      priority: 50,
      tx: { actionIn: ['transfer'], amountGt: 20_000 },
    },
  ],
};
