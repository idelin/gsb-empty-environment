import { Action, PlannerInput, ResourceVector } from './types';
import { createHeap } from './heap';

// 归一化后的动作：目标引用已编译为位掩码
export interface NormAction {
  name: string;
  cost: number;
  consumes: ResourceVector;
  from: string[];
  to: string;
  requiresMask: bigint;
  achievesMask: bigint;
}

export interface AppliedAction {
  state: string;
  mask: bigint;
  remaining: ResourceVector;
  gained: bigint; // 新达成的目标位
}

export interface Model {
  resources: string[];
  initialState: string;
  initialResources: ResourceVector;
  maxSteps: number;
  nearDistance: number;
  goalIds: string[];
  actions: NormAction[];
  bit(ordinal: number): bigint;
  profitOf(ordinal: number): number;
  profitOfMask(mask: bigint): number;
  goalsOfMask(mask: bigint): string[];
  applicable(a: NormAction, state: string, mask: bigint): boolean;
  affordable(a: NormAction, remaining: ResourceVector): boolean;
  apply(a: NormAction, mask: bigint, remaining: ResourceVector): AppliedAction;
  // 从 (state, mask) 出发达成 goal 所需的最小代价；resource 为 null 时表示动作数（图距离），
  // 否则表示该资源的最小消耗下界（忽略其他资源，是合法下界）。不可达返回 null。
  minCostToGoal(state: string, mask: bigint, goal: number, resource: string | null): number | null;
  distanceToGoal(state: string, mask: bigint, goal: number): number | null;
  // 达成 goal 的各资源最小消耗向量；图上不可达返回 null
  minNeedVector(state: string, mask: bigint, goal: number): ResourceVector | null;
  // 在剩余资源内“单独可行”的目标序号列表（上界估计用）
  feasibleGoals(state: string, mask: bigint, remaining: ResourceVector): number[];
  // 收益上界：当前收益 + 所有单独可行目标的收益和（忽略资源共享，是合法上界）
  profitCeiling(baseProfit: number, state: string, mask: bigint, remaining: ResourceVector): number;
}

interface DNode {
  c: number;
  s: string;
  m: bigint;
}

export function createModel(input: PlannerInput): Model {
  const resources: string[] = Object.keys(input.initialResources);
  const initialState: string = input.initialState;
  const initialResources: ResourceVector = { ...input.initialResources };
  const maxSteps: number = input.maxSteps ?? 64;
  const nearDistance: number = input.nearDistance ?? 2;
  const goalIds: string[] = input.goals.map(goal => goal.id);
  const profits: number[] = input.goals.map(goal => goal.profit);
  const goalOrdinal: Map<string, number> = new Map();
  const memo: Map<string, number | null> = new Map();

  if (resources.length === 0) throw new Error('至少需要声明一种不可再生资源');
  for (const res of resources) {
    if (!(initialResources[res] >= 0)) throw new Error('资源初始量非法: ' + res);
  }
  if (!(maxSteps >= 0)) throw new Error('maxSteps 非法');
  const seenGoals: Set<string> = new Set();
  input.goals.forEach((goal, idx) => {
    if (seenGoals.has(goal.id)) throw new Error('目标 id 重复: ' + goal.id);
    seenGoals.add(goal.id);
    if (!(goal.profit >= 0)) throw new Error('目标收益必须非负: ' + goal.id);
    goalOrdinal.set(goal.id, idx);
  });

  function maskOf(ids: string[], ctx: string): bigint {
    let mask = 0n;
    for (const id of ids) {
      const ordinal = goalOrdinal.get(id);
      if (ordinal === undefined) throw new Error('动作 ' + ctx + ' 引用未知目标: ' + id);
      mask |= 1n << BigInt(ordinal);
    }
    return mask;
  }

  function normalize(a: Action): NormAction {
    if (!a.name) throw new Error('动作缺少名称');
    if (!(a.cost >= 0)) throw new Error('动作成本非法: ' + a.name);
    if (!a.from || a.from.length === 0) throw new Error('动作缺少前置状态: ' + a.name);
    const consumes: ResourceVector = {};
    const raw = a.consumes ?? {};
    for (const res of Object.keys(raw)) {
      if (!resources.includes(res)) throw new Error('动作 ' + a.name + ' 消耗了未声明的资源: ' + res);
      if (!(raw[res] >= 0)) throw new Error('动作 ' + a.name + ' 消耗量非法: ' + res);
      if (raw[res] > 0) consumes[res] = raw[res];
    }
    return {
      name: a.name,
      cost: a.cost,
      consumes,
      from: a.from.slice(),
      to: a.to,
      requiresMask: maskOf(a.requiresGoals ?? [], a.name),
      achievesMask: maskOf(a.achieves ?? [], a.name),
    };
  }

  const actions: NormAction[] = input.actions.map(a => normalize(a));

  function bit(ordinal: number): bigint {
    return 1n << BigInt(ordinal);
  }

  function profitOf(ordinal: number): number {
    return profits[ordinal];
  }

  function profitOfMask(mask: bigint): number {
    let total = 0;
    for (let idx = 0; idx < goalIds.length; idx++) {
      if ((mask & bit(idx)) !== 0n) total += profits[idx];
    }
    return total;
  }

  function goalsOfMask(mask: bigint): string[] {
    const out: string[] = [];
    for (let idx = 0; idx < goalIds.length; idx++) {
      if ((mask & bit(idx)) !== 0n) out.push(goalIds[idx]);
    }
    return out;
  }

  function applicable(a: NormAction, state: string, mask: bigint): boolean {
    return a.from.includes(state) && (a.requiresMask & ~mask) === 0n;
  }

  function affordable(a: NormAction, remaining: ResourceVector): boolean {
    for (const res of Object.keys(a.consumes)) {
      if (remaining[res] < a.consumes[res]) return false;
    }
    return true;
  }

  function apply(a: NormAction, mask: bigint, remaining: ResourceVector): AppliedAction {
    const next: ResourceVector = { ...remaining };
    for (const res of Object.keys(a.consumes)) next[res] -= a.consumes[res];
    const gained = a.achievesMask & ~mask;
    return { state: a.to, mask: mask | a.achievesMask, remaining: next, gained };
  }

  function minCostToGoal(state: string, mask: bigint, goal: number, resource: string | null): number | null {
    const key = state + '|' + mask.toString(36) + '|' + goal + '|' + (resource === null ? '-' : resource);
    const hit = memo.get(key);
    if (hit !== undefined) return hit;
    const result = dijkstra(state, mask, goal, resource);
    memo.set(key, result);
    return result;
  }

  // 在 (状态, 目标位掩码) 隐式图上做 Dijkstra，忽略资源约束
  function dijkstra(startState: string, startMask: bigint, goal: number, resource: string | null): number | null {
    const target = bit(goal);
    if ((startMask & target) !== 0n) return 0;
    const dist: Map<string, number> = new Map();
    const nodes: DNode[] = [];
    const heap = createHeap((idA, idB) => nodes[idA].c - nodes[idB].c);
    const keyOf = (s: string, m: bigint): string => s + '|' + m.toString(36);
    dist.set(keyOf(startState, startMask), 0);
    nodes.push({ c: 0, s: startState, m: startMask });
    heap.push(0);
    while (heap.size() > 0) {
      const id = heap.pop();
      if (id === undefined) break;
      const cur = nodes[id];
      const known = dist.get(keyOf(cur.s, cur.m));
      if (known === undefined || cur.c > known) continue;
      if ((cur.m & target) !== 0n) return cur.c;
      for (const action of actions) {
        if (!applicable(action, cur.s, cur.m)) continue;
        const nextMask = cur.m | action.achievesMask;
        const weight = resource === null ? 1 : (action.consumes[resource] ?? 0);
        const nextCost = cur.c + weight;
        const nextKey = keyOf(action.to, nextMask);
        const old = dist.get(nextKey);
        if (old === undefined || nextCost < old) {
          dist.set(nextKey, nextCost);
          nodes.push({ c: nextCost, s: action.to, m: nextMask });
          heap.push(nodes.length - 1);
        }
      }
    }
    return null;
  }

  function distanceToGoal(state: string, mask: bigint, goal: number): number | null {
    return minCostToGoal(state, mask, goal, null);
  }

  function minNeedVector(state: string, mask: bigint, goal: number): ResourceVector | null {
    if ((mask & bit(goal)) !== 0n) return {};
    const need: ResourceVector = {};
    for (const res of resources) {
      const cost = minCostToGoal(state, mask, goal, res);
      if (cost === null) return null;
      need[res] = cost;
    }
    return need;
  }

  function feasibleGoals(state: string, mask: bigint, remaining: ResourceVector): number[] {
    const out: number[] = [];
    for (let goal = 0; goal < goalIds.length; goal++) {
      if ((mask & bit(goal)) !== 0n) continue;
      const need = minNeedVector(state, mask, goal);
      if (need === null) continue;
      let ok = true;
      for (const res of resources) {
        if (need[res] > remaining[res]) {
          ok = false;
          break;
        }
      }
      if (ok) out.push(goal);
    }
    return out;
  }

  function profitCeiling(baseProfit: number, state: string, mask: bigint, remaining: ResourceVector): number {
    let ceiling = baseProfit;
    for (const goal of feasibleGoals(state, mask, remaining)) ceiling += profits[goal];
    return ceiling;
  }

  return {
    resources,
    initialState,
    initialResources,
    maxSteps,
    nearDistance,
    goalIds,
    actions,
    bit,
    profitOf,
    profitOfMask,
    goalsOfMask,
    applicable,
    affordable,
    apply,
    minCostToGoal,
    distanceToGoal,
    minNeedVector,
    feasibleGoals,
    profitCeiling,
  };
}
