import { test } from 'node:test';
import assert from 'node:assert/strict';
import { solve } from '../src/solver.ts';
import type { OrderItem, Warehouse } from '../src/types.ts';

/** 暴力枚举所有分配方案，返回字典序最优的 (总费用, 包裹数)，用于对拍。 */
function bruteForce(
  order: OrderItem[],
  warehouses: Warehouse[],
  allowOverDelivery: boolean,
): { cost: number; packages: number } | null {
  const skus = [...new Set(order.map((i) => i.sku))];
  const demand = skus.map((s) =>
    order.filter((i) => i.sku === s).reduce((a, i) => a + i.quantity, 0),
  );
  const W = warehouses.length;
  const maxExtra = allowOverDelivery
    ? warehouses.reduce((a, w) => a + (w.freeShippingThreshold ?? 0), 0)
    : 0;
  let best: { cost: number; packages: number } | null = null;
  const alloc: number[][] = warehouses.map(() => new Array(skus.length).fill(0));

  const consider = () => {
    let cost = 0;
    let packages = 0;
    for (let w = 0; w < W; w++) {
      const x = alloc[w].reduce((a, b) => a + b, 0);
      if (x === 0) continue;
      packages++;
      const wh = warehouses[w];
      const thr = wh.freeShippingThreshold;
      if (thr !== undefined && x >= thr) continue; // 免运
      cost += wh.fixedFee + wh.perItemFee * x;
    }
    if (best === null || cost < best.cost || (cost === best.cost && packages < best.packages)) {
      best = { cost, packages };
    }
  };

  const assignSku = (j: number) => {
    if (j === skus.length) {
      consider();
      return;
    }
    const a = new Array(W).fill(0);
    const rec = (w: number, sum: number) => {
      if (w === W) {
        if (sum >= demand[j] && sum <= demand[j] + maxExtra) {
          for (let i = 0; i < W; i++) alloc[i][j] = a[i];
          assignSku(j + 1);
        }
        return;
      }
      const cap = warehouses[w].stock[skus[j]] ?? 0;
      for (let v = 0; v <= cap; v++) {
        a[w] = v;
        rec(w + 1, sum + v);
      }
    };
    rec(0, 0);
  };
  assignSku(0);
  return best;
}

test('单仓基础履约', () => {
  const r = solve(
    [{ sku: 'A', quantity: 2 }],
    [{ id: 'W1', fixedFee: 10, perItemFee: 2, stock: { A: 5 } }],
  );
  assert.equal(r.status, 'optimal');
  assert.equal(r.totalCost, 14);
  assert.equal(r.packages, 1);
  assert.equal(r.lowerBound, 14);
  assert.equal(r.gap, 0);
  assert.deepEqual(r.allocations[0].items, { A: 2 });
});

test('库存不足时拆分到多仓', () => {
  const r = solve(
    [{ sku: 'A', quantity: 4 }],
    [
      { id: 'W1', fixedFee: 5, perItemFee: 1, stock: { A: 2 } },
      { id: 'W2', fixedFee: 5, perItemFee: 1, stock: { A: 3 } },
    ],
  );
  assert.equal(r.totalCost, 14);
  assert.equal(r.packages, 2);
});

test('固定费与单件费的权衡', () => {
  const wh = (n: number): Warehouse[] => [
    { id: 'W1', fixedFee: 100, perItemFee: 0, stock: { A: n } },
    { id: 'W2', fixedFee: 0, perItemFee: 5, stock: { A: n } },
  ];
  assert.equal(solve([{ sku: 'A', quantity: 3 }], wh(3)).totalCost, 15);
  const big = solve([{ sku: 'A', quantity: 30 }], wh(30));
  assert.equal(big.totalCost, 100);
  assert.equal(big.allocations[0].warehouseId, 'W1');
});

test('免运门槛：集中发货触发免运', () => {
  const r = solve(
    [{ sku: 'A', quantity: 5 }],
    [
      { id: 'W1', fixedFee: 0, perItemFee: 3, stock: { A: 5 } },
      { id: 'W2', fixedFee: 10, perItemFee: 1, freeShippingThreshold: 5, stock: { A: 5 } },
    ],
  );
  assert.equal(r.totalCost, 0);
  assert.equal(r.allocations[0].warehouseId, 'W2');
  assert.equal(r.allocations[0].freeShippingApplied, true);
});

test('免运门槛：多取一件反而更便宜（超发凑门槛）', () => {
  const order = [{ sku: 'A', quantity: 4 }];
  const warehouses: Warehouse[] = [
    { id: 'W1', fixedFee: 0, perItemFee: 3, stock: { A: 10 } },
    { id: 'W2', fixedFee: 10, perItemFee: 1, freeShippingThreshold: 5, stock: { A: 10 } },
  ];
  // 允许超发：W2 发 5 件触发免运，总费用 0，比 W1 的 12 便宜
  const r = solve(order, warehouses);
  assert.equal(r.totalCost, 0);
  assert.equal(r.allocations[0].warehouseId, 'W2');
  assert.equal(r.allocations[0].totalItems, 5);
  assert.deepEqual(r.overDelivery, { A: 1 });
  // 禁止超发：只能精确发货，最优为 W1 的 12
  const exact = solve(order, warehouses, { allowOverDelivery: false });
  assert.equal(exact.totalCost, 12);
  assert.deepEqual(exact.overDelivery, {});
});

test('同费用时包裹数最少优先', () => {
  const r = solve(
    [{ sku: 'A', quantity: 2 }],
    [
      { id: 'W1', fixedFee: 5, perItemFee: 0, stock: { A: 2 } },
      { id: 'W2', fixedFee: 2, perItemFee: 0, stock: { A: 1 } },
      { id: 'W3', fixedFee: 3, perItemFee: 0, stock: { A: 1 } },
    ],
  );
  assert.equal(r.totalCost, 5);
  assert.equal(r.packages, 1);
  assert.equal(r.allocations[0].warehouseId, 'W1');
});

test('多 SKU 混合分配', () => {
  const r = solve(
    [
      { sku: 'A', quantity: 2 },
      { sku: 'B', quantity: 1 },
    ],
    [
      { id: 'W1', fixedFee: 8, perItemFee: 1, stock: { A: 2, B: 1 } },
      { id: 'W2', fixedFee: 3, perItemFee: 2, stock: { A: 2 } },
    ],
  );
  assert.equal(r.totalCost, 11);
  assert.equal(r.packages, 1);
});

test('库存总和不满足需求时返回不可行', () => {
  const r = solve(
    [{ sku: 'A', quantity: 5 }],
    [{ id: 'W1', fixedFee: 1, perItemFee: 1, stock: { A: 3 } }],
  );
  assert.equal(r.status, 'infeasible');
  assert.equal(r.totalCost, null);
});

test('空订单费用为 0', () => {
  const r = solve([], [{ id: 'W1', fixedFee: 1, perItemFee: 1, stock: {} }]);
  assert.equal(r.status, 'optimal');
  assert.equal(r.totalCost, 0);
  assert.equal(r.packages, 0);
});

test('无门槛时下界等于最优值', () => {
  const r = solve(
    [
      { sku: 'A', quantity: 3 },
      { sku: 'B', quantity: 2 },
    ],
    [
      { id: 'W1', fixedFee: 6, perItemFee: 2, stock: { A: 3, B: 1 } },
      { id: 'W2', fixedFee: 4, perItemFee: 3, stock: { A: 1, B: 2 } },
      { id: 'W3', fixedFee: 10, perItemFee: 1, stock: { A: 2, B: 2 } },
    ],
  );
  assert.equal(r.status, 'optimal');
  assert.equal(r.lowerBound, r.totalCost);
  assert.equal(r.gap, 0);
});

test('输入校验', () => {
  assert.throws(
    () =>
      solve(
        [{ sku: 'A', quantity: 1 }],
        Array.from({ length: 9 }, (_, i) => ({
          id: `W${i}`,
          fixedFee: 1,
          perItemFee: 1,
          stock: { A: 1 },
        })),
      ),
    /at most 8/,
  );
  assert.throws(
    () =>
      solve(
        Array.from({ length: 13 }, (_, i) => ({ sku: `S${i}`, quantity: 1 })),
        [{ id: 'W1', fixedFee: 1, perItemFee: 1, stock: {} }],
      ),
    /at most 12/,
  );
});

function mulberry32(seed: number): () => number {
  let a = seed;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('随机小规模实例与暴力枚举对拍', () => {
  const rand = mulberry32(20261006);
  const ri = (n: number) => Math.floor(rand() * n);
  for (let iter = 0; iter < 120; iter++) {
    const W = 1 + ri(3);
    const S = 1 + ri(3);
    const skus = Array.from({ length: S }, (_, i) => `S${i}`);
    const order = skus.map((sku) => ({ sku, quantity: ri(3) }));
    const warehouses: Warehouse[] = Array.from({ length: W }, (_, i) => {
      const w: Warehouse = {
        id: `W${i}`,
        fixedFee: ri(9),
        perItemFee: ri(5),
        stock: Object.fromEntries(skus.map((s) => [s, ri(4)])),
      };
      if (rand() < 0.5) w.freeShippingThreshold = 2 + ri(3);
      return w;
    });
    const allowOverDelivery = rand() < 0.7;
    const r = solve(order, warehouses, { allowOverDelivery });
    const b = bruteForce(order, warehouses, allowOverDelivery);
    if (b === null) {
      assert.equal(r.status, 'infeasible', `iter ${iter}: expected infeasible`);
    } else {
      assert.equal(r.status, 'optimal', `iter ${iter}: expected optimal`);
      assert.equal(r.totalCost, b.cost, `iter ${iter}: cost mismatch`);
      assert.equal(r.packages, b.packages, `iter ${iter}: packages mismatch`);
      assert.ok(r.lowerBound !== null && r.lowerBound <= r.totalCost!, `iter ${iter}: LB`);
      // 校验分配确实满足需求
      const shipped = new Map<string, number>();
      for (const a of r.allocations) {
        for (const [sku, q] of Object.entries(a.items)) {
          shipped.set(sku, (shipped.get(sku) ?? 0) + q);
        }
      }
      for (const item of order) {
        assert.ok((shipped.get(item.sku) ?? 0) >= item.quantity, `iter ${iter}: demand`);
      }
    }
  }
});

test('无门槛随机实例下界等于最优值', () => {
  const rand = mulberry32(7);
  const ri = (n: number) => Math.floor(rand() * n);
  for (let iter = 0; iter < 40; iter++) {
    const W = 1 + ri(4);
    const S = 1 + ri(3);
    const skus = Array.from({ length: S }, (_, i) => `S${i}`);
    const order = skus.map((sku) => ({ sku, quantity: 1 + ri(3) }));
    const warehouses: Warehouse[] = Array.from({ length: W }, (_, i) => ({
      id: `W${i}`,
      fixedFee: ri(10),
      perItemFee: ri(5),
      stock: Object.fromEntries(skus.map((s) => [s, 1 + ri(5)])),
    }));
    const r = solve(order, warehouses);
    if (r.status === 'optimal') {
      assert.equal(r.lowerBound, r.totalCost, `iter ${iter}`);
    }
  }
});

test('满规模性能冒烟（8 仓 × 12 SKU）', () => {
  const rand = mulberry32(99);
  const ri = (n: number) => Math.floor(rand() * n);
  const skus = Array.from({ length: 12 }, (_, i) => `S${i}`);
  const order = skus.map((sku) => ({ sku, quantity: 1 + ri(5) }));
  const warehouses: Warehouse[] = Array.from({ length: 8 }, (_, i) => ({
    id: `W${i}`,
    fixedFee: ri(20),
    perItemFee: ri(6),
    freeShippingThreshold: i % 2 === 0 ? 4 + ri(6) : undefined,
    stock: Object.fromEntries(skus.map((s) => [s, ri(12)])),
  }));
  const start = Date.now();
  const r = solve(order, warehouses);
  const elapsed = Date.now() - start;
  assert.ok(r.status === 'optimal' || r.status === 'infeasible');
  assert.ok(elapsed < 10000, `too slow: ${elapsed}ms`);
  console.log(`  full-scale solve: ${elapsed}ms, status=${r.status}, cost=${r.totalCost}`);
});
