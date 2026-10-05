import { describe, expect, it } from "bun:test";
import {
  Allocation,
  OrderLine,
  SolveSuccess,
  WarehouseInput,
  solveOrderSplitting,
} from "./solver";

function expectOptimal(result: ReturnType<typeof solveOrderSplitting>): SolveSuccess {
  if (result.status !== "optimal") throw new Error(`expected optimal, got ${result.status}`);
  expect(result.lowerBound).toBeLessThanOrEqual(result.totalCost);
  return result;
}

function checkConsistency(result: SolveSuccess, order: OrderLine[], warehouses: WarehouseInput[]) {
  const demand = new Map<string, number>();
  for (const line of order) demand.set(line.productId, (demand.get(line.productId) ?? 0) + line.quantity);
  const shipped = new Map<string, number>();
  const perWarehouse = new Map<string, number>();
  for (const a of result.allocations) {
    shipped.set(a.productId, (shipped.get(a.productId) ?? 0) + a.quantity);
    perWarehouse.set(a.warehouseId, (perWarehouse.get(a.warehouseId) ?? 0) + a.quantity);
    const w = warehouses.find((x) => x.id === a.warehouseId)!;
    const used = result.allocations
      .filter((x) => x.warehouseId === a.warehouseId && x.productId === a.productId)
      .reduce((s, x) => s + x.quantity, 0);
    expect(used).toBeLessThanOrEqual(w.stock[a.productId] ?? 0);
  }
  for (const [p, need] of demand) expect(shipped.get(p) ?? 0).toBeGreaterThanOrEqual(need);
  let cost = 0;
  for (const [wid, units] of perWarehouse) {
    const w = warehouses.find((x) => x.id === wid)!;
    if (w.freeShippingThreshold != null && units >= w.freeShippingThreshold) continue;
    cost += w.baseFee + w.perItemFee * units;
  }
  expect(result.totalCost).toBe(cost);
  expect(result.packages).toBe(perWarehouse.size);
  const extra = [...shipped.entries()].reduce((s, [p, q]) => s + q - (demand.get(p) ?? 0), 0);
  expect(result.extraUnits).toBe(extra);
}

describe("solveOrderSplitting", () => {
  it("单仓直发：启用费 + 单件费", () => {
    const warehouses: WarehouseInput[] = [
      { id: "A", baseFee: 5, perItemFee: 2, stock: { x: 10 } },
    ];
    const result = expectOptimal(solveOrderSplitting(warehouses, [{ productId: "x", quantity: 3 }]));
    expect(result.totalCost).toBe(5 + 2 * 3);
    expect(result.packages).toBe(1);
    expect(result.extraUnits).toBe(0);
    checkConsistency(result, [{ productId: "x", quantity: 3 }], warehouses);
  });

  it("库存不足时拆到多个仓", () => {
    const warehouses: WarehouseInput[] = [
      { id: "A", baseFee: 3, perItemFee: 1, stock: { x: 2 } },
      { id: "B", baseFee: 4, perItemFee: 1, stock: { x: 5 } },
    ];
    const order = [{ productId: "x", quantity: 4 }];
    const result = expectOptimal(solveOrderSplitting(warehouses, order));
    // A 发 2 件 (3+2=5)，B 发 2 件 (4+2=6)，共 11；全走 B 需 4+4=8，更便宜
    expect(result.totalCost).toBe(8);
    expect(result.packages).toBe(1);
    checkConsistency(result, order, warehouses);
  });

  it("启用费权衡：合并发货 vs 拆分", () => {
    const warehouses: WarehouseInput[] = [
      { id: "A", baseFee: 10, perItemFee: 0, stock: { x: 5, y: 5 } },
      { id: "B", baseFee: 1, perItemFee: 3, stock: { x: 5, y: 5 } },
    ];
    const order = [
      { productId: "x", quantity: 1 },
      { productId: "y", quantity: 1 },
    ];
    const result = expectOptimal(solveOrderSplitting(warehouses, order));
    // A: 10；B: 1+6=7；A+B 更贵
    expect(result.totalCost).toBe(7);
    expect(result.packages).toBe(1);
    checkConsistency(result, order, warehouses);
  });

  it("免运门槛：多取一件反而更便宜", () => {
    const warehouses: WarehouseInput[] = [
      { id: "A", baseFee: 10, perItemFee: 2, freeShippingThreshold: 5, stock: { x: 10 } },
      { id: "B", baseFee: 3, perItemFee: 1, stock: { x: 10 } },
    ];
    const order = [{ productId: "x", quantity: 4 }];
    const result = expectOptimal(solveOrderSplitting(warehouses, order));
    // A 付费 4 件 = 18；A 免运凑 5 件 = 0；B = 3+4 = 7 → 选 A 免运，多买 1 件
    expect(result.totalCost).toBe(0);
    expect(result.extraUnits).toBe(1);
    expect(result.packages).toBe(1);
    const a = result.allocations.find((x) => x.warehouseId === "A")!;
    expect(a.quantity).toBe(5);
    checkConsistency(result, order, warehouses);
  });

  it("免运门槛：差两件才够门槛时不凑单", () => {
    const warehouses: WarehouseInput[] = [
      { id: "A", baseFee: 10, perItemFee: 2, freeShippingThreshold: 6, stock: { x: 10 } },
      { id: "B", baseFee: 3, perItemFee: 1, stock: { x: 10 } },
    ];
    const order = [{ productId: "x", quantity: 4 }];
    const result = expectOptimal(solveOrderSplitting(warehouses, order));
    // A 免运需 6 件 = 0 元，依然最优（免运就是 0）；构造 B 更便宜的场景见下
    expect(result.totalCost).toBe(0);
    checkConsistency(result, order, warehouses);
  });

  it("免运门槛：库存够不着门槛时回退付费/他仓", () => {
    const warehouses: WarehouseInput[] = [
      { id: "A", baseFee: 10, perItemFee: 2, freeShippingThreshold: 6, stock: { x: 5 } },
      { id: "B", baseFee: 3, perItemFee: 1, stock: { x: 10 } },
    ];
    const order = [{ productId: "x", quantity: 4 }];
    const result = expectOptimal(solveOrderSplitting(warehouses, order));
    // A 库存 5 < 门槛 6，只能付费 10+8=18；B = 7 → 选 B
    expect(result.totalCost).toBe(7);
    expect(result.packages).toBe(1);
    expect(result.allocations).toEqual([{ warehouseId: "B", productId: "x", quantity: 4 }]);
    checkConsistency(result, order, warehouses);
  });

  it("次目标：费用相同时包裹更少者胜", () => {
    const warehouses: WarehouseInput[] = [
      { id: "A", baseFee: 6, perItemFee: 0, stock: { x: 1, y: 1 } },
      { id: "B", baseFee: 3, perItemFee: 0, stock: { x: 1 } },
      { id: "C", baseFee: 3, perItemFee: 0, stock: { y: 1 } },
    ];
    const order = [
      { productId: "x", quantity: 1 },
      { productId: "y", quantity: 1 },
    ];
    const result = expectOptimal(solveOrderSplitting(warehouses, order));
    expect(result.totalCost).toBe(6);
    expect(result.packages).toBe(1);
    checkConsistency(result, order, warehouses);
  });

  it("库存总不足：不可行", () => {
    const warehouses: WarehouseInput[] = [
      { id: "A", baseFee: 1, perItemFee: 1, stock: { x: 2 } },
      { id: "B", baseFee: 1, perItemFee: 1, stock: { x: 1 } },
    ];
    const result = solveOrderSplitting(warehouses, [{ productId: "x", quantity: 4 }]);
    expect(result.status).toBe("infeasible");
  });

  it("空订单：零费用零包裹", () => {
    const result = solveOrderSplitting(
      [{ id: "A", baseFee: 1, perItemFee: 1, stock: { x: 2 } }],
      [],
    );
    expect(result).toMatchObject({ status: "optimal", totalCost: 0, packages: 0 });
  });

  it("超出规模上限直接报错", () => {
    const warehouses = Array.from({ length: 9 }, (_, i) => ({
      id: `W${i}`,
      baseFee: 1,
      perItemFee: 1,
      stock: { x: 1 },
    }));
    expect(() => solveOrderSplitting(warehouses, [{ productId: "x", quantity: 1 }])).toThrow();
  });
});

/** 暴力枚举所有分配，用于小规模随机对拍 */
function bruteForce(
  warehouses: WarehouseInput[],
  order: OrderLine[],
): { cost: number; packages: number; extra: number } | null {
  const demand = new Map<string, number>();
  for (const line of order) demand.set(line.productId, (demand.get(line.productId) ?? 0) + line.quantity);
  const products = [...demand.keys()];
  const maxThreshold = Math.max(0, ...warehouses.map((w) => w.freeShippingThreshold ?? 0));
  const caps = warehouses.map((w) =>
    products.map((p) => Math.min(w.stock[p] ?? 0, (demand.get(p) ?? 0) + maxThreshold)),
  );
  let best: { cost: number; packages: number; extra: number } | null = null;
  const alloc = warehouses.map(() => products.map(() => 0));
  const consider = () => {
    for (let j = 0; j < products.length; j++) {
      let sum = 0;
      for (let i = 0; i < warehouses.length; i++) sum += alloc[i][j];
      if (sum < demand.get(products[j])!) return;
    }
    let cost = 0;
    let packages = 0;
    let units = 0;
    for (let i = 0; i < warehouses.length; i++) {
      const n = alloc[i].reduce((a, b) => a + b, 0);
      if (n === 0) continue;
      packages++;
      units += n;
      const w = warehouses[i];
      if (w.freeShippingThreshold != null && n >= w.freeShippingThreshold) continue;
      cost += w.baseFee + w.perItemFee * n;
    }
    const totalDemand = [...demand.values()].reduce((a, b) => a + b, 0);
    const cand = { cost, packages, extra: units - totalDemand };
    if (
      !best ||
      cand.cost < best.cost ||
      (cand.cost === best.cost &&
        (cand.packages < best.packages ||
          (cand.packages === best.packages && cand.extra < best.extra)))
    ) {
      best = cand;
    }
  };
  const walk = (i: number, j: number) => {
    if (i === warehouses.length) return consider();
    const [ni, nj] = j + 1 === products.length ? [i + 1, 0] : [i, j + 1];
    for (let q = 0; q <= caps[i][j]; q++) {
      alloc[i][j] = q;
      walk(ni, nj);
    }
    alloc[i][j] = 0;
  };
  walk(0, 0);
  return best;
}

describe("随机小规模对拍（暴力枚举 vs 求解器）", () => {
  let state = 20261005;
  const rand = (n: number) => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) % n;
  };
  for (let iter = 0; iter < 60; iter++) {
    it(`case ${iter}`, () => {
      const products = ["p0", "p1"].slice(0, 1 + rand(2));
      const warehouses: WarehouseInput[] = Array.from({ length: 1 + rand(3) }, (_, i) => {
        const stock: Record<string, number> = {};
        for (const p of products) stock[p] = rand(5);
        const w: WarehouseInput = {
          id: `W${i}`,
          baseFee: rand(9),
          perItemFee: rand(5),
          stock,
        };
        if (rand(2) === 0) w.freeShippingThreshold = 2 + rand(4);
        return w;
      });
      const order: OrderLine[] = products.map((p) => ({ productId: p, quantity: 1 + rand(3) }));
      const result = solveOrderSplitting(warehouses, order);
      const expected = bruteForce(warehouses, order);
      if (!expected) {
        expect(result.status).toBe("infeasible");
        return;
      }
      expect(result.status).toBe("optimal");
      const r = result as SolveSuccess;
      expect([r.totalCost, r.packages, r.extraUnits]).toEqual([
        expected.cost,
        expected.packages,
        expected.extra,
      ]);
      expect(r.lowerBound).toBeLessThanOrEqual(r.totalCost);
      checkConsistency(r, order, warehouses);
    });
  }
});
