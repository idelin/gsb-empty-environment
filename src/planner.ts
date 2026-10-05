import { Plan, PlannedMove, Problem, TripLeg } from "./types";

export const MAX_BATCHES = 10;

interface BatchState {
  loc: string;
  /** 货物在该地点可被提取（可开始装货）的绝对分钟 */
  avail: number;
}

interface SearchState {
  batches: BatchState[];
  /** 已使用的班次下标（升序）。班次是单次发车的，不可重复使用 */
  usedTrips: number[];
}

interface MoveRecord {
  tripIndex: number;
  batchIndices: number[];
}

export function validate(problem: Problem): void {
  const { batches, trips, handling } = problem;
  if (batches.length === 0) throw new Error("至少需要一批货物");
  if (batches.length > MAX_BATCHES) throw new Error(`最多支持 ${MAX_BATCHES} 批货物`);
  if (handling.loadTime < 0 || handling.unloadTime < 0) throw new Error("装卸时间不能为负");
  const batchIds = new Set<string>();
  for (const b of batches) {
    if (batchIds.has(b.id)) throw new Error(`批次 id 重复: ${b.id}`);
    batchIds.add(b.id);
    if (!(b.size > 0)) throw new Error(`批次 ${b.id} 的 size 必须为正数`);
    if (!(b.readyTime >= 0)) throw new Error(`批次 ${b.id} 的 readyTime 非法`);
  }
  const tripIds = new Set<string>();
  for (const t of trips) {
    if (tripIds.has(t.id)) throw new Error(`班次 id 重复: ${t.id}`);
    tripIds.add(t.id);
    if (!(t.arrive > t.depart)) {
      throw new Error(`班次 ${t.id} 的到达时刻必须晚于发车时刻（跨午夜请用绝对分钟，如次日 01:00 = 1500）`);
    }
    if (!(t.capacity > 0)) throw new Error(`班次 ${t.id} 的容量必须为正数`);
    if (t.fixedCost < 0 || (t.perUnitCost ?? 0) < 0) throw new Error(`班次 ${t.id} 的费用不能为负`);
    if (t.from === t.to) throw new Error(`班次 ${t.id} 的起点与终点相同`);
  }
}

function keyOf(state: SearchState): string {
  const parts = state.batches.map((b) => `${b.loc}@${b.avail}`).join("|");
  return `${parts}#${state.usedTrips.join(",")}`;
}

function insertSorted(arr: number[], value: number): number[] {
  const out = arr.slice();
  let i = 0;
  while (i < out.length && out[i] < value) i++;
  out.splice(i, 0, value);
  return out;
}

/** 枚举 items 的所有非空子集：总 size 不超过 capacity */
function forEachSubsetWithinCapacity(
  items: number[],
  sizeOf: (index: number) => number,
  capacity: number,
  emit: (subset: number[], totalSize: number) => void,
): void {
  const chosen: number[] = [];
  const rec = (idx: number, remaining: number, total: number): void => {
    if (idx === items.length) {
      if (chosen.length > 0) emit(chosen.slice(), total);
      return;
    }
    rec(idx + 1, remaining, total);
    const s = sizeOf(items[idx]);
    if (s <= remaining) {
      chosen.push(items[idx]);
      rec(idx + 1, remaining - s, total + s);
      chosen.pop();
    }
  };
  rec(0, capacity, 0);
}

class MinHeap {
  private priorities: number[] = [];
  private keys: string[] = [];

  get size(): number {
    return this.keys.length;
  }

  push(priority: number, key: string): void {
    this.priorities.push(priority);
    this.keys.push(key);
    let i = this.keys.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (this.priorities[parent] <= this.priorities[i]) break;
      this.swap(i, parent);
      i = parent;
    }
  }

  pop(): string {
    const top = this.keys[0];
    const lastPri = this.priorities.pop() as number;
    const lastKey = this.keys.pop() as string;
    if (this.keys.length > 0) {
      this.priorities[0] = lastPri;
      this.keys[0] = lastKey;
      let i = 0;
      for (;;) {
        const left = i * 2 + 1;
        const right = left + 1;
        let smallest = i;
        if (left < this.keys.length && this.priorities[left] < this.priorities[smallest]) smallest = left;
        if (right < this.keys.length && this.priorities[right] < this.priorities[smallest]) smallest = right;
        if (smallest === i) break;
        this.swap(i, smallest);
        i = smallest;
      }
    }
    return top;
  }

  private swap(a: number, b: number): void {
    const tp = this.priorities[a];
    this.priorities[a] = this.priorities[b];
    this.priorities[b] = tp;
    const tk = this.keys[a];
    this.keys[a] = this.keys[b];
    this.keys[b] = tk;
  }
}

/**
 * 求全部货物送达的最小总费用方案。
 *
 * 模型要点：
 * - 批次不可拆分；同一次班次可合装多批，但总 size 不得超过班次容量；
 * - 班次是单次发车事件，整个方案中每个班次至多使用一次；
 * - 转运必须计入固定装卸时间：上一步 arrive + unloadTime + loadTime <= 下一班 depart，
 *   即“到站时刻”不等于“可转运时刻”；
 * - 费用 = 班次固定费（载货即收一次）+ perUnitCost * 合装总 size；
 * - 已到达目的地的批次不再移动（费用非负，移走不会更优）。
 */
export function solve(problem: Problem): Plan {
  validate(problem);
  const { batches, trips, handling } = problem;
  const n = batches.length;

  const start: SearchState = {
    batches: batches.map((b) => ({ loc: b.origin, avail: b.readyTime })),
    usedTrips: [],
  };
  const startKey = keyOf(start);

  const dist = new Map<string, number>([[startKey, 0]]);
  const stateOf = new Map<string, SearchState>([[startKey, start]]);
  const parent = new Map<string, { prev: string; move: MoveRecord }>();
  const settled = new Set<string>();
  const heap = new MinHeap();
  heap.push(0, startKey);

  const isGoal = (s: SearchState): boolean =>
    s.batches.every((bs, i) => bs.loc === batches[i].destination);

  let goalKey: string | null = null;

  while (heap.size > 0) {
    const key = heap.pop();
    if (settled.has(key)) continue;
    settled.add(key);
    const state = stateOf.get(key) as SearchState;
    if (isGoal(state)) {
      goalKey = key;
      break;
    }
    const cost = dist.get(key) as number;
    const used = new Set(state.usedTrips);

    for (let t = 0; t < trips.length; t++) {
      if (used.has(t)) continue;
      const trip = trips[t];
      const eligible: number[] = [];
      for (let i = 0; i < n; i++) {
        const bs = state.batches[i];
        if (bs.loc !== trip.from) continue;
        if (bs.loc === batches[i].destination) continue;
        // 计入装货时间：可提时刻 + loadTime 必须不晚于发车时刻
        if (bs.avail + handling.loadTime > trip.depart) continue;
        eligible.push(i);
      }
      if (eligible.length === 0) continue;

      forEachSubsetWithinCapacity(eligible, (i) => batches[i].size, trip.capacity, (subset, totalSize) => {
        const moveCost = trip.fixedCost + (trip.perUnitCost ?? 0) * totalSize;
        const nextBatches = state.batches.map((bs, i) =>
          subset.indexOf(i) >= 0
            ? { loc: trip.to, avail: trip.arrive + handling.unloadTime }
            : bs,
        );
        const nextState: SearchState = {
          batches: nextBatches,
          usedTrips: insertSorted(state.usedTrips, t),
        };
        const nextKey = keyOf(nextState);
        const nextCost = cost + moveCost;
        if (nextCost < (dist.get(nextKey) ?? Infinity)) {
          dist.set(nextKey, nextCost);
          stateOf.set(nextKey, nextState);
          parent.set(nextKey, { prev: key, move: { tripIndex: t, batchIndices: subset.slice() } });
          heap.push(nextCost, nextKey);
        }
      });
    }
  }

  if (goalKey === null) {
    const undeliverable = findUndeliverable(problem);
    return {
      feasible: false,
      totalCost: 0,
      moves: [],
      batches: [],
      undeliverable,
      reason:
        undeliverable.length > 0
          ? "部分批次在班次表内无法送达（班次衔接或单车容量不足）"
          : "班次与运力组合下无法送达全部货物（多批合车受载重限制）",
    };
  }

  const moveRecords: MoveRecord[] = [];
  let cursor = goalKey;
  while (cursor !== startKey) {
    const p = parent.get(cursor) as { prev: string; move: MoveRecord };
    moveRecords.push(p.move);
    cursor = p.prev;
  }
  moveRecords.reverse();

  const legsOf: TripLeg[][] = batches.map(() => []);
  for (const rec of moveRecords) {
    const trip = trips[rec.tripIndex];
    for (const i of rec.batchIndices) {
      legsOf[i].push({ tripId: trip.id, from: trip.from, to: trip.to, depart: trip.depart, arrive: trip.arrive });
    }
  }

  const moves: PlannedMove[] = moveRecords.map((rec) => {
    const trip = trips[rec.tripIndex];
    const totalSize = rec.batchIndices.reduce((sum, i) => sum + batches[i].size, 0);
    return {
      trip,
      batchIds: rec.batchIndices.map((i) => batches[i].id),
      loadBy: trip.depart - handling.loadTime,
      availableAt: trip.arrive + handling.unloadTime,
      totalSize,
      cost: trip.fixedCost + (trip.perUnitCost ?? 0) * totalSize,
    };
  });
  moves.sort((a, b) => a.trip.depart - b.trip.depart || (a.trip.id < b.trip.id ? -1 : 1));

  const batchResults = batches.map((b, i) => {
    const legs = legsOf[i];
    return {
      batchId: b.id,
      legs,
      arrivedAt: legs.length > 0 ? legs[legs.length - 1].arrive + handling.unloadTime : b.readyTime,
    };
  });

  const totalCost = moves.reduce((sum, m) => sum + m.cost, 0);
  return { feasible: true, totalCost, moves, batches: batchResults, undeliverable: [] };
}

/** 单批视角的可达性检查（忽略容量与其他批次），用于不可行时定位问题批次 */
function findUndeliverable(problem: Problem): string[] {
  const { batches, trips, handling } = problem;
  const result: string[] = [];
  for (const b of batches) {
    if (b.origin === b.destination) continue;
    const seen = new Set<string>();
    const queue: BatchState[] = [{ loc: b.origin, avail: b.readyTime }];
    let reachable = false;
    while (queue.length > 0 && !reachable) {
      const cur = queue.pop() as BatchState;
      for (const t of trips) {
        if (t.from !== cur.loc) continue;
        if (cur.avail + handling.loadTime > t.depart) continue;
        const next: BatchState = { loc: t.to, avail: t.arrive + handling.unloadTime };
        if (next.loc === b.destination) {
          reachable = true;
          break;
        }
        const k = `${next.loc}@${next.avail}`;
        if (!seen.has(k)) {
          seen.add(k);
          queue.push(next);
        }
      }
    }
    if (!reachable) result.push(b.id);
  }
  return result;
}
