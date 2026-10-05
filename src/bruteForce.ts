/**
 * 排列枚举预言机：按字典序枚举排列，用于核对求解器。
 * 仅适用于小规模（n <= 9，即至多 362880 个排列）。
 */

import type { Edge } from "./evidence.ts";

/** 检查排列是否同时满足先后关系与固定位置约束。 */
export function isValidPermutation(
  n: number,
  edges: readonly Edge[],
  fixed: ReadonlyMap<number, number>,
  perm: readonly number[],
): boolean {
  if (perm.length !== n) return false;
  const posOf = new Array<number>(n).fill(-1);
  for (let i = 0; i < n; i++) {
    const v = perm[i];
    if (!Number.isInteger(v) || v < 0 || v >= n || posOf[v] !== -1) return false;
    posOf[v] = i;
  }
  for (const [u, v] of edges) {
    if (posOf[u] >= posOf[v]) return false;
  }
  for (const [v, p] of fixed) {
    if (posOf[v] !== p) return false;
  }
  return true;
}

/**
 * 按字典序枚举排列，返回第一个（即字典序最小的）合法排列；
 * 不存在则返回 null。带剪枝：只扩展满足前驱计数与固定位置的部分排列。
 */
export function lexMinValidPermutation(
  n: number,
  edges: readonly Edge[],
  fixed: ReadonlyMap<number, number>,
): number[] | null {
  if (n > 9) {
    throw new RangeError("brute force oracle supports n <= 9 only");
  }
  const succ: number[][] = Array.from({ length: n }, () => []);
  const indeg = new Array<number>(n).fill(0);
  for (const [u, v] of edges) {
    succ[u].push(v);
    indeg[v]++;
  }
  const fixedPos = new Array<number>(n).fill(-1);
  const fixedAt = new Array<number>(n).fill(-1);
  for (const [v, p] of fixed) {
    if (!Number.isInteger(p) || p < 0 || p >= n) return null;
    if (fixedAt[p] !== -1) return null;
    fixedPos[v] = p;
    fixedAt[p] = v;
  }
  const used = new Array<boolean>(n).fill(false);
  const remaining = indeg.slice();
  const perm: number[] = [];

  const dfs = (pos: number): number[] | null => {
    if (pos === n) return perm.slice();
    const forced = fixedAt[pos];
    for (let v = 0; v < n; v++) {
      if (used[v]) continue;
      if (forced !== -1 && v !== forced) continue;
      if (fixedPos[v] !== -1 && fixedPos[v] !== pos) continue;
      if (remaining[v] !== 0) continue;
      used[v] = true;
      perm.push(v);
      for (const w of succ[v]) remaining[w]--;
      const result = dfs(pos + 1);
      for (const w of succ[v]) remaining[w]++;
      perm.pop();
      used[v] = false;
      if (result !== null) return result;
      if (forced !== -1) return null; // 被强制的位置没有别的候选
    }
    return null;
  };
  return dfs(0);
}

/** 是否存在合法排列（n <= 9）。 */
export function existsValidPermutation(
  n: number,
  edges: readonly Edge[],
  fixed: ReadonlyMap<number, number>,
): boolean {
  return lexMinValidPermutation(n, edges, fixed) !== null;
}

/**
 * 独立的穷尽式判定（独立于求解器主搜索实现），用于验证
 * exhaustive-search 兜底证据。指数复杂度，仅适合小规模实例。
 */
export function exhaustiveSearchCheck(
  n: number,
  edges: readonly Edge[],
  fixed: ReadonlyMap<number, number>,
): boolean {
  const succ: number[][] = Array.from({ length: n }, () => []);
  const indeg = new Array<number>(n).fill(0);
  for (const [u, v] of edges) {
    succ[u].push(v);
    indeg[v]++;
  }
  const fixedPos = new Array<number>(n).fill(-1);
  const fixedAt = new Array<number>(n).fill(-1);
  for (const [v, p] of fixed) {
    if (!Number.isInteger(p) || p < 0 || p >= n) return false;
    if (fixedAt[p] !== -1) return false;
    fixedPos[v] = p;
    fixedAt[p] = v;
  }
  const used = new Array<boolean>(n).fill(false);
  const remaining = indeg.slice();
  const dfs = (pos: number): boolean => {
    if (pos === n) return true;
    const forced = fixedAt[pos];
    for (let v = 0; v < n; v++) {
      if (used[v]) continue;
      if (forced !== -1 && v !== forced) continue;
      if (fixedPos[v] !== -1 && fixedPos[v] !== pos) continue;
      if (remaining[v] !== 0) continue;
      used[v] = true;
      for (const w of succ[v]) remaining[w]--;
      const ok = dfs(pos + 1);
      for (const w of succ[v]) remaining[w]++;
      used[v] = false;
      if (ok) return true;
      if (forced !== -1) return false;
    }
    return false;
  };
  return dfs(0);
}
