import type { PathStep, Status } from './types.ts';

/**
 * 抽象状态：
 * - status:      变量 -> 可能状态集合（幂集格，join 为并集）
 * - borrows:     借用句柄 -> 可能指向的变量集合
 * - regionStack: 当前嵌套清理区域（内->外的逆序，数组末尾为最内层）
 * - pending:     region -> 已注册延迟关闭变量（注册顺序，关闭时反向 LIFO）
 */
export interface AbstractState {
  status: Map<string, Set<Status>>;
  borrows: Map<string, Set<string>>;
  regionStack: string[];
  pending: Map<string, string[]>;
}

export function initialState(): AbstractState {
  return {
    status: new Map(),
    borrows: new Map(),
    regionStack: [],
    pending: new Map(),
  };
}

export function cloneState(s: AbstractState): AbstractState {
  const status = new Map<string, Set<Status>>();
  for (const [k, v] of s.status) status.set(k, new Set(v));
  const borrows = new Map<string, Set<string>>();
  for (const [k, v] of s.borrows) borrows.set(k, new Set(v));
  return {
    status,
    borrows,
    regionStack: [...s.regionStack],
    pending: new Map([...s.pending].map(([k, v]) => [k, [...v]])),
  };
}

export function statesEqual(a: AbstractState, b: AbstractState): boolean {
  if (a.status.size !== b.status.size || a.borrows.size !== b.borrows.size) return false;
  for (const [k, va] of a.status) {
    const vb = b.status.get(k);
    if (!vb || va.size !== vb.size || [...va].some((x) => !vb.has(x))) return false;
  }
  for (const [k, va] of a.borrows) {
    const vb = b.borrows.get(k);
    if (!vb || va.size !== vb.size || [...va].some((x) => !vb.has(x))) return false;
  }
  if (a.regionStack.length !== b.regionStack.length || a.regionStack.some((r, i) => r !== b.regionStack[i])) {
    return false;
  }
  if (a.pending.size !== b.pending.size) return false;
  for (const [k, va] of a.pending) {
    const vb = b.pending.get(k);
    if (!vb || va.length !== vb.length || va.some((x, i) => x !== vb[i])) return false;
  }
  return true;
}

/**
 * 单调 join：所有成分按并集合并。
 * 结构化控制流保证汇合点 regionStack/pending 一致；不一致说明图非结构化，
 * 此时保留前者（该预处理条件由 GraphBuilder 构造的图满足）。
 */
export function joinStates(a: AbstractState, b: AbstractState): AbstractState {
  const out = cloneState(a);
  for (const [k, vb] of b.status) {
    const va = out.status.get(k);
    if (va) for (const s of vb) va.add(s);
    else out.status.set(k, new Set(vb));
  }
  for (const [h, varsB] of b.borrows) {
    const varsA = out.borrows.get(h);
    if (varsA) for (const v of varsB) varsA.add(v);
    else out.borrows.set(h, new Set(varsB));
  }
  return out;
}

export function cloneSteps(steps: PathStep[]): PathStep[] {
  return steps.map((s) => ({ ...s, cleanup: s.cleanup ? [...s.cleanup] : undefined }));
}
