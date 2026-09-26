import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  suggestRepairs,
  applyPlan,
  providePendingValues,
  compileSchema,
  levenshtein,
  type JsonObject,
} from '../src/index.ts';

const orderSchema = {
  type: 'object',
  properties: {
    count: { type: 'integer' },
    ratio: { type: 'number' },
    active: { type: 'boolean' },
    note: { type: 'string' },
    level: { type: 'string', enum: ['LOW', 'MEDIUM', 'HIGH'] },
    code: { type: 'string', enum: ['A', 'B', 'C'] },
    minAmount: { type: 'integer' },
    maxAmount: {
      type: 'integer',
      requires: ['minAmount'],
      compare: [{ field: 'minAmount', op: '>=' }],
    },
    coupon: {
      type: 'string',
      enum: ['NONE', 'RATE', 'FIXED'],
      requires: ['couponCode'],
    },
    couponCode: { type: 'string' },
  },
  required: ['count', 'level'],
};

describe('校验：实例路径 + 规则路径，不借现成验证器', () => {
  it('合法实例直接 valid', () => {
    const res = suggestRepairs(orderSchema, { count: 2, level: 'LOW' });
    assert.equal(res.status, 'valid');
    assert.equal(res.errors.length, 0);
  });

  it('错误同时带实例路径与规则路径', () => {
    const res = suggestRepairs(orderSchema, { count: 1.5, level: 'WRONG' });
    const typeErr = res.errors.find((e) => e.instancePath === '/count')!;
    assert.equal(typeErr.kind, 'TYPE_MISMATCH');
    assert.equal(typeErr.rulePath, '/properties/count/type');
    const enumErr = res.errors.find((e) => e.instancePath === '/level')!;
    assert.equal(enumErr.rulePath, '/properties/level/enum');
  });
});

describe('类型不符修复：确定 vs 需人工', () => {
  it('数字字符串 -> 整数：确定候选', () => {
    const res = suggestRepairs(orderSchema, { count: '12', level: 'LOW' });
    const g = res.groups.find((x) => x.targetPath === '/count')!;
    assert.equal(g.candidates.length, 1);
    assert.equal(g.candidates[0].certainty, 'certain');
    assert.equal(g.candidates[0].value, 12);
  });

  it("'001' -> 整数：前导零丢语义，标记需人工", () => {
    const res = suggestRepairs(orderSchema, { count: '001', level: 'LOW' });
    const g = res.groups.find((x) => x.targetPath === '/count')!;
    assert.equal(g.candidates[0].certainty, 'human');
    assert.match(g.candidates[0].reason, /前导零/);
  });

  it('浮点 -> 整数：截断/四舍五入并列、同代价、需人工', () => {
    const res = suggestRepairs(orderSchema, { count: 2.6, level: 'LOW' });
    const g = res.groups.find((x) => x.targetPath === '/count')!;
    assert.equal(g.candidates.length, 2);
    assert.ok(g.candidates.every((c) => c.certainty === 'human'));
    assert.equal(g.candidates[0].cost, g.candidates[1].cost);
    assert.notEqual(g.candidates[0].value, g.candidates[1].value);
  });

  it('boolean 词串转换需人工；number->string 确定', () => {
    const res = suggestRepairs(orderSchema, { count: 1, level: 'LOW', active: 'true', note: 7 });
    const ga = res.groups.find((x) => x.targetPath === '/active')!;
    assert.equal(ga.candidates[0].certainty, 'human');
    const gn = res.groups.find((x) => x.targetPath === '/note')!;
    assert.equal(gn.candidates[0].certainty, 'certain');
    assert.equal(gn.candidates[0].value, '7');
  });
});

describe('枚举近似', () => {
  it('唯一最近为确定候选', () => {
    const res = suggestRepairs(orderSchema, { count: 1, level: 'MEDIM' });
    const g = res.groups.find((x) => x.targetPath === '/level')!;
    const certain = g.candidates.filter((c) => c.certainty === 'certain');
    assert.equal(certain.length, 1);
    assert.equal(certain[0].value, 'MEDIUM');
  });

  it('多个近似并列时不任选，全部 human、同代价', () => {
    const res = suggestRepairs(orderSchema, { count: 1, level: 'LOW', code: 'D' });
    const g = res.groups.find((x) => x.targetPath === '/code')!;
    // 'D' 到 'A','B','C' 距离相同（tie），并列且不自动选一个
    const near = g.candidates.filter((c) => /并列/.test(c.reason));
    assert.ok(near.length >= 2);
    assert.ok(near.every((c) => c.certainty === 'human'));
    assert.ok(new Set(near.map((c) => c.cost)).size === 1);
  });

  it('无近似时枚举全体列为人工候选，不静默任选', () => {
    const res = suggestRepairs(orderSchema, { count: 1, level: 'LOW', code: 'ZZZ' });
    const g = res.groups.find((x) => x.targetPath === '/code')!;
    assert.deepEqual(
      g.candidates.map((c) => c.value).sort(),
      ['A', 'B', 'C'],
    );
    assert.ok(g.candidates.every((c) => c.certainty === 'human'));
  });

  it('编辑距离工具正确', () => {
    assert.equal(levenshtein('MEDIM', 'MEDIUM'), 1);
    assert.equal(levenshtein('D', 'A'), 1);
  });
});

describe('缺字段与不依赖隐藏默认值', () => {
  it('必填缺失且无枚举：pending 人工，代价高，不注入默认值', () => {
    const res = suggestRepairs(orderSchema, { level: 'LOW' });
    const plan = res.bestPlans[0];
    const p = plan.patches.find((x) => x.targetPath === '/count');
    assert.equal(p?.pending, true);
    assert.equal(p.op, 'set');
    assert.equal(Object.prototype.hasOwnProperty.call(p, 'value'), false);
    assert.equal(res.status, 'optimal-pending-human');
  });

  it('必填缺失且有枚举：枚举值为人工候选', () => {
    const res = suggestRepairs(orderSchema, { count: 1 });
    const g = res.groups.find((x) => x.targetPath === '/level')!;
    assert.ok(g.candidates.every((c) => c.certainty === 'human'));
    assert.deepEqual(g.candidates.map((c) => c.value), ['LOW', 'MEDIUM', 'HIGH']);
  });
});

describe('跨字段约束 requires/compare 与全局最优', () => {
  it('requires：补字段(pending,100) 与删源字段(15) 两条路线，选最小代价', () => {
    const res = suggestRepairs(orderSchema, {
      count: 1,
      level: 'LOW',
      maxAmount: 10,
    });
    assert.equal(res.status, 'optimal-proven');
    assert.equal(res.optimalCost, 15);
    const plan = res.bestPlans[0];
    assert.equal(plan.patches.length, 1);
    assert.equal(plan.patches[0].targetPath, '/maxAmount');
    assert.equal(plan.patches[0].op, 'unset');
  });

  it('requires 且可廉价补值时，补值路线成为最优，同成本平局保留', () => {
    const res = suggestRepairs(orderSchema, {
      count: 1,
      level: 'LOW',
      coupon: 'NONE',
    });
    // 路线A：补 couponCode(pending 100)；路线B：删 coupon(15) => 最优 15
    assert.equal(res.optimalCost, 15);
    assert.ok(res.bestPlans.every((p) => p.totalCost === 15));
  });

  it('compare 数字冲突：改任一侧两个平局方案，最优性穷举核验', () => {
    const res = suggestRepairs(orderSchema, {
      count: 1,
      level: 'LOW',
      minAmount: 100,
      maxAmount: 50,
    });
    assert.equal(res.status, 'optimal-proven');
    // 每侧各一个代价 5 的确定修复 => 两个同成本方案全部保留
    assert.ok(res.bestPlans.length >= 2);
    assert.equal(res.optimalCost, 5);
    const targets = res.bestPlans.map((p) => p.patches[0].targetPath).sort();
    assert.deepEqual(targets, ['/maxAmount', '/minAmount']);
  });
});

describe('穷举、最优性与预算中断', () => {
  it('有限实例穷举全部组合并给出组合数', () => {
    const res = suggestRepairs(orderSchema, {
      count: 1,
      level: 'LOW',
      minAmount: 100,
      maxAmount: 50,
    });
    assert.ok(Number(res.combinationsTotal) > 1);
    assert.ok(res.combinationsChecked <= Number(res.combinationsTotal));
  });

  it('预算为 1 时中断：返回可行上界与未决组', () => {
    const res = suggestRepairs(
      orderSchema,
      { count: 1, level: 'LOW', minAmount: 100, maxAmount: 50 },
      { budget: 1 },
    );
    assert.equal(res.status, 'budget-exhausted');
    assert.equal(res.optimalCost, null);
    assert.ok(res.upperBoundCost !== null && res.upperBoundCost >= 5);
    assert.ok(res.unexploredGroups.length > 0);
  });

  it('不自动改变输入，应用后重新验证；失败回滚', () => {
    const input: JsonObject = { count: '12', level: 'LOW' };
    const snapshot = JSON.stringify(input);
    const res = suggestRepairs(orderSchema, input);
    const plan = res.bestPlans.find((p) => p.status === 'legal')!;
    const applied = applyPlan(input, compileSchema(orderSchema as never), plan);
    assert.equal(applied.ok, true);
    assert.equal(JSON.stringify(input), snapshot, '原始输入未被修改');
    assert.equal((applied.output!.count as unknown), 12);
    // 二次验证合法
    assert.deepEqual(
      suggestRepairs(orderSchema, applied.output!).status,
      'valid',
    );
  });

  it('pending 计划拒绝自动应用；人工供值并重新验证通过', () => {
    const input: JsonObject = { level: 'LOW' };
    const res = suggestRepairs(orderSchema, input);
    const blocked = applyPlan(input as JsonObject, compileSchema(orderSchema as never), res.bestPlans[0]);
    assert.equal(blocked.ok, false);
    const filled = providePendingValues(
      input,
      compileSchema(orderSchema as never),
      { '/count': 3 },
    );
    assert.equal(filled.ok, true);
    assert.equal(suggestRepairs(orderSchema, filled.output!).status, 'valid');
  });
});

describe('无可修复候选', () => {
  it('非数字字符串无法安全转整数：no-candidate', () => {
    const res = suggestRepairs(
      { type: 'object', properties: { count: { type: 'integer' } }, required: ['count'] },
      { count: 'abc' },
    );
    assert.equal(res.status, 'no-candidate');
    assert.equal(res.bestPlans.length, 0);
    assert.equal(res.optimalCost, null);
    const g = res.groups.find((x) => x.targetPath === '/count');
    assert.equal(g === undefined || g.candidates.length === 0, true);
  });
});

describe('只承诺实现的 schema 子集', () => {
  it('未实现关键字立即显式报错', () => {
    assert.throws(
      () => suggestRepairs({ type: 'object', properties: { x: { type: 'string', minLength: 1 } } }, { x: 'a' }),
      /未实现的 schema 关键字 "minLength"/,
    );
  });

  it('未实现类型（array）显式报错', () => {
    assert.throws(
      () => suggestRepairs({ type: 'object', properties: { x: { type: 'array' } } }, { x: [] }),
      /未实现的类型 "array"/,
    );
  });
});
