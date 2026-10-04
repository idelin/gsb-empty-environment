import { test } from 'node:test';
import assert from 'node:assert/strict';
import { planPicking } from '../src/solver.ts';
import { DistanceTable } from '../src/graph.ts';
import type { PickItem, WarehouseGraph } from '../src/types.ts';

const graph: WarehouseGraph = {
  nodes: ['D', 'M1', 'M2', 'M3', 'T1', 'T2', 'T3', 'B1', 'B2', 'B3'],
  edges: [
    { from: 'D', to: 'M1', dist: 1 },
    { from: 'M1', to: 'M2', dist: 2 },
    { from: 'M2', to: 'M3', dist: 2 },
    { from: 'T1', to: 'T2', dist: 2 },
    { from: 'T2', to: 'T3', dist: 2 },
    { from: 'B1', to: 'B2', dist: 2 },
    { from: 'B2', to: 'B3', dist: 2 },
    { from: 'M1', to: 'T1', dist: 1 },
    { from: 'M2', to: 'T2', dist: 1 },
    { from: 'M3', to: 'T3', dist: 1 },
    { from: 'M1', to: 'B1', dist: 1 },
    { from: 'M2', to: 'B2', dist: 1 },
    { from: 'M3', to: 'B3', dist: 1 },
  ],
  depot: 'D',
};

// ---- 暴力枚举：所有满足容量的集合划分，用于交叉验证 ----
function bruteForce(
  g: WarehouseGraph,
  items: PickItem[],
  capacity: number,
  splitCost: number,
): number {
  const dt = new DistanceTable(g);
  const n = items.length;
  const locOf = items.map((i) => i.location);

  const tspWalk = (block: number[]): number => {
    const locs = [...new Set(block.map((i) => locOf[i]))];
    let best = Infinity;
    const perm = (prefix: string[], rest: string[]): void => {
      if (rest.length === 0) {
        let w = dt.distance(g.depot, prefix[0]);
        for (let i = 1; i < prefix.length; i++) w += dt.distance(prefix[i - 1], prefix[i]);
        w += dt.distance(prefix[prefix.length - 1], g.depot);
        if (w < best) best = w;
        return;
      }
      for (let i = 0; i < rest.length; i++) {
        perm([...prefix, rest[i]], [...rest.slice(0, i), ...rest.slice(i + 1)]);
      }
    };
    perm([], locs);
    return best;
  };

  let best = Infinity;
  const blocks: number[][] = [];
  const blockLoad: number[] = [];
  const rec = (i: number): void => {
    if (i === n) {
      let cost = 0;
      for (const b of blocks) cost += tspWalk(b);
      const tripsPerOrder = new Map<string, number>();
      for (const b of blocks) {
        for (const o of new Set(b.map((x) => items[x].orderId))) {
          tripsPerOrder.set(o, (tripsPerOrder.get(o) ?? 0) + 1);
        }
      }
      for (const t of tripsPerOrder.values()) cost += (t - 1) * splitCost;
      if (cost < best) best = cost;
      return;
    }
    for (let b = 0; b < blocks.length; b++) {
      if (blockLoad[b] + items[i].qty <= capacity) {
        blocks[b].push(i);
        blockLoad[b] += items[i].qty;
        rec(i + 1);
        blocks[b].pop();
        blockLoad[b] -= items[i].qty;
      }
    }
    blocks.push([i]);
    blockLoad.push(items[i].qty);
    rec(i + 1);
    blocks.pop();
    blockLoad.pop();
  };
  rec(0);
  return best;
}

test('单个拣货项：一趟往返', () => {
  const plan = planPicking(graph, [{ id: 'a', orderId: 'O1', location: 'T2', qty: 2 }], {
    capacity: 5,
    splitCost: 3,
  });
  assert.equal(plan.trips.length, 1);
  assert.equal(plan.walkingCost, 2 * new DistanceTable(graph).distance('D', 'T2'));
  assert.equal(plan.splitCostTotal, 0);
  assert.equal(plan.trips[0].load, 2);
  assert.deepEqual(plan.trips[0].stops, [
    { location: 'T2', picks: [{ orderId: 'O1', qty: 2 }] },
  ]);
});

test('同货位不同订单：一趟内区分数量归属', () => {
  const items: PickItem[] = [
    { id: 'a', orderId: 'O1', location: 'T2', qty: 2 },
    { id: 'b', orderId: 'O2', location: 'T2', qty: 3 },
  ];
  const plan = planPicking(graph, items, { capacity: 5, splitCost: 100 });
  assert.equal(plan.trips.length, 1);
  const picks = plan.trips[0].stops[0].picks;
  assert.deepEqual(picks, [
    { orderId: 'O1', qty: 2 },
    { orderId: 'O2', qty: 3 },
  ]);
});

test('拆单成本为 0 时倾向多趟，极大时倾向少趟', () => {
  const items: PickItem[] = [
    { id: 'a', orderId: 'O1', location: 'T1', qty: 3 },
    { id: 'b', orderId: 'O1', location: 'B3', qty: 3 },
  ];
  const cheap = planPicking(graph, items, { capacity: 3, splitCost: 0 });
  const pricey = planPicking(graph, items, { capacity: 3, splitCost: 1000 });
  // 容量 3 强制拆成两趟
  assert.equal(cheap.trips.length, 2);
  assert.equal(pricey.trips.length, 2);
  assert.equal(pricey.splitCostTotal, 1000);

  const roomy = planPicking(graph, items, { capacity: 6, splitCost: 1000 });
  assert.equal(roomy.trips.length, 1);
  assert.equal(roomy.splitCostTotal, 0);
});

test('超限时抛错', () => {
  const many = Array.from({ length: 13 }, (_, i) => ({
    id: `i${i}`,
    orderId: 'O1',
    location: 'T1',
    qty: 1,
  }));
  assert.throws(() => planPicking(graph, many, { capacity: 99, splitCost: 0 }), /超过上限/);
  assert.throws(
    () =>
      planPicking(graph, [{ id: 'x', orderId: 'O1', location: 'T1', qty: 10 }], {
        capacity: 5,
        splitCost: 0,
      }),
    /超过车容量/,
  );
});

test('与暴力枚举交叉验证（随机小规模实例）', () => {
  let seed = 42;
  const rand = (): number => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x80000000;
  };
  const locs = ['T1', 'T2', 'T3', 'B1', 'B2', 'B3', 'M2', 'M3'];
  for (let t = 0; t < 60; t++) {
    const n = 1 + Math.floor(rand() * 8); // 1..8 个拣货项
    const nOrders = 1 + Math.floor(rand() * Math.min(4, n));
    const items: PickItem[] = Array.from({ length: n }, (_, i) => ({
      id: `i${i}`,
      orderId: `O${1 + Math.floor(rand() * nOrders)}`,
      location: locs[Math.floor(rand() * locs.length)],
      qty: 1 + Math.floor(rand() * 3),
    }));
    const maxQty = Math.max(...items.map((i) => i.qty));
    const capacity = maxQty + Math.floor(rand() * 5);
    const splitCost = Math.floor(rand() * 3) * 4; // 0 / 4 / 8
    const plan = planPicking(graph, items, { capacity, splitCost });
    const expected = bruteForce(graph, items, capacity, splitCost);
    assert.equal(
      plan.totalCost,
      expected,
      `实例 ${t}: items=${JSON.stringify(items)} cap=${capacity} split=${splitCost}`,
    );
    // 内部一致性：总成本 = 行走 + 拆单；载荷不超容量；每趟距离与路线一致
    assert.equal(plan.totalCost, plan.walkingCost + plan.splitCostTotal);
    const dt = new DistanceTable(graph);
    for (const trip of plan.trips) {
      assert.ok(trip.load <= capacity);
      let w = 0;
      for (let i = 1; i < trip.route.length; i++) {
        w += dt.distance(trip.route[i - 1], trip.route[i]);
      }
      assert.equal(w, trip.distance);
      assert.equal(trip.route[0], 'D');
      assert.equal(trip.route[trip.route.length - 1], 'D');
    }
  }
});
