import type { ColoredMultigraph } from "./graph.ts";
import { validateGraph, edgeCount, sortedCopy } from "./graph.ts";
import { canonicalForm } from "./canonical.ts";

export type IsoResult =
  | {
      status: "isomorphic";
      /** mapping[v] = 图 b 中与图 a 的顶点 v 对应的顶点。 */
      mapping: number[];
      certificate: string;
      nodesExplored: number;
    }
  | {
      status: "non-isomorphic";
      reason: string;
      certificateA?: string;
      certificateB?: string;
      nodesExplored: number;
    }
  | { status: "unknown"; reason: "budget-exhausted"; nodesExplored: number };

/**
 * 同构判定。顶点输入顺序不影响结论。
 * - isomorphic：附可独立核对的顶点映射；
 * - non-isomorphic：附双方规范编码（可重新计算核对）或显然的结构差异原因；
 * - unknown：搜索预算耗尽，无法下结论。
 */
export function checkIsomorphism(
  a: ColoredMultigraph,
  b: ColoredMultigraph,
  budget: number = 100000,
): IsoResult {
  validateGraph(a);
  validateGraph(b);
  if (a.vertexCount !== b.vertexCount) {
    return { status: "non-isomorphic", reason: "vertex-count-mismatch", nodesExplored: 0 };
  }
  // 颜色直方图不同 ⇒ 必不同构；但直方图相同绝不能当作同构证据。
  const ca = sortedCopy(a.colors);
  const cb = sortedCopy(b.colors);
  if (ca.some((c, i) => c !== cb[i])) {
    return { status: "non-isomorphic", reason: "color-histogram-mismatch", nodesExplored: 0 };
  }
  if (edgeCount(a) !== edgeCount(b)) {
    return { status: "non-isomorphic", reason: "edge-count-mismatch", nodesExplored: 0 };
  }
  const fa = canonicalForm(a, budget);
  if (fa === null) return { status: "unknown", reason: "budget-exhausted", nodesExplored: budget + 1 };
  const fb = canonicalForm(b, budget);
  if (fb === null) return { status: "unknown", reason: "budget-exhausted", nodesExplored: budget + 1 };
  const nodesExplored = fa.nodesExplored + fb.nodesExplored;
  if (fa.certificate !== fb.certificate) {
    return {
      status: "non-isomorphic",
      reason: "canonical-certificate-mismatch",
      certificateA: fa.certificate,
      certificateB: fb.certificate,
      nodesExplored,
    };
  }
  // 两份规范标号都把各自图映到同一规范邻接矩阵，复合即得同构映射。
  const posA = inversePermutation(fa.labeling);
  const mapping = posA.map((p) => fb.labeling[p]);
  return { status: "isomorphic", mapping, certificate: fa.certificate, nodesExplored };
}

/** 独立核对一个顶点映射是否确为 a→b 的同构（双射 + 保色 + 保边重数）。 */
export function verifyIsomorphism(
  a: ColoredMultigraph,
  b: ColoredMultigraph,
  mapping: number[],
): boolean {
  const n = a.vertexCount;
  if (b.vertexCount !== n || mapping.length !== n) return false;
  const seen = new Set(mapping);
  if (seen.size !== n) return false;
  for (const m of mapping) if (!Number.isInteger(m) || m < 0 || m >= n) return false;
  for (let i = 0; i < n; i++) {
    if (a.colors[i] !== b.colors[mapping[i]]) return false;
    for (let j = 0; j < n; j++) {
      if (a.adj[i][j] !== b.adj[mapping[i]][mapping[j]]) return false;
    }
  }
  return true;
}

/**
 * 核对 checkIsomorphism 的结论：
 * - isomorphic：重新验证映射与规范编码；
 * - non-isomorphic：重新计算规范编码，确认确实不同（或平凡原因成立）；
 * - unknown：确认同一预算下搜索确实耗尽。
 */
export function verifyIsoResult(
  a: ColoredMultigraph,
  b: ColoredMultigraph,
  result: IsoResult,
  budget: number = 100000,
): boolean {
  if (result.status === "isomorphic") {
    if (!verifyIsomorphism(a, b, result.mapping)) return false;
    const fa = canonicalForm(a, Math.max(budget, 1000000));
    return fa !== null && fa.certificate === result.certificate;
  }
  if (result.status === "non-isomorphic") {
    if (result.reason === "vertex-count-mismatch") return a.vertexCount !== b.vertexCount;
    if (result.reason === "color-histogram-mismatch") {
      const ca = sortedCopy(a.colors);
      const cb = sortedCopy(b.colors);
      return ca.length !== cb.length || ca.some((c, i) => c !== cb[i]);
    }
    if (result.reason === "edge-count-mismatch") return edgeCount(a) !== edgeCount(b);
    const big = Math.max(budget, 1000000);
    const fa = canonicalForm(a, big);
    const fb = canonicalForm(b, big);
    if (fa === null || fb === null) return false;
    return (
      fa.certificate !== fb.certificate &&
      fa.certificate === result.certificateA &&
      fb.certificate === result.certificateB
    );
  }
  // unknown：预算内必须确实算不完。
  return canonicalForm(a, budget) === null || canonicalForm(b, budget) === null;
}

function inversePermutation(perm: number[]): number[] {
  const inv = new Array<number>(perm.length);
  for (let i = 0; i < perm.length; i++) inv[perm[i]] = i;
  return inv;
}
