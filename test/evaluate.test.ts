import { test } from 'node:test';
import assert from 'node:assert/strict';
import { evaluateSolution, supplierOrderCost } from '../src/evaluate.ts';
import type { Problem, Supplier } from '../src/types.ts';

const supplier: Supplier = {
  id: 'S1',
  freight: { fixed: 50, perUnit: 1, freeAboveSubtotal: 1000 },
  discountTiers: [
    { minSubtotal: 500, discountRate: 0.1 },
    { minSubtotal: 1000, discountRate: 0.2 },
  ],
  items: [{ materialId: 'A', unitPrice: 10, packSize: 1 }],
};

test('整单阶梯：按折前金额命中最高阶梯', () => {
  const cost = supplierOrderCost(supplier, new Map([['A', 60]])); // subtotal 600 -> 10%
  assert.equal(cost.tierIndex, 0);
  assert.ok(Math.abs(cost.discountAmount - 60) < 1e-9);
  // 运费：未满 1000，fixed 50 + 60*1
  assert.ok(Math.abs(cost.freight - 110) < 1e-9);
  assert.ok(Math.abs(cost.total - (600 - 60 + 110)) < 1e-9);
});

test('满额免固定运费 + 更高阶梯', () => {
  const cost = supplierOrderCost(supplier, new Map([['A', 120]])); // subtotal 1200 -> 20%
  assert.equal(cost.tierIndex, 1);
  assert.ok(Math.abs(cost.freight - 120) < 1e-9); // 免 fixed，仅按件
  assert.ok(Math.abs(cost.total - (1200 - 240 + 120)) < 1e-9);
});

test('方案校验：起订量 / 超购上限 / 供应商家数', () => {
  const problem: Problem = {
    maxOverbuyRatio: 0.1,
    maxSuppliers: 1,
    demands: [{ materialId: 'A', demand: 100 }],
    suppliers: [
      { id: 'S1', items: [{ materialId: 'A', unitPrice: 1, moq: 50, packSize: 10 }] },
      { id: 'S2', items: [{ materialId: 'A', unitPrice: 1 }] },
    ],
  };
  // 起订量违规：3 包 = 30 < 50
  const bad = evaluateSolution(problem, [{ supplierId: 'S1', materialId: 'A', packs: 3 }]);
  assert.equal(bad.feasible, false);
  assert.ok(bad.violations.some((v) => v.includes('起订量')));
  // 超购违规：需求 100 上限 110，采 120
  const over = evaluateSolution(problem, [
    { supplierId: 'S1', materialId: 'A', packs: 5 },
    { supplierId: 'S2', materialId: 'A', packs: 70 },
  ]);
  assert.equal(over.feasible, false);
  assert.ok(over.violations.some((v) => v.includes('超购')));
  assert.ok(over.violations.some((v) => v.includes('超过上限 1 家')));
});
