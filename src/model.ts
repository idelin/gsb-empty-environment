import type { ChangeSpec, Instance, Model } from "./types";

export const MAX_CHANGES = 18;

/** 校验输入并归一化为位掩码模型。 */
export function buildModel(instance: Instance): Model {
  const { budget, changes } = instance;
  if (!Number.isFinite(budget) || budget < 0) {
    throw new Error(`执行预算必须是非负有限数，收到: ${budget}`);
  }
  if (!Array.isArray(changes) || changes.length === 0) {
    throw new Error("至少需要一个候选改动");
  }
  if (changes.length > MAX_CHANGES) {
    throw new Error(`改动数量 ${changes.length} 超过上限 ${MAX_CHANGES}`);
  }

  const n = changes.length;
  const ids: string[] = [];
  const index = new Map<string, number>();
  for (const c of changes) {
    validateChange(c);
    if (index.has(c.id)) throw new Error(`改动 id 重复: ${c.id}`);
    index.set(c.id, ids.length);
    ids.push(c.id);
  }

  const costs = changes.map((c) => c.cost);
  const requireMask = new Array<number>(n).fill(0);
  const conflictMask = new Array<number>(n).fill(0);

  const resolve = (owner: string, ref: string, field: string): number => {
    const j = index.get(ref);
    if (j === undefined) throw new Error(`改动 ${owner} 的 ${field} 引用了未知改动: ${ref}`);
    return j;
  };

  changes.forEach((c, i) => {
    for (const r of c.requires ?? []) {
      const j = resolve(c.id, r, "requires");
      if (j === i) throw new Error(`改动 ${c.id} 不能依赖自身`);
      requireMask[i] |= 1 << j;
    }
    for (const r of c.conflicts ?? []) {
      const j = resolve(c.id, r, "conflicts");
      if (j === i) throw new Error(`改动 ${c.id} 不能与自身冲突`);
      conflictMask[i] |= 1 << j;
      conflictMask[j] |= 1 << i; // 冲突对称化
    }
  });

  // 依赖闭包（不动点迭代，容忍依赖环：环上改动同进同出）。
  const closureMask = new Array<number>(n);
  for (let i = 0; i < n; i++) closureMask[i] = (1 << i) | requireMask[i];
  let changed = true;
  while (changed) {
    changed = false;
    for (let i = 0; i < n; i++) {
      let m = closureMask[i];
      let acc = m;
      for (let b = 0; b < n; b++) {
        if (m & (1 << b)) acc |= closureMask[b];
      }
      if (acc !== m) {
        closureMask[i] = acc;
        changed = true;
      }
    }
  }

  const closureCost = closureMask.map((m) => maskCost(m, costs));
  return { n, ids, costs, closureMask, closureCost, conflictMask };
}

function validateChange(c: ChangeSpec): void {
  if (!c || typeof c.id !== "string" || c.id.length === 0) {
    throw new Error(`改动缺少合法 id: ${JSON.stringify(c)}`);
  }
  if (!Number.isFinite(c.cost) || c.cost < 0) {
    throw new Error(`改动 ${c.id} 的成本必须是非负有限数，收到: ${c.cost}`);
  }
}

export function maskCost(mask: number, costs: number[]): number {
  let sum = 0;
  let m = mask;
  while (m !== 0) {
    const lsb = m & -m;
    const i = 31 - Math.clz32(lsb);
    sum += costs[i];
    m ^= lsb;
  }
  return sum;
}

export function popcount(mask: number): number {
  let m = mask;
  let c = 0;
  while (m !== 0) {
    m &= m - 1;
    c++;
  }
  return c;
}

/** 掩码内是否存在冲突对；存在则返回一对见证下标，否则返回 null。 */
export function conflictWitness(model: Model, mask: number): [number, number] | null {
  for (let i = 0; i < model.n; i++) {
    if (mask & (1 << i)) {
      const hit = model.conflictMask[i] & mask;
      if (hit !== 0) {
        const j = 31 - Math.clz32(hit & -hit);
        return [i, j];
      }
    }
  }
  return null;
}

/** 掩码是否为合法方案：依赖闭包封闭、无冲突、成本不超预算。 */
export function isLegal(model: Model, mask: number, cost: number, budget: number): boolean {
  if (cost > budget) return false;
  for (let i = 0; i < model.n; i++) {
    if (mask & (1 << i)) {
      if (model.closureMask[i] & ~mask) return false;
      if (model.conflictMask[i] & mask) return false;
    }
  }
  return true;
}
