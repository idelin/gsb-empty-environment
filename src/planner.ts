import { RoadGraph } from "./graph.ts";
import type { Plan, Scenario, Stop, Trip } from "./types.ts";

export const MAX_ITEMS = 12;

function popcount(x: number): number {
  let c = 0;
  while (x !== 0) {
    x &= x - 1;
    c++;
  }
  return c;
}

// Exact solver: minimizes total walking distance + order-splitting cost.
//
// Model: pick items (order lines) are partitioned into trips; each trip's
// total quantity must fit the cart capacity. A trip's walking cost is the
// shortest depot-to-depot tour visiting its locations (TSP on the graph's
// shortest-path metric). An order appearing in t trips adds (t-1)*splitCost.
//
// Method (n <= 12 items, m <= 12 distinct locations):
//  1. Held-Karp DP over location subsets -> optimal tour cost per subset.
//  2. Set-partition DP over item subsets. Split cost is decomposable:
//     charging splitCost per distinct order in every trip and subtracting
//     the constant splitCost*numOrders at the end yields exactly
//     sum_over_orders (trips(order) - 1) * splitCost.
export function planPicking(scenario: Scenario): Plan {
  const lines = scenario.lines;
  const n = lines.length;
  if (n === 0) throw new Error("scenario has no pick items");
  if (n > MAX_ITEMS) {
    throw new Error(`too many pick items: ${n} (max ${MAX_ITEMS})`);
  }
  if (scenario.capacity <= 0) throw new Error("capacity must be positive");
  if (scenario.splitCost < 0) throw new Error("splitCost must be >= 0");
  for (const line of lines) {
    if (!Number.isInteger(line.qty) || line.qty <= 0) {
      throw new Error(`invalid qty for ${line.order}@${line.location}`);
    }
    if (line.qty > scenario.capacity) {
      throw new Error(
        `line ${line.order}@${line.location} qty ${line.qty} exceeds capacity`,
      );
    }
  }

  const graph = new RoadGraph(scenario.graph);
  graph.distance(scenario.depot, scenario.depot); // validates depot exists

  const locIndex = new Map<string, number>();
  const orderIndex = new Map<string, number>();
  for (const line of lines) {
    if (!locIndex.has(line.location)) locIndex.set(line.location, locIndex.size);
    if (!orderIndex.has(line.order)) orderIndex.set(line.order, orderIndex.size);
  }
  const m = locIndex.size;
  const numOrders = orderIndex.size;
  const locNames = [...locIndex.keys()];
  const orderNames = [...orderIndex.keys()];

  const qty = lines.map((l) => l.qty);
  const locBit = lines.map((l) => 1 << locIndex.get(l.location)!);
  const ordBit = lines.map((l) => 1 << orderIndex.get(l.order)!);

  const size = 1 << n;
  const qtySum = new Int32Array(size);
  const locMaskOf = new Int32Array(size);
  const ordMaskOf = new Int32Array(size);
  for (let mask = 1; mask < size; mask++) {
    const bit = mask & -mask;
    const i = 31 - Math.clz32(bit);
    const prev = mask ^ bit;
    qtySum[mask] = qtySum[prev] + qty[i];
    locMaskOf[mask] = locMaskOf[prev] | locBit[i];
    ordMaskOf[mask] = ordMaskOf[prev] | ordBit[i];
  }

  // Distance matrix over [depot, ...locations].
  const points = [scenario.depot, ...locNames];
  const d = points.map((a) => points.map((b) => graph.distance(a, b)));

  // Held-Karp: hk[mask][j] = shortest path depot -> visits mask -> ends at j.
  const locSubsetCount = 1 << m;
  const hk = new Float64Array(locSubsetCount * m).fill(Infinity);
  const parent = new Int32Array(locSubsetCount * m).fill(-1);
  for (let j = 0; j < m; j++) hk[(1 << j) * m + j] = d[0][j + 1];
  for (let mask = 1; mask < locSubsetCount; mask++) {
    for (let j = 0; j < m; j++) {
      if ((mask & (1 << j)) === 0) continue;
      const cur = hk[mask * m + j];
      if (!isFinite(cur)) continue;
      for (let k = 0; k < m; k++) {
        if ((mask & (1 << k)) !== 0) continue;
        const nMask = mask | (1 << k);
        const cand = cur + d[j + 1][k + 1];
        if (cand < hk[nMask * m + k]) {
          hk[nMask * m + k] = cand;
          parent[nMask * m + k] = j;
        }
      }
    }
  }
  const tourCost = new Float64Array(locSubsetCount);
  const tourEnd = new Int32Array(locSubsetCount).fill(-1);
  for (let mask = 1; mask < locSubsetCount; mask++) {
    let best = Infinity;
    let bestEnd = -1;
    for (let j = 0; j < m; j++) {
      if ((mask & (1 << j)) === 0) continue;
      const cand = hk[mask * m + j] + d[j + 1][0];
      if (cand < best) {
        best = cand;
        bestEnd = j;
      }
    }
    tourCost[mask] = best;
    tourEnd[mask] = bestEnd;
  }

  // Set-partition DP over item subsets.
  const dp = new Float64Array(size).fill(Infinity);
  const choice = new Int32Array(size).fill(-1);
  dp[0] = 0;
  for (let mask = 1; mask < size; mask++) {
    let sub = mask;
    while (sub > 0) {
      if (qtySum[sub] <= scenario.capacity) {
        const cand =
          dp[mask ^ sub] +
          tourCost[locMaskOf[sub]] +
          scenario.splitCost * popcount(ordMaskOf[sub]);
        if (cand < dp[mask]) {
          dp[mask] = cand;
          choice[mask] = sub;
        }
      }
      sub = (sub - 1) & mask;
    }
  }
  if (!isFinite(dp[size - 1])) throw new Error("no feasible plan");

  // Reconstruct trips.
  const trips: Trip[] = [];
  let rest = size - 1;
  while (rest > 0) {
    const sub = choice[rest];
    trips.push(buildTrip(sub, trips.length));
    rest ^= sub;
  }
  trips.reverse();
  trips.forEach((trip, i) => (trip.index = i));

  const walkingCost = trips.reduce((acc, t) => acc + t.distance, 0);
  const tripsPerOrder = new Map<string, number>();
  for (const trip of trips) {
    const seen = new Set<string>();
    for (const stop of trip.stops) {
      for (const pick of stop.picks) seen.add(pick.order);
    }
    for (const order of seen) {
      tripsPerOrder.set(order, (tripsPerOrder.get(order) ?? 0) + 1);
    }
  }
  let splitCostTotal = 0;
  for (const name of orderNames) {
    const t = tripsPerOrder.get(name) ?? 0;
    if (t > 1) splitCostTotal += (t - 1) * scenario.splitCost;
  }

  return {
    trips,
    walkingCost,
    splitCost: splitCostTotal,
    totalCost: walkingCost + splitCostTotal,
  };

  function buildTrip(sub: number, index: number): Trip {
    const locMask = locMaskOf[sub];
    // Visit order from Held-Karp parents.
    const visitSeq: number[] = [];
    let mm = locMask;
    let j = tourEnd[locMask];
    while (j >= 0) {
      visitSeq.unshift(j);
      const pj = parent[mm * m + j];
      mm ^= 1 << j;
      j = pj;
    }
    // Expand to full node route through the graph.
    const route: string[] = [scenario.depot];
    let prev = scenario.depot;
    for (const locIdx of visitSeq) {
      const segment = graph.path(prev, locNames[locIdx]);
      route.push(...segment.slice(1));
      prev = locNames[locIdx];
    }
    route.push(...graph.path(prev, scenario.depot).slice(1));
    // Stops with per-order quantities (shared locations stay distinguishable).
    const stops: Stop[] = visitSeq.map((locIdx) => {
      const location = locNames[locIdx];
      const picks = lines
        .map((line, i) => ({ line, i }))
        .filter(
          ({ line, i }) => ((sub >> i) & 1) === 1 && line.location === location,
        )
        .map(({ line }) => ({ order: line.order, qty: line.qty }));
      return { location, picks };
    });
    return {
      index,
      stops,
      route,
      distance: tourCost[locMask],
      load: qtySum[sub],
    };
  }
}
