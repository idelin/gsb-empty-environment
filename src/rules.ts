import type { RuleSet } from './types.ts';

/**
 * 规则库 v1（宽松）：
 * 10 分钟内转账超过 3 次且累计金额超过 10000 才拦截。
 */
export const ruleSetV1: RuleSet = {
  version: 'v1',
  rules: [
    {
      id: 'R-TRANSFER-FREQ',
      description: '10分钟内转账超3次且累计金额超10000',
      action: 'block',
      condition: {
        txTypes: ['transfer'],
        window: {
          windowSeconds: 600,
          txTypes: ['transfer'],
          countGt: 3,
          sumAmountGt: 10000,
        },
      },
    },
    {
      id: 'R-LARGE-SINGLE',
      description: '单笔金额超50000',
      action: 'review',
      condition: { amountGt: 50000 },
    },
  ],
};

/**
 * 规则库 v2（更严格，t=100 热切换上线）：
 * 频次阈值 3 -> 2，累计金额阈值 10000 -> 5000，单笔拦截阈值 50000 -> 20000。
 */
export const ruleSetV2: RuleSet = {
  version: 'v2',
  rules: [
    {
      id: 'R-TRANSFER-FREQ',
      description: '10分钟内转账超2次且累计金额超5000（v2 收紧）',
      action: 'block',
      condition: {
        txTypes: ['transfer'],
        window: {
          windowSeconds: 600,
          txTypes: ['transfer'],
          countGt: 2,
          sumAmountGt: 5000,
        },
      },
    },
    {
      id: 'R-LARGE-SINGLE',
      description: '单笔金额超20000（v2 收紧）',
      action: 'block',
      condition: { amountGt: 20000 },
    },
  ],
};
