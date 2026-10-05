import type { ChangeSpec, ProblemSpec, Combo, UncoveredPair } from "./types.ts";

export const MAX_CHANGES = 18;

export function popcount(mask: bigint): number {
  let count = 0;
  let m = mask;
  while (m !== 0n) {
    m &= m - 1n;
    count++;
  }
  return count;
}

export function validateSpec(spec: ProblemSpec): string[] {
  const errors: string[] = [];
  if (!spec || typeof spec !== "object") {
    return ["输入必须是包含 budget 与 changes 的 JSON 对象"];
  }
  if (!Number.isInteger(spec.budget) || spec.budget <= 0) {
    errors.push("budget 必须为正整数");
  }
  if (!Array.isArray(spec.changes) || spec.changes.length === 0) {
    errors.push("changes 必须是非空数组");
    return errors;
  }
  if (spec.changes.length > MAX_CHANGES) {
    errors.push(`改动数量 ${spec.changes.length} 超过上限 ${MAX_CHANGES}`);
  }
  const ids = new Set<string>();
  for (const change of spec.changes) {
    if (!change || typeof change.id !== "string" || change.id.length === 0) {
      errors.push("每个改动必须包含非空字符串 id");
      continue;
    }
    if (ids.has(change.id)) {
      errors.push(`改动 id 重复: ${change.id}`);
    }
    ids.add(change.id);
    if (!Number.isInteger(change.cost) || change.cost <= 0) {
      errors.push(`改动 ${change.id} 的 cost 必须为正整数`);
    }
  }
  for (const change of spec.changes) {
    for (const dep of change.deps ?? []) {
      if (dep === change.id) {
        errors.push(`改动 ${change.id} 不能依赖自身`);
      } else if (!ids.has(dep)) {
        errors.push(`改动 ${change.id} 依赖了不存在的改动: ${dep}`);
      }
    }
  }
  return errors;
}

export interface ChangeModel {
  ids: string[];
  costs: number[];
  closureMask: bigint[];
  closureCost: number[];
}

export function buildModel(spec: ProblemSpec): ChangeModel {
  const ids = spec.changes.map((c) => c.id);
  const index = new Map(ids.map((id, i) => [id, i] as const));
  const costs = spec.changes.map((c) => c.cost);
  const n = ids.length;
  const closureMask = ids.map((_, i) => 1n << BigInt(i));
  let changed = true;
  while (changed) {
    changed = false;
    for (let i = 0; i < n; i++) {
      let m = closureMask[i];
      for (const dep of spec.changes[i].deps ?? []) {
        m |= closureMask[index.get(dep)!];
      }
      if (m !== closureMask[i]) {
        closureMask[i] = m;
        changed = true;
      }
    }
  }
  const closureCost = closureMask.map((m) => maskCost(m, costs));
  return { ids, costs, closureMask, closureCost };
}

export function maskCost(mask: bigint, costs: number[]): number {
  let total = 0;
  let m = mask;
  let i = 0;
  while (m !== 0n) {
    if (m & 1n) total += costs[i];
    m >>= 1n;
    i++;
  }
  return total;
}

export function maskToIds(mask: bigint, ids: string[]): string[] {
  const out: string[] = [];
  let m = mask;
  let i = 0;
  while (m !== 0n) {
    if (m & 1n) out.push(ids[i]);
    m >>= 1n;
    i++;
  }
  return out;
}

export function pairBit(i: number, j: number, n: number): bigint {
  return 1n << BigInt(i * n + j);
}

export function pairMaskOf(mask: bigint, n: number): bigint {
  const members: number[] = [];
  let m = mask;
  let i = 0;
  while (m !== 0n) {
    if (m & 1n) members.push(i);
    m >>= 1n;
    i++;
  }
  let pairs = 0n;
  for (let a = 0; a < members.length; a++) {
    for (let b = a + 1; b < members.length; b++) {
      pairs |= pairBit(members[a], members[b], n);
    }
  }
  return pairs;
}

export function enumerateLegalCombos(model: ChangeModel, budget: number): Combo[] {
  const n = model.ids.length;
  const combos: Combo[] = [];
  const total = 1 << n;
  for (let mask = 1; mask < total; mask++) {
    if (popcount(BigInt(mask)) < 2) continue;
    let closed = true;
    let cost = 0;
    for (let i = 0; i < n; i++) {
      if (mask & (1 << i)) {
        if ((model.closureMask[i] & ~BigInt(mask)) !== 0n) {
          closed = false;
          break;
        }
        cost += model.costs[i];
      }
    }
    if (!closed || cost > budget) continue;
    const bm = BigInt(mask);
    const pairMask = pairMaskOf(bm, n);
    combos.push({
      mask: bm,
      changes: maskToIds(bm, model.ids),
      cost,
      pairMask,
      pairCount: popcount(pairMask),
    });
  }
  return combos;
}

export interface Reachability {
  reachableMask: bigint;
  reachableCount: number;
  uncovered: UncoveredPair[];
  unselectable: string[];
}

export function analyzeReachability(
  model: ChangeModel,
  budget: number,
): Reachability {
  const n = model.ids.length;
  let reachableMask = 0n;
  let reachableCount = 0;
  const uncovered: UncoveredPair[] = [];
  const unselectable: string[] = [];
  for (let i = 0; i < n; i++) {
    if (model.closureCost[i] > budget) unselectable.push(model.ids[i]);
  }
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      const unionMask = model.closureMask[i] | model.closureMask[j];
      const unionCost = maskCost(unionMask, model.costs);
      if (unionCost <= budget) {
        reachableMask |= pairBit(i, j, n);
        reachableCount++;
      } else {
        uncovered.push({
          a: model.ids[i],
          b: model.ids[j],
          reason: uncoverReason(model, budget, i, j, unionMask, unionCost),
        });
      }
    }
  }
  return { reachableMask, reachableCount, uncovered, unselectable };
}

function uncoverReason(
  model: ChangeModel,
  budget: number,
  i: number,
  j: number,
  unionMask: bigint,
  unionCost: number,
): string {
  const parts: string[] = [];
  if (model.closureCost[i] > budget) {
    parts.push(
      `改动 ${model.ids[i]} 自身依赖闭包成本 ${model.closureCost[i]} 已超预算 ${budget}`,
    );
  }
  if (model.closureCost[j] > budget) {
    parts.push(
      `改动 ${model.ids[j]} 自身依赖闭包成本 ${model.closureCost[j]} 已超预算 ${budget}`,
    );
  }
  if (parts.length > 0) return parts.join("；");
  const members = maskToIds(unionMask, model.ids).join(", ");
  return `联合依赖闭包 {${members}} 成本 ${unionCost} 超过执行预算 ${budget}`;
}
