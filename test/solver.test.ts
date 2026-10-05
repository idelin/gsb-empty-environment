import { test } from 'node:test';
import assert from 'node:assert/strict';
import { plan } from '../src/planner.ts';
import { solveExact } from '../src/exact.ts';
import { solveHeuristic } from '../src/heuristic.ts';
import type { Problem } from '../src/types.ts';

const close = (a: number, b: number): void => assert.ok(Math.abs(a - b) < 1e-6, `${a} != ${b}`);

test('起订量触发超购：需求 7、起订量 10', () => {
  const problem: Problem = {
    maxOverbuyRatio: 1,
    demands: [{ materialId: 'A', demand: 7 }],
    suppliers: [{ id: 'S', items: [{ materialId: 'A', unitPrice: 5, moq: 10 }] }],
  };
  const result = plan(problem);
  assert.equal(result.status, 'optimal');
  close(result.totalCost, 50);
  assert.equal(result.materials[0]!.purchased, 10);
});

test('整包倍数：需求 7、每包 6 件 -> 12 件', () => {
  const problem: Problem = {
    maxOverbuyRatio: 1,
    demands: [{ materialId: 'A', demand: 7 }],
    suppliers: [{ id: 'S', items: [{ materialId: 'A', unitPrice: 2, packSize: 6 }] }],
  };
  const result = plan(problem);
  assert.equal(result.status, 'optimal');
  close(result.totalCost, 24);
  assert.equal(result.materials[0]!.purchased, 12);
});

test('整单阶梯不能拆成单价贪心：高价供应商触发阶梯后更便宜', () => {
  const problem: Problem = {
    maxOverbuyRatio: 0.2,
    demands: [{ materialId: 'A', demand: 100 }],
    suppliers: [
      { id: '低价无折扣', items: [{ materialId: 'A', unitPrice: 9 }] },
      {
        id: '高价有阶梯',
        discountTiers: [{ minSubtotal: 1000, discountRate: 0.25 }],
        items: [{ materialId: 'A', unitPrice: 10 }],
      },
    ],
  };
  const result = plan(problem);
  assert.equal(result.status, 'optimal');
  // 单价贪心会选 100×9=900；整单最优是 100×10×0.75=750
  close(result.totalCost, 750);
  assert.equal(result.orders.length, 1);
  assert.equal(result.orders[0]!.supplierId, '高价有阶梯');
  assert.equal(result.orders[0]!.tierIndex, 0);
  assert.match(result.orders[0]!.tierRationale, /阶梯#1/);
  assert.match(result.orders[0]!.tierRationale, /1000\.00/);
});

test('为触发整单阶梯而主动超购', () => {
  const problem: Problem = {
    maxOverbuyRatio: 0.2, // 上限 108
    demands: [{ materialId: 'A', demand: 90 }],
    suppliers: [
      {
        id: 'S',
        discountTiers: [{ minSubtotal: 1000, discountRate: 0.3 }],
        items: [{ materialId: 'A', unitPrice: 10 }],
      },
    ],
  };
  const result = plan(problem);
  assert.equal(result.status, 'optimal');
  // 买 90 件 = 900；买 100 件触发七折 = 700；买 108 件 = 756
  close(result.totalCost, 700);
  assert.equal(result.materials[0]!.purchased, 100);
  assert.ok(result.materials[0]!.overbuyRatio <= 0.2 + 1e-9);
});

test('超购上限是硬约束', () => {
  const problem: Problem = {
    maxOverbuyRatio: 0.2, // 上限 12
    demands: [{ materialId: 'A', demand: 10 }],
    suppliers: [{ id: 'S', items: [{ materialId: 'A', unitPrice: 1, packSize: 6 }] }],
  };
  const result = plan(problem);
  assert.equal(result.status, 'optimal');
  assert.equal(result.materials[0]!.purchased, 12); // 2 包 = 12 <= 12，3 包 = 18 违规
  close(result.totalCost, 12);
});

test('供应商交付上限迫使拆单', () => {
  const problem: Problem = {
    demands: [{ materialId: 'A', demand: 100 }],
    suppliers: [
      { id: '便宜但限量', capacity: 60, items: [{ materialId: 'A', unitPrice: 1 }] },
      { id: '贵但充足', items: [{ materialId: 'A', unitPrice: 2 }] },
    ],
  };
  const result = plan(problem);
  assert.equal(result.status, 'optimal');
  close(result.totalCost, 60 * 1 + 40 * 2);
  assert.equal(result.orders.length, 2);
});

test('固定运费促使订单合并到一家', () => {
  const problem: Problem = {
    demands: [
      { materialId: 'A', demand: 10 },
      { materialId: 'B', demand: 10 },
    ],
    suppliers: [
      {
        id: '一站式',
        freight: { fixed: 100 },
        items: [
          { materialId: 'A', unitPrice: 3 },
          { materialId: 'B', unitPrice: 3 },
        ],
      },
      { id: '只卖A', freight: { fixed: 50 }, items: [{ materialId: 'A', unitPrice: 4 }] },
      { id: '只卖B', freight: { fixed: 50 }, items: [{ materialId: 'B', unitPrice: 4 }] },
    ],
  };
  const result = plan(problem);
  assert.equal(result.status, 'optimal');
  // 分开买 (40+50)*2=180；合并 60+100=160
  close(result.totalCost, 160);
  assert.equal(result.orders.length, 1);
  assert.equal(result.orders[0]!.supplierId, '一站式');
});

test('供应商家数上限', () => {
  const problem: Problem = {
    maxSuppliers: 1,
    demands: [{ materialId: 'A', demand: 100 }],
    suppliers: [
      { id: 'S1', capacity: 50, items: [{ materialId: 'A', unitPrice: 5 }] },
      { id: 'S2', items: [{ materialId: 'A', unitPrice: 6 }] },
    ],
  };
  const result = plan(problem);
  assert.equal(result.status, 'optimal');
  assert.equal(result.orders.length, 1);
  close(result.totalCost, 600); // 只能用 S2
});

test('无可行解：产能不足且不允许超购缺口', () => {
  const problem: Problem = {
    demands: [{ materialId: 'A', demand: 100 }],
    suppliers: [{ id: 'S', capacity: 50, items: [{ materialId: 'A', unitPrice: 1 }] }],
  };
  const result = plan(problem);
  assert.equal(result.status, 'infeasible');
});

test('随机小实例：启发式与精确解一致', () => {
  let rngState = 12345;
  const rand = (): number => {
    rngState = (rngState * 1103515245 + 12345) % 2147483648;
    return rngState / 2147483648;
  };
  for (let instance = 0; instance < 8; instance++) {
    const materialCount = 2 + Math.floor(rand() * 2);
    const supplierCount = 2 + Math.floor(rand() * 3);
    const demands = Array.from({ length: materialCount }, (_, m) => ({
      materialId: `M${m}`,
      demand: 10 + Math.floor(rand() * 40),
    }));
    const suppliers = Array.from({ length: supplierCount }, (_, s) => ({
      id: `S${s}`,
      capacity: rand() < 0.5 ? 40 + Math.floor(rand() * 80) : undefined,
      freight: { fixed: Math.floor(rand() * 30) },
      discountTiers:
        rand() < 0.6
          ? [
              { minSubtotal: 200 + Math.floor(rand() * 200), discountRate: 0.05 + rand() * 0.15 },
              { minSubtotal: 600 + Math.floor(rand() * 300), discountRate: 0.2 + rand() * 0.15 },
            ]
          : undefined,
      items: demands
        .filter(() => rand() < 0.8)
        .map((d) => ({
          materialId: d.materialId,
          unitPrice: 1 + Math.floor(rand() * 10),
          moq: rand() < 0.4 ? Math.floor(rand() * 15) : undefined,
          packSize: rand() < 0.5 ? 1 + Math.floor(rand() * 4) : undefined,
        })),
    })).filter((s) => s.items.length > 0);
    // 保证每个物料至少一家供应
    for (const d of demands) {
      if (!suppliers.some((s) => s.items.some((it) => it.materialId === d.materialId))) {
        suppliers[0]!.items.push({ materialId: d.materialId, unitPrice: 5 });
      }
    }
    const problem: Problem = { maxOverbuyRatio: 0.3, demands, suppliers };
    const exact = solveExact(problem, { nodeLimit: 100_000, timeLimitMs: 5_000 });
    const heuristic = solveHeuristic(problem, 12, 7);
    if (!exact.lines) {
      assert.equal(heuristic, null, `实例 ${instance} 应为不可行`);
      continue;
    }
    assert.ok(exact.proven, `实例 ${instance} 应在预算内证完`);
    assert.ok(heuristic, `实例 ${instance} 启发式应找到可行解`);
    assert.ok(Math.abs(heuristic!.cost - exact.cost) < 1e-6, `实例 ${instance}: 启发式 ${heuristic!.cost} != 精确 ${exact.cost}`);
  }
});
