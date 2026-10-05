/**
 * 带固定位置约束的拓扑排序器。
 *
 * 三态结果：
 *  - "solved"     ：找到合法排列，且为字典序最小；
 *  - "infeasible" ：不可行，附带可独立验证的证据（见 evidence.ts）；
 *  - "unknown"    ：搜索预算耗尽，无法判定。
 *
 * 关键原则：普通拓扑排序成功不代表存在满足固定位置的排列，
 * 只有搜索真正构造出完整排列才返回 "solved"。
 */

import type { BoundProof, Edge, Evidence, IntervalMember } from "./evidence.ts";

export type SolveResult =
  | { status: "solved"; permutation: number[]; explored: number }
  | { status: "infeasible"; evidence: Evidence; explored: number }
  | { status: "unknown"; explored: number };

export interface SolveOptions {
  /** 搜索节点预算，默认 100_000；传 Infinity 表示不设限。 */
  budget?: number;
}

const DEFAULT_BUDGET = 100_000;

/** 沿 parent 指针重建 s -> v 的路径（用于证据）。 */
function reconstructPath(parents: (number | null)[], s: number, v: number): number[] {
  const path = [v];
  while (path[0] !== s) path.unshift(parents[path[0]]!);
  return path;
}

export function solveConstrainedTopo(
  n: number,
  edges: readonly Edge[],
  fixed: ReadonlyMap<number, number>,
  options: SolveOptions = {},
): SolveResult {
  const budget = options.budget ?? DEFAULT_BUDGET;
  let explored = 0;

  const infeasible = (evidence: Evidence): SolveResult => ({
    status: "infeasible",
    evidence,
    explored,
  });

  if (!Number.isInteger(n) || n < 0) {
    throw new RangeError("n must be a non-negative integer");
  }
  for (const [u, v] of edges) {
    if (!Number.isInteger(u) || !Number.isInteger(v) || u < 0 || u >= n || v < 0 || v >= n) {
      throw new RangeError(`edge endpoint out of range: [${u}, ${v}]`);
    }
  }
  for (const v of fixed.keys()) {
    if (!Number.isInteger(v) || v < 0 || v >= n) {
      throw new RangeError(`fixed node out of range: ${v}`);
    }
  }

  // 1) 固定位置越界 / 两个节点占用同一位置
  const fixedPos = new Array<number>(n).fill(-1);
  const fixedAt = new Array<number>(n).fill(-1);
  for (const [v, p] of fixed) {
    if (!Number.isInteger(p) || p < 0 || p >= n) {
      return infeasible({ kind: "fixed-position-out-of-range", node: v, position: p, n });
    }
    if (fixedAt[p] !== -1) {
      return infeasible({
        kind: "duplicate-fixed-position",
        position: p,
        nodes: [fixedAt[p], v],
      });
    }
    fixedAt[p] = v;
    fixedPos[v] = p;
  }

  // 2) 建图 + 环检测（自环视为长度 1 的环）
  const adj: number[][] = Array.from({ length: n }, () => []);
  const preds: number[][] = Array.from({ length: n }, () => []);
  const selfLoop = new Array<boolean>(n).fill(false);
  for (const [u, v] of edges) {
    adj[u].push(v);
    preds[v].push(u);
    if (u === v) selfLoop[u] = true;
  }
  for (let v = 0; v < n; v++) {
    if (selfLoop[v]) return infeasible({ kind: "cycle", nodes: [v] });
  }
  const topoOrder: number[] = [];
  {
    const color = new Array<number>(n).fill(0); // 0=未访问 1=在栈上 2=已完成
    const stack: number[] = [];
    let cycle: number[] | null = null;
    const visit = (u: number): void => {
      if (cycle) return;
      color[u] = 1;
      stack.push(u);
      for (const w of adj[u]) {
        if (cycle) return;
        if (color[w] === 0) {
          visit(w);
        } else if (color[w] === 1) {
          cycle = stack.slice(stack.indexOf(w));
          return;
        }
      }
      stack.pop();
      color[u] = 2;
      topoOrder.push(u);
    };
    for (let v = 0; v < n && !cycle; v++) {
      if (color[v] === 0) visit(v);
    }
    if (cycle) return infeasible({ kind: "cycle", nodes: cycle });
    topoOrder.reverse(); // 源点在前
  }

  // 3) 每个源点的最长路径长度 dist[s][v]（边数），不可达为 -1
  const dist: number[][] = Array.from({ length: n }, () => new Array<number>(n).fill(-1));
  const parent: (number | null)[][] = Array.from({ length: n }, () =>
    new Array<number | null>(n).fill(null),
  );
  for (let s = 0; s < n; s++) {
    dist[s][s] = 0;
    for (const u of topoOrder) {
      if (dist[s][u] === -1) continue;
      for (const w of adj[u]) {
        if (dist[s][w] < dist[s][u] + 1) {
          dist[s][w] = dist[s][u] + 1;
          parent[s][w] = u;
        }
      }
    }
  }

  // 4) 依赖链与固定位置矛盾：u⇝…⇝v（长度 k）且 f(u)+k > f(v)
  for (let u = 0; u < n; u++) {
    if (fixedPos[u] === -1) continue;
    for (let v = 0; v < n; v++) {
      if (u === v || fixedPos[v] === -1) continue;
      if (dist[u][v] >= 1 && fixedPos[u] + dist[u][v] > fixedPos[v]) {
        return infeasible({
          kind: "chain-contradiction",
          path: reconstructPath(parent[u], u, v),
          firstPos: fixedPos[u],
          lastPos: fixedPos[v],
        });
      }
    }
  }

  // 5) 由固定节点传播的最紧界（初始为位置范围本身 [0, n-1]）：
  //    lb(v) = max(0, max_w f(w)+dist(w,v))
  //    ub(v) = min(n-1, min_w f(w)-dist(v,w))
  const lb = new Array<number>(n).fill(0);
  const ub = new Array<number>(n).fill(n - 1);
  for (let v = 0; v < n; v++) {
    for (let w = 0; w < n; w++) {
      if (fixedPos[w] === -1) continue;
      if (dist[w][v] >= 0) lb[v] = Math.max(lb[v], fixedPos[w] + dist[w][v]);
      if (dist[v][w] >= 0) ub[v] = Math.min(ub[v], fixedPos[w] - dist[v][w]);
    }
  }

  // 6) 区间挤占（Hall 条件）：lb(v) >= s 且 ub(v) < t 的节点
  //    都必须落在区间 [s,t) 内，节点数超过 t-s 即不可行。
  //    前缀（s=0）、后缀（t=n）及空区间（s=t，即某节点无合法位置）都是特例。
  const buildMember = (v: number, s: number, t: number): IntervalMember => {
    let lower: BoundProof;
    if (fixedPos[v] !== -1) {
      lower = { type: "fixed", position: fixedPos[v] };
    } else if (s === 0) {
      lower = { type: "trivial" };
    } else {
      let witness = -1;
      for (let w = 0; w < n; w++) {
        if (fixedPos[w] !== -1 && dist[w][v] >= 1 && fixedPos[w] + dist[w][v] >= s) {
          witness = w;
          break;
        }
      }
      lower = witness === -1
        ? { type: "trivial" } // 不会发生（lb 由固定节点传播而来）
        : { type: "chain", path: reconstructPath(parent[witness], witness, v), fixedPos: fixedPos[witness] };
    }
    let upper: BoundProof;
    if (fixedPos[v] !== -1) {
      upper = { type: "fixed", position: fixedPos[v] };
    } else if (t === n) {
      upper = { type: "trivial" };
    } else {
      let witness = -1;
      for (let w = 0; w < n; w++) {
        if (fixedPos[w] !== -1 && dist[v][w] >= 1 && fixedPos[w] - dist[v][w] < t) {
          witness = w;
          break;
        }
      }
      upper = witness === -1
        ? { type: "trivial" } // 不会发生（ub 由固定节点传播而来）
        : { type: "chain", path: reconstructPath(parent[v], v, witness), fixedPos: fixedPos[witness] };
    }
    return { node: v, lower, upper };
  };
  for (let s = 0; s <= n; s++) {
    for (let t = s; t <= n; t++) {
      let count = 0;
      for (let v = 0; v < n; v++) {
        if (lb[v] >= s && ub[v] < t) count++;
      }
      if (count > t - s) {
        const members: IntervalMember[] = [];
        for (let v = 0; v < n; v++) {
          if (lb[v] >= s && ub[v] < t) members.push(buildMember(v, s, t));
        }
        return infeasible({ kind: "interval-squeeze", s, t, members });
      }
    }
  }

  // 8) 回溯搜索：从左到右填位置，候选按编号升序，
  //    首个完整解即字典序最小解。
  const indeg = preds.map((p) => p.length);
  const placed = new Array<boolean>(n).fill(false);
  const perm: number[] = [];
  let aborted = false;

  const dfs = (pos: number): boolean => {
    if (pos === n) return true;
    if (++explored > budget) {
      aborted = true;
      return false;
    }
    let candidates: number[];
    const forced = fixedAt[pos];
    if (forced !== -1) {
      // 该位置被固定占用，只能放 forced，且其前驱必须已全部就位
      if (placed[forced] || indeg[forced] !== 0) return false;
      candidates = [forced];
    } else {
      candidates = [];
      for (let v = 0; v < n; v++) {
        // 有固定位置的节点只能放在自己的位置上
        if (!placed[v] && fixedPos[v] === -1 && indeg[v] === 0) {
          candidates.push(v);
        }
      }
    }
    for (const v of candidates) {
      placed[v] = true;
      perm.push(v);
      for (const w of adj[v]) indeg[w]--;
      if (dfs(pos + 1)) return true;
      for (const w of adj[v]) indeg[w]++;
      perm.pop();
      placed[v] = false;
      if (aborted) return false;
    }
    return false;
  };

  if (dfs(0)) {
    return { status: "solved", permutation: perm.slice(), explored };
  }
  if (aborted) {
    // 预算耗尽：诚实报告未知
    return { status: "unknown", explored };
  }
  // 预算内穷尽整个搜索空间仍无解：确定性不可行。
  // 结构检查未命中，给出兜底证据（可由验证器确定性重跑确认）。
  return infeasible({ kind: "exhaustive-search" });
}
