/**
 * 最优自适应询问树求解器（带成本的离散属性，极小化最坏总成本）。
 *
 * 问题模型：
 *   - 至多 18 个候选对象，每个对象有一组离散属性值。
 *   - 询问某个属性需要支付该属性的固定成本，回答把当前候选集按取值分裂成若干分支。
 *   - 候选之间允许全部属性相同：这样的对象不可区分，永远落在同一分支，
 *     最终作为等价组（equivalence group）保留在叶子中。
 *   - 目标：构造自适应询问树，使“根到叶子的询问成本之和”的最大值（最坏总成本）最小。
 *
 * 方法：
 *   对候选子集做精确极小化极大动态规划（位掩码记忆化），
 *   solve(S) = min over 属性 a of ( cost(a) + max over 分支 B of solve(B) )。
 *   这枚举了所有自适应问序，因此是精确最优，而不是固定问序或贪心启发式。
 */

export type AttrValue = string | number | boolean;
export type AttrMap = Record<string, AttrValue>;

export interface Candidate {
  id: string;
  attrs: AttrMap;
}

export interface Problem {
  candidates: Candidate[];
  costs: Record<string, number>;
}

export interface Leaf {
  kind: "leaf";
  /** 叶子中的候选（大小 > 1 时即不可区分的等价组）。 */
  candidates: string[];
}

export interface Branch {
  /** 该分支对应的属性取值（规范化后的字符串）。 */
  value: string;
  /** 进入该分支的候选集。 */
  candidates: string[];
  child: Tree;
}

export interface Node {
  kind: "node";
  /** 到达本节点时剩余的候选集。 */
  candidates: string[];
  attribute: string;
  askCost: number;
  branches: Branch[];
}

export type Tree = Leaf | Node;

export interface Solution {
  worstCaseCost: number;
  tree: Tree;
  statesExplored: number;
}

export const MAX_CANDIDATES = 18;

/** 属性取值的规范化键：带上类型前缀，避免 "1" 与 1 之类的碰撞。 */
function valueKey(v: AttrValue): string {
  return typeof v + ":" + String(v);
}

interface MemoEntry {
  cost: number;
  /** 最优询问属性；叶子为 null。 */
  attribute: string | null;
}

export function solveProblem(problem: Problem): Solution {
  const { candidates, costs } = problem;
  const n = candidates.length;
  if (n === 0) throw new Error("至少需要一个候选对象。");
  if (n > MAX_CANDIDATES) {
    throw new Error(`候选数 ${n} 超过上限 ${MAX_CANDIDATES}。`);
  }
  const attributes = Object.keys(costs);
  if (attributes.length === 0) throw new Error("至少需要一个可询问属性。");
  for (const a of attributes) {
    if (!(costs[a] > 0)) throw new Error(`属性 ${a} 的成本必须为正数。`);
  }
  for (const c of candidates) {
    for (const a of attributes) {
      if (!(a in c.attrs)) {
        throw new Error(`候选 ${c.id} 缺少属性 ${a}。`);
      }
    }
  }

  // 每个候选的完整属性签名：签名相同 <=> 不可区分。
  const signatures: string[] = candidates.map((c) =>
    JSON.stringify(attributes.map((a) => valueKey(c.attrs[a]))),
  );

  // 每个候选在每个属性下的取值键，用于快速分裂；display 为原始取值的展示形式。
  const keys: string[][] = candidates.map((c) =>
    attributes.map((a) => valueKey(c.attrs[a])),
  );
  const display: string[][] = candidates.map((c) =>
    attributes.map((a) => String(c.attrs[a])),
  );
  const attrIndex = new Map<string, number>();
  attributes.forEach((a, i) => attrIndex.set(a, i));

  // 属性按成本升序排列，配合下界剪枝：cost(a) >= 当前最优即可停止。
  const orderedAttrs = [...attributes].sort((a, b) =>
    costs[a] - costs[b] || (a < b ? -1 : a > b ? 1 : 0),
  );

  const memo = new Map<number, MemoEntry>();

  function solveCost(mask: number): number {
    const hit = memo.get(mask);
    if (hit) return hit.cost;

    const indices: number[] = [];
    for (let i = 0; i < n; i++) if (mask & (1 << i)) indices.push(i);

    let entry: MemoEntry;
    const first = indices[0];
    const allSame = indices.every((i) => signatures[i] === signatures[first]);
    if (indices.length === 1 || allSame) {
      // 单例或等价组：无需再问，叶子成本 0。
      entry = { cost: 0, attribute: null };
    } else {
      let best = Infinity;
      let bestAttr: string | null = null;
      for (const a of orderedAttrs) {
        const ca = costs[a];
        if (ca >= best) break; // 下界剪枝：分支子问题成本非负
        const ai = attrIndex.get(a)!;
        const groups = new Map<string, number>();
        for (const i of indices) {
          const k = keys[i][ai];
          const g = groups.get(k);
          groups.set(k, (g ?? 0) | (1 << i));
        }
        if (groups.size < 2) continue; // 该属性在当前集合上不产生分裂
        let worst = 0;
        let abort = false;
        for (const sub of groups.values()) {
          const c = solveCost(sub);
          if (c > worst) worst = c;
          if (ca + worst >= best) { abort = true; break; } // 提前放弃该属性
        }
        if (!abort && ca + worst < best) {
          best = ca + worst;
          bestAttr = a;
        }
      }
      // 必然存在至少一个可分裂属性（否则 allSame 已成立）。
      entry = { cost: best, attribute: bestAttr };
    }
    memo.set(mask, entry);
    return entry.cost;
  }

  const fullMask = (1 << n) - 1;
  const worstCaseCost = solveCost(fullMask);

  function idsOf(mask: number): string[] {
    const ids: string[] = [];
    for (let i = 0; i < n; i++) if (mask & (1 << i)) ids.push(candidates[i].id);
    return ids;
  }

  function buildTree(mask: number): Tree {
    const entry = memo.get(mask)!;
    if (entry.attribute === null) {
      return { kind: "leaf", candidates: idsOf(mask) };
    }
    const ai = attrIndex.get(entry.attribute)!;
    const groups = new Map<string, { display: string; mask: number }>();
    for (let i = 0; i < n; i++) {
      if (!(mask & (1 << i))) continue;
      const k = keys[i][ai];
      const g = groups.get(k);
      if (g) g.mask |= 1 << i;
      else groups.set(k, { display: display[i][ai], mask: 1 << i });
    }
    const branches: Branch[] = [...groups.values()]
      .sort((x, y) => (x.display < y.display ? -1 : x.display > y.display ? 1 : 0))
      .map((g) => ({
        value: g.display,
        candidates: idsOf(g.mask),
        child: buildTree(g.mask),
      }));
    return {
      kind: "node",
      candidates: idsOf(mask),
      attribute: entry.attribute,
      askCost: costs[entry.attribute],
      branches,
    };
  }

  return { worstCaseCost, tree: buildTree(fullMask), statesExplored: memo.size };
}

/** 沿树模拟：返回每条根到叶子路径的询问成本之和，以及各叶子的候选集。 */
export function simulate(tree: Tree): { pathCost: number; leaf: string[] }[] {
  const out: { pathCost: number; leaf: string[] }[] = [];
  function walk(t: Tree, acc: number): void {
    if (t.kind === "leaf") {
      out.push({ pathCost: acc, leaf: t.candidates });
      return;
    }
    for (const b of t.branches) walk(b.child, acc + t.askCost);
  }
  walk(tree, 0);
  return out;
}

/** 树的文本可视化：每个节点/分支都标注候选集。 */
export function formatTree(solution: Solution): string {
  const lines: string[] = [];
  lines.push(`最坏总成本 = ${solution.worstCaseCost}（搜索子集状态数 = ${solution.statesExplored}）`);
  function walk(t: Tree, indent: string): void {
    if (t.kind === "leaf") {
      const tag = t.candidates.length > 1 ? "（不可区分等价组）" : "";
      lines.push(`${indent}叶 ${tag}{ ${t.candidates.join(", ")} }`);
      return;
    }
    lines.push(
      `${indent}{ ${t.candidates.join(", ")} } 询问 "${t.attribute}"（成本 ${t.askCost}）`,
    );
    for (const b of t.branches) {
      lines.push(`${indent}  ├─ ${t.attribute} = ${b.value} → { ${b.candidates.join(", ")} }`);
      walk(b.child, indent + "  │   ");
    }
  }
  walk(solution.tree, "");
  return lines.join("\n");
}
