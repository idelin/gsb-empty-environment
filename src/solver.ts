// 核心求解器：带剪枝的字典序搜索。
//
// 字典序定义：集合表示为按输入节点顺序排列的有序列表，按字典序比较，
// 前缀较短者更小（如 [] < [a] < [a,b] < [a,c] < [b]）。
// 集合对 (A, B) 先比 A 再比 B。
//
// 搜索不是"枚举全部子集再筛选"：枚举器按字典序逐个生成候选，
// 沿途用三值求值（triEval）对单调公式做可靠剪枝——
// 一旦部分赋值使公式必假，整棵子树被剪掉，永不展开。

import { triEval, evalExact, type Spec } from "./spec.ts";

export interface SearchStats {
  candidatesVisited: number; // 完整检查过的候选集合数
  nodesExpanded: number; // 搜索树展开的节点数
  prunes: number; // 剪枝次数
  maxDepth: number; // 最大搜索深度
  elapsedMs: number;
}

export function createStats(): SearchStats {
  return { candidatesVisited: 0, nodesExpanded: 0, prunes: 0, maxDepth: 0, elapsedMs: 0 };
}

// 在 allowed 限制下，按字典序找第一个满足 spec 的集合；不存在返回 null。
export function findMinSatisfying(
  spec: Spec,
  n: number,
  allowed: boolean[] | null,
  stats: SearchStats,
): number[] | null {
  const assign = new Uint8Array(n);
  if (allowed) {
    for (let i = 0; i < n; i++) if (!allowed[i]) assign[i] = 2;
  }
  let found: number[] | null = null;

  function currentIn(): number[] {
    const out: number[] = [];
    for (let i = 0; i < n; i++) if (assign[i] === 1) out.push(i);
    return out;
  }

  // 枚举所有"下标 < idx 的选择已固定"的集合，字典序：
  // 先访问当前集合（其余全不选），再依次把 j = idx..n-1 作为下一个入选元素递归。
  function walk(idx: number, depth: number): boolean {
    stats.nodesExpanded++;
    if (depth > stats.maxDepth) stats.maxDepth = depth;
    if (triEval(spec, assign) === -1) {
      stats.prunes++;
      return false;
    }
    if (evalExact(spec, assign)) {
      stats.candidatesVisited++;
      found = currentIn();
      return true;
    }
    for (let j = idx; j < n; j++) {
      if (assign[j] === 2) continue; // 被禁用或已确定为在外
      const changed: number[] = [];
      for (let t = idx; t < j; t++) {
        if (assign[t] === 0) {
          assign[t] = 2; // 选择 j 意味着 idx..j-1 都在外
          changed.push(t);
        }
      }
      assign[j] = 1;
      changed.push(j);
      if (triEval(spec, assign) !== -1) {
        if (walk(j + 1, depth + 1)) return true;
      } else {
        stats.prunes++;
      }
      for (const t of changed) assign[t] = 0;
    }
    return false;
  }

  walk(0, 1);
  return found;
}

export interface DisjointPair {
  a: number[];
  b: number[];
}

// 找字典序最小的不相交对 (A, B)：A 满足 specA，B 满足 specB，A ∩ B = ∅。
// 不存在（即两族集合必然相交）时返回 null。
export function findDisjointPair(
  specA: Spec,
  specB: Spec,
  n: number,
  stats: SearchStats,
): DisjointPair | null {
  const assignA = new Uint8Array(n);
  const viewB = new Uint8Array(n); // B 的视角：A 已占用的节点不可用
  let witness: DisjointPair | null = null;

  // B 是否还有可能满足（A 只会继续变大，可用节点只会变少，故这是可靠剪枝）
  function bFeasible(): boolean {
    for (let i = 0; i < n; i++) viewB[i] = assignA[i] === 1 ? 2 : 0;
    return triEval(specB, viewB) !== -1;
  }

  function currentInA(): number[] {
    const out: number[] = [];
    for (let i = 0; i < n; i++) if (assignA[i] === 1) out.push(i);
    return out;
  }

  function walkA(idx: number, depth: number): boolean {
    stats.nodesExpanded++;
    if (depth > stats.maxDepth) stats.maxDepth = depth;
    if (triEval(specA, assignA) === -1) {
      stats.prunes++;
      return false;
    }
    if (!bFeasible()) {
      stats.prunes++;
      return false;
    }
    // 访问 A = 当前已选集合：若满足 specA，则为其找字典序最小的 B
    if (evalExact(specA, assignA)) {
      stats.candidatesVisited++;
      const allowed: boolean[] = new Array(n);
      for (let i = 0; i < n; i++) allowed[i] = assignA[i] !== 1;
      const b = findMinSatisfying(specB, n, allowed, stats);
      if (b !== null) {
        witness = { a: currentInA(), b };
        return true;
      }
    }
    for (let j = idx; j < n; j++) {
      if (assignA[j] === 2) continue;
      const changed: number[] = [];
      for (let t = idx; t < j; t++) {
        if (assignA[t] === 0) {
          assignA[t] = 2;
          changed.push(t);
        }
      }
      assignA[j] = 1;
      changed.push(j);
      if (triEval(specA, assignA) !== -1 && bFeasible()) {
        if (walkA(j + 1, depth + 1)) return true;
      } else {
        stats.prunes++;
      }
      for (const t of changed) assignA[t] = 0;
    }
    return false;
  }

  walkA(0, 1);
  return witness;
}

// 满足 spec 的最小集合大小；不可满足时返回 n + 1。分支限界，非全枚举。
export function minQuorumSize(spec: Spec, n: number, stats: SearchStats): number {
  const assign = new Uint8Array(n);
  let best = n + 1;

  function dfs(idx: number, inCount: number, depth: number): void {
    stats.nodesExpanded++;
    if (depth > stats.maxDepth) stats.maxDepth = depth;
    if (inCount >= best) {
      stats.prunes++;
      return;
    }
    const t = triEval(spec, assign);
    if (t === -1) {
      stats.prunes++;
      return;
    }
    if (t === 1) {
      best = inCount; // 未定节点全部不选即达最小
      stats.candidatesVisited++;
      return;
    }
    if (idx >= n) return;
    assign[idx] = 1;
    dfs(idx + 1, inCount + 1, depth + 1);
    assign[idx] = 2;
    dfs(idx + 1, inCount, depth + 1);
    assign[idx] = 0;
  }

  dfs(0, 0, 1);
  return best;
}

export interface Proof {
  method: "size-argument" | "exhaustive-search" | "vacuous";
  detail: string;
  stats: SearchStats;
}

export interface CheckOutcome {
  holds: boolean;
  witness: DisjointPair | null;
  proof: Proof | null;
}

// 判定 "任意满足 specA 的集合与任意满足 specB 的集合必相交"。
// 先尝试大小论证（min|A| + min|B| > n，鸽巢原理），
// 不足以判定时退到带剪枝的完备搜索：找到字典序最小不相交对，
// 或穷尽搜索空间给出证明统计。
export function checkIntersection(
  specA: Spec,
  specB: Spec,
  n: number,
  labelA: string,
  labelB: string,
): CheckOutcome {
  const sizeStats = createStats();
  const t0 = performance.now();
  const minA = minQuorumSize(specA, n, sizeStats);
  const minB = minQuorumSize(specB, n, sizeStats);
  sizeStats.elapsedMs = performance.now() - t0;

  if (minA > n || minB > n) {
    const empty = minA > n ? labelA : labelB;
    return {
      holds: true,
      witness: null,
      proof: {
        method: "vacuous",
        detail: `${empty}集合族为空（规格不可满足），命题空虚成立`,
        stats: sizeStats,
      },
    };
  }
  if (minA + minB > n) {
    return {
      holds: true,
      witness: null,
      proof: {
        method: "size-argument",
        detail:
          `最小${labelA}集合大小=${minA}，最小${labelB}集合大小=${minB}，` +
          `${minA}+${minB}=${minA + minB} > 节点数${n}，` +
          `由鸽巢原理任意${labelA}集合与任意${labelB}集合必相交`,
        stats: sizeStats,
      },
    };
  }

  const stats = createStats();
  const t1 = performance.now();
  const witness = findDisjointPair(specA, specB, n, stats);
  stats.elapsedMs = performance.now() - t1;

  if (witness !== null) {
    return { holds: false, witness, proof: null };
  }
  return {
    holds: true,
    witness: null,
    proof: {
      method: "exhaustive-search",
      detail:
        `大小论证不足以判定（最小${labelA}集合=${minA}，最小${labelB}集合=${minB}，和为 ${minA + minB} <= ${n}），` +
        `改用带剪枝的完备搜索：已穷尽所有候选，不存在不相交对，故任意${labelA}集合与任意${labelB}集合必相交`,
      stats,
    },
  };
}
