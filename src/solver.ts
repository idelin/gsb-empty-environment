import { DistanceTable } from './graph.ts';
import type {
  PickItem,
  Plan,
  PlannerParams,
  Trip,
  WarehouseGraph,
} from './types.ts';

function popcount(x: number): number {
  let c = 0;
  while (x) {
    x &= x - 1;
    c++;
  }
  return c;
}

/**
 * 精确求解合单拣货规划：
 *  - 货位层：Held-Karp 求任一货位子集的最优往返路线 O(2^m · m^2)
 *  - 拣货项层：子集 DP 枚举分趟方案，容量约束 + 拆单成本 O(3^n)
 * n ≤ 12 时总计算量在毫秒级。
 */
export function planPicking(
  graph: WarehouseGraph,
  items: PickItem[],
  params: PlannerParams,
): Plan {
  const maxItems = params.maxItems ?? 12;
  if (items.length > maxItems) {
    throw new Error(`拣货项数量 ${items.length} 超过上限 ${maxItems}`);
  }
  if (params.capacity <= 0) throw new Error('车容量必须为正数');
  if (params.splitCost < 0) throw new Error('拆单成本不能为负');
  for (const it of items) {
    if (!(it.qty > 0)) throw new Error(`拣货项 ${it.id} 数量必须为正`);
    if (it.qty > params.capacity) {
      throw new Error(`拣货项 ${it.id} 数量 ${it.qty} 超过车容量 ${params.capacity}`);
    }
  }

  const dt = new DistanceTable(graph);
  const n = items.length;
  if (n === 0) return { trips: [], walkingCost: 0, splitCostTotal: 0, totalCost: 0 };

  // 订单编号（位图用）
  const orderIds = [...new Set(items.map((i) => i.orderId))];
  const orderBitOf = items.map((i) => orderIds.indexOf(i.orderId));

  // 不同货位
  const locations = [...new Set(items.map((i) => i.location))];
  const m = locations.length;
  const locIndex = new Map(locations.map((l, i) => [l, i]));

  // depot + 货位 的两两最短路
  const points = [graph.depot, ...locations];
  const d = points.map((a) => points.map((b) => dt.distance(a, b)));

  // ---- Held-Karp：每个货位子集的最优往返行走距离与访问顺序 ----
  const L = 1 << m;
  const INF = Number.POSITIVE_INFINITY;
  const hk = new Float64Array(L * m).fill(INF); // hk[mask*m+j]：depot 出发访问 mask 最后停在 j
  const parent = new Int32Array(L * m).fill(-1);
  const hkWalk = new Float64Array(L).fill(INF); // 往返总距离
  const hkLast = new Int32Array(L).fill(-1);
  for (let j = 0; j < m; j++) {
    hk[(1 << j) * m + j] = d[0][j + 1];
  }
  for (let mask = 1; mask < L; mask++) {
    for (let j = 0; j < m; j++) {
      if (!(mask & (1 << j))) continue;
      const prev = mask ^ (1 << j);
      if (prev === 0) continue;
      let best = INF;
      let bestK = -1;
      for (let k = 0; k < m; k++) {
        if (!(prev & (1 << k))) continue;
        const cand = hk[prev * m + k] + d[k + 1][j + 1];
        if (cand < best) {
          best = cand;
          bestK = k;
        }
      }
      hk[mask * m + j] = best;
      parent[mask * m + j] = bestK;
    }
    let bestEnd = INF;
    let bestJ = -1;
    for (let j = 0; j < m; j++) {
      if (!(mask & (1 << j))) continue;
      const cand = hk[mask * m + j] + d[j + 1][0];
      if (cand < bestEnd) {
        bestEnd = cand;
        bestJ = j;
      }
    }
    hkWalk[mask] = bestEnd;
    hkLast[mask] = bestJ;
  }

  const visitOrder = (locMask: number): number[] => {
    const order: number[] = [];
    let mask = locMask;
    let j = hkLast[mask];
    while (j >= 0) {
      order.push(j);
      const k = parent[mask * m + j];
      mask ^= 1 << j;
      j = k;
    }
    return order.reverse();
  };

  // ---- 拣货项子集 DP ----
  const N = 1 << n;
  const load = new Float64Array(N);
  const ordersOf = new Uint32Array(N);
  const locMaskOf = new Uint32Array(N);
  for (let mask = 1; mask < N; mask++) {
    const bit = mask & -mask;
    const i = 31 - Math.clz32(bit);
    const rest = mask ^ bit;
    load[mask] = load[rest] + items[i].qty;
    ordersOf[mask] = ordersOf[rest] | (1 << orderBitOf[i]);
    locMaskOf[mask] = locMaskOf[rest] | (1 << locIndex.get(items[i].location)!);
  }

  const f = new Float64Array(N).fill(INF);
  const choice = new Uint32Array(N);
  f[0] = 0;
  for (let mask = 1; mask < N; mask++) {
    let sub = mask;
    while (sub > 0) {
      if (load[sub] <= params.capacity) {
        const rest = mask ^ sub;
        const splitTrips = popcount(ordersOf[sub] & ordersOf[rest]);
        const cand = f[rest] + hkWalk[locMaskOf[sub]] + splitTrips * params.splitCost;
        if (cand < f[mask]) {
          f[mask] = cand;
          choice[mask] = sub;
        }
      }
      sub = (sub - 1) & mask;
    }
  }

  // ---- 方案还原 ----
  const trips: Trip[] = [];
  let walking = 0;
  let splitTotal = 0;
  const seenOrders = new Set<string>();
  for (let mask = N - 1; mask > 0; mask ^= choice[mask]) {
    const sub = choice[mask];
    const tripItems = items.filter((_, i) => sub & (1 << i));
    const locMask = locMaskOf[sub];
    const order = visitOrder(locMask);

    // 行走路径：依次拼接相邻点最短路
    const route: string[] = [graph.depot];
    let cur = graph.depot;
    for (const li of order) {
      const seg = dt.path(cur, locations[li]);
      route.push(...seg.slice(1));
      cur = locations[li];
    }
    route.push(...dt.path(cur, graph.depot).slice(1));

    const stops = order.map((li) => {
      const byOrder = new Map<string, number>();
      for (const it of tripItems) {
        if (it.location !== locations[li]) continue;
        byOrder.set(it.orderId, (byOrder.get(it.orderId) ?? 0) + it.qty);
      }
      return {
        location: locations[li],
        picks: [...byOrder.entries()].map(([orderId, qty]) => ({ orderId, qty })),
      };
    });

    const tripOrders = orderIds.filter((_, oi) => ordersOf[sub] & (1 << oi));
    for (const o of tripOrders) if (seenOrders.has(o)) splitTotal += params.splitCost;
    tripOrders.forEach((o) => seenOrders.add(o));

    walking += hkWalk[locMask];
    trips.push({
      index: trips.length + 1,
      stops,
      route,
      distance: hkWalk[locMask],
      load: load[sub],
      orderIds: tripOrders,
    });
  }

  return {
    trips,
    walkingCost: walking,
    splitCostTotal: splitTotal,
    totalCost: walking + splitTotal,
  };
}
