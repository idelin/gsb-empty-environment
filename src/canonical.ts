import type { ColoredMultigraph } from "./graph.ts";
import { refineColors } from "./refine.ts";

export interface CanonicalResult {
  /** 规范编码：任意重标号后完全相同（同构不变量，且同构当且仅当编码相同）。 */
  certificate: string;
  /** labeling[i] = 被放到规范位置 i 的原图顶点。 */
  labeling: number[];
  /** 搜索树中实际执行的个体化次数。 */
  nodesExplored: number;
}

/**
 * 个体化-细化（individualization-refinement）规范形。
 *
 * 流程：颜色细化 → 若染色离散则输出证书 → 否则选取颜色 id 最小的
 * 非平凡单元作为目标单元（颜色 id 本身是规范排名，因此该选择同构不变），
 * 对单元中每个顶点个体化后递归，取字典序最大的证书。
 *
 * budget 为允许的最大个体化次数；超出则返回 null（调用方应报“未知”）。
 */
export function canonicalForm(g: ColoredMultigraph, budget: number = 100000): CanonicalResult | null {
  const n = g.vertexCount;
  let nodes = 0;
  let exhausted = false;
  let bestCert: number[] | null = null;
  let bestOrder: number[] | null = null;

  function certificateFor(order: number[]): number[] {
    const cert: number[] = [n];
    for (const v of order) cert.push(g.colors[v]);
    for (let i = 0; i < n; i++) {
      for (let j = i; j < n; j++) cert.push(g.adj[order[i]][order[j]]);
    }
    return cert;
  }

  function search(colors: number[]): void {
    if (exhausted) return;
    const refined = refineColors(g, colors);
    const cells = new Map<number, number[]>();
    for (let v = 0; v < n; v++) {
      const c = refined[v];
      const cell = cells.get(c);
      if (cell) cell.push(v);
      else cells.set(c, [v]);
    }
    if (cells.size === n) {
      const order = Array.from({ length: n }, (_, v) => v).sort((a, b) => refined[a] - refined[b]);
      const cert = certificateFor(order);
      if (bestCert === null || compareLex(cert, bestCert) > 0) {
        bestCert = cert;
        bestOrder = order;
      }
      return;
    }
    const keys = Array.from(cells.keys()).sort((a, b) => a - b);
    let target: number[] | null = null;
    for (const k of keys) {
      const cell = cells.get(k) as number[];
      if (cell.length > 1) {
        target = cell;
        break;
      }
    }
    const fresh = keys[keys.length - 1] + 1;
    for (const v of target as number[]) {
      nodes++;
      if (nodes > budget) {
        exhausted = true;
        return;
      }
      const next = refined.slice();
      next[v] = fresh;
      search(next);
      if (exhausted) return;
    }
  }

  search(g.colors.slice());
  if (exhausted || bestCert === null || bestOrder === null) return null;
  return { certificate: bestCert.join(","), labeling: bestOrder, nodesExplored: nodes };
}

function compareLex(a: number[], b: number[]): number {
  const len = Math.min(a.length, b.length);
  for (let i = 0; i < len; i++) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return a.length - b.length;
}
