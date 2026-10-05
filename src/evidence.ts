/**
 * 不可行证据（certificate）类型与独立验证器。
 *
 * 每种证据都是"不可行"的充分条件，验证器不做搜索、只逐条核对：
 *  - fixed-position-out-of-range：固定位置越界；
 *  - duplicate-fixed-position   ：两个节点占用同一位置；
 *  - cycle                      ：先后关系成环（含自环）；
 *  - chain-contradiction        ：依赖链 u⇝…⇝v 长度 k，但 f(u)+k > f(v)；
 *  - interval-squeeze           ：超过 t-s 个节点都必须落在区间 [s,t) 内；
 *  - exhaustive-search          ：兜底——确定性重跑判定过程确认无解。
 *
 * 位置一律从 0 计。
 */

import { exhaustiveSearchCheck } from "./bruteForce.ts";

export type Edge = readonly [number, number];

/** 位置界证明：成员节点被挤在区间内的下界/上界依据。 */
export type BoundProof =
  | { type: "fixed"; position: number } // 节点本身固定在 position
  | { type: "chain"; path: number[]; fixedPos: number } // 经依赖链到某个固定节点
  | { type: "trivial" }; // 界来自位置范围本身（s=0 或 t=n）

export type IntervalMember = {
  node: number;
  /** 下界证明：pos(node) >= s */
  lower: BoundProof;
  /** 上界证明：pos(node) <= t-1 */
  upper: BoundProof;
};

export type Evidence =
  | { kind: "fixed-position-out-of-range"; node: number; position: number; n: number }
  | { kind: "duplicate-fixed-position"; position: number; nodes: [number, number] }
  | { kind: "cycle"; nodes: number[] }
  | { kind: "chain-contradiction"; path: number[]; firstPos: number; lastPos: number }
  | { kind: "interval-squeeze"; s: number; t: number; members: IntervalMember[] }
  | { kind: "exhaustive-search" };

function hasEdge(edgeSet: Set<string>, u: number, v: number): boolean {
  return edgeSet.has(u + "," + v);
}

/** path 至少两个节点且每对相邻节点间都有边；返回路径长度（边数），非法返回 -1。 */
function pathLength(edgeSet: Set<string>, path: number[]): number {
  if (path.length < 2) return -1;
  for (let i = 0; i + 1 < path.length; i++) {
    if (!hasEdge(edgeSet, path[i], path[i + 1])) return -1;
  }
  return path.length - 1;
}

/** 验证下界证明：pos(node) >= s。 */
function checkLowerBound(
  edgeSet: Set<string>,
  fixed: ReadonlyMap<number, number>,
  node: number,
  proof: BoundProof,
  s: number,
): boolean {
  switch (proof.type) {
    case "trivial":
      return s <= 0; // 任何位置都 >= 0
    case "fixed":
      return fixed.get(node) === proof.position && proof.position >= s;
    case "chain": {
      // 链 w ⇝…⇝ node（长度 k）且 w 固定在 f，则 pos(node) >= f+k
      const path = proof.path;
      const k = pathLength(edgeSet, path);
      return (
        k >= 1 &&
        path[path.length - 1] === node &&
        fixed.get(path[0]) === proof.fixedPos &&
        proof.fixedPos + k >= s
      );
    }
  }
}

/** 验证上界证明：pos(node) <= t-1。 */
function checkUpperBound(
  edgeSet: Set<string>,
  fixed: ReadonlyMap<number, number>,
  node: number,
  proof: BoundProof,
  t: number,
  n: number,
): boolean {
  switch (proof.type) {
    case "trivial":
      return t >= n; // 任何位置都 <= n-1，仅当 t=n 时上界平凡成立
    case "fixed":
      return fixed.get(node) === proof.position && proof.position < t;
    case "chain": {
      // 链 node ⇝…⇝ w（长度 k）且 w 固定在 f，则 pos(node) <= f-k
      const path = proof.path;
      const k = pathLength(edgeSet, path);
      return (
        k >= 1 &&
        path[0] === node &&
        fixed.get(path[path.length - 1]) === proof.fixedPos &&
        proof.fixedPos - k < t
      );
    }
  }
}

/**
 * 独立核对证据：返回 true 表示证据成立，即该实例确实不可行。
 */
export function verifyEvidence(
  n: number,
  edges: readonly Edge[],
  fixed: ReadonlyMap<number, number>,
  evidence: Evidence,
): boolean {
  const edgeSet = new Set<string>(edges.map(([u, v]) => u + "," + v));
  switch (evidence.kind) {
    case "fixed-position-out-of-range": {
      const { node, position } = evidence;
      return (
        fixed.get(node) === position &&
        (!Number.isInteger(position) || position < 0 || position >= n)
      );
    }
    case "duplicate-fixed-position": {
      const [a, b] = evidence.nodes;
      return (
        a !== b &&
        fixed.get(a) === evidence.position &&
        fixed.get(b) === evidence.position
      );
    }
    case "cycle": {
      const nodes = evidence.nodes;
      if (nodes.length < 1) return false;
      if (new Set(nodes).size !== nodes.length) return false;
      for (let i = 0; i < nodes.length; i++) {
        if (!hasEdge(edgeSet, nodes[i], nodes[(i + 1) % nodes.length])) return false;
      }
      return true;
    }
    case "chain-contradiction": {
      const { path, firstPos, lastPos } = evidence;
      if (path.length < 2) return false;
      if (fixed.get(path[0]) !== firstPos || fixed.get(path[path.length - 1]) !== lastPos) {
        return false;
      }
      const k = pathLength(edgeSet, path);
      // 链长 k 意味着 pos(v) >= pos(u)+k，与固定位置矛盾
      return k >= 1 && firstPos + k > lastPos;
    }
    case "interval-squeeze": {
      const { s, t, members } = evidence;
      // 允许 s == t（空区间，此时任何成员都使区间放不下）
      if (!Number.isInteger(s) || !Number.isInteger(t) || s < 0 || s > t || t > n) {
        return false;
      }
      const seen = new Set<number>();
      for (const member of evidence.members) {
        if (seen.has(member.node)) return false;
        seen.add(member.node);
        if (!checkLowerBound(edgeSet, fixed, member.node, member.lower, s)) return false;
        if (!checkUpperBound(edgeSet, fixed, member.node, member.upper, t, n)) return false;
      }
      // 区间 [s,t) 只有 t-s 个空位，成员数超过即不可行
      return members.length > t - s;
    }
    case "exhaustive-search": {
      // 兜底证书：用独立的穷尽式判定重跑，确认不存在任何合法排列
      return !exhaustiveSearchCheck(n, edges, fixed);
    }
  }
}
