/**
 * 基于子集动态规划（DPsize 风格）的稠密二叉连接树枚举优化器。
 *
 * - 枚举所有 bushy 二叉连接树（不限于左深树）。
 * - 基数与选择率均为精确有理数。
 * - 成本 = 所有内部节点中间结果基数之和 + 物化代价（可选）。
 * - 可配置是否禁止笛卡尔积。
 * - 最多 12 张表。
 */

import { Rational, type RationalLike } from "./rational.ts";

export const MAX_TABLES = 12;

export interface TableSpec {
  name: string;
  cardinality: RationalLike;
}

export interface EdgeSpec {
  /** 两张表名，表示等值连接谓词。 */
  tables: [string, string];
  /** 连接选择率（有理数），join 结果基数 = |A|*|B|*sel。 */
  selectivity: RationalLike;
}

export interface MaterializationSpec {
  /** 每行物化代价（乘以中间结果基数）。 */
  perRow?: RationalLike;
  /** 每次物化的固定代价。 */
  fixed?: RationalLike;
  /** 是否对最终结果（根节点）也计物化代价，默认 false。 */
  includeRoot?: boolean;
}

export interface OptimizeInput {
  tables: TableSpec[];
  edges?: EdgeSpec[];
  /** 为 false 时禁止任何含笛卡尔积的子计划，默认 true（允许）。 */
  allowCrossProducts?: boolean;
  /** 物化代价配置；缺省表示不物化中间结果。 */
  materialization?: MaterializationSpec;
}

export interface LeafNode {
  kind: "leaf";
  table: string;
  cardinality: Rational;
}

export interface JoinNode {
  kind: "join";
  /** 该节点覆盖的表集合（有序）。 */
  tables: string[];
  /** 输出基数估计。 */
  cardinality: Rational;
  /** 本次连接两侧之间所有谓词选择率之积（1 表示笛卡尔积）。 */
  joinSelectivity: Rational;
  isCrossProduct: boolean;
  /** 本步成本 = 中间结果基数 + 物化代价。 */
  stepCost: Rational;
  /** 物化代价部分（未配置则为 0）。 */
  materializationCost: Rational;
  /** 子树累计成本（含本步）。 */
  cumulativeCost: Rational;
  left: PlanNode;
  right: PlanNode;
}

export type PlanNode = LeafNode | JoinNode;

export interface SubplanEstimate {
  tables: string[];
  cardinality: Rational;
  bestCost: Rational;
}

export interface OptimizeResult {
  optimalCost: Rational;
  plan: PlanNode;
  stats: {
    tableCount: number;
    subsetsEvaluated: number;
    splitsConsidered: number;
    reachableSubsets: number;
  };
  /** 每个可达子集的最优估计（基数与成本），用于“各节点估算”输出。 */
  estimates: SubplanEstimate[];
}

interface DpEntry {
  cost: Rational;
  card: Rational;
  plan: PlanNode;
}

function popcount(x: number): number {
  let c = 0;
  while (x !== 0) {
    x &= x - 1;
    c++;
  }
  return c;
}

export function optimize(input: OptimizeInput): OptimizeResult {
  const tables = input.tables ?? [];
  const n = tables.length;
  if (n === 0) throw new Error("至少需要一张表");
  if (n > MAX_TABLES) throw new Error(`最多支持 ${MAX_TABLES} 张表，当前 ${n} 张`);

  const allowCross = input.allowCrossProducts ?? true;
  const matPerRow = Rational.of(input.materialization?.perRow ?? Rational.ZERO);
  const matFixed = Rational.of(input.materialization?.fixed ?? Rational.ZERO);
  const matIncludeRoot = input.materialization?.includeRoot ?? false;
  const useMaterialization = !matPerRow.isZero() || !matFixed.isZero();

  const names = tables.map((t, i) => {
    if (!t.name) throw new Error(`第 ${i} 张表缺少 name`);
    return t.name;
  });
  if (new Set(names).size !== n) throw new Error("表名必须唯一");
  const indexOf = new Map(names.map((nm, i) => [nm, i]));

  const baseCard = tables.map((t) => {
    const c = Rational.of(t.cardinality);
    if (c.cmp(Rational.ZERO) <= 0) throw new Error(`表 ${t.name} 的基数必须为正`);
    return c;
  });

  // 选择率矩阵，默认 1（无谓词）。
  const sel: Rational[][] = Array.from({ length: n }, () =>
    Array.from({ length: n }, () => Rational.ONE),
  );
  const adj: number[] = new Array<number>(n).fill(0);
  for (const edge of input.edges ?? []) {
    const [a, b] = edge.tables;
    const i = indexOf.get(a);
    const j = indexOf.get(b);
    if (i === undefined || j === undefined) {
      throw new Error(`连接边引用了未知表: ${a}, ${b}`);
    }
    if (i === j) throw new Error(`连接边两端不能是同一张表: ${a}`);
    const s = Rational.of(edge.selectivity);
    if (s.cmp(Rational.ZERO) <= 0) throw new Error(`选择率必须为正: ${a}-${b}`);
    sel[i][j] = sel[i][j].mul(s);
    sel[j][i] = sel[j][i].mul(s);
    adj[i] |= 1 << j;
    adj[j] |= 1 << i;
  }

  const fullMask = (1 << n) - 1;

  // selTo[i][mask]：表 i 与 mask 中所有表之间选择率的乘积。
  const selTo: Rational[][] = Array.from({ length: n }, () => {
    const row = new Array<Rational>(fullMask + 1);
    row[0] = Rational.ONE;
    return row;
  });
  for (let i = 0; i < n; i++) {
    for (let mask = 1; mask <= fullMask; mask++) {
      const low = mask & -mask;
      const j = 31 - Math.clz32(low);
      selTo[i][mask] = selTo[i][mask ^ low].mul(sel[i][j]);
    }
  }

  // cutSel(aMask, bMask)：跨切分的所有谓词选择率之积。
  const cutSel = (aMask: number, bMask: number): Rational => {
    let acc = Rational.ONE;
    let m = aMask;
    while (m !== 0) {
      const low = m & -m;
      const i = 31 - Math.clz32(low);
      acc = acc.mul(selTo[i][bMask]);
      m ^= low;
    }
    return acc;
  };

  // hasEdgeAcross(aMask, bMask)：切分两侧是否存在至少一条谓词边。
  const hasEdgeAcross = (aMask: number, bMask: number): boolean => {
    let m = aMask;
    while (m !== 0) {
      const low = m & -m;
      const i = 31 - Math.clz32(low);
      if ((adj[i] & bMask) !== 0) return true;
      m ^= low;
    }
    return false;
  };

  const best: (DpEntry | undefined)[] = new Array<DpEntry | undefined>(fullMask + 1);
  let splitsConsidered = 0;

  for (let i = 0; i < n; i++) {
    const card = baseCard[i];
    best[1 << i] = {
      cost: Rational.ZERO,
      card,
      plan: { kind: "leaf", table: names[i], cardinality: card },
    };
  }

  for (let mask = 1; mask <= fullMask; mask++) {
    if ((mask & (mask - 1)) === 0) continue; // 单表已初始化
    const lowest = mask & -mask;
    let bestEntry: DpEntry | undefined;

    // 枚举包含最低位的真子集，保证每个二分只考虑一次。
    for (let sub = (mask - 1) & mask; sub !== 0; sub = (sub - 1) & mask) {
      if ((sub & lowest) === 0) continue;
      const other = mask ^ sub;
      if (other === 0) continue;
      const leftEntry = best[sub];
      const rightEntry = best[other];
      if (!leftEntry || !rightEntry) continue;

      const cross = !hasEdgeAcross(sub, other);
      if (cross && !allowCross) continue;
      splitsConsidered++;

      const joinSel = cutSel(sub, other);
      const card = leftEntry.card.mul(rightEntry.card).mul(joinSel);
      const isRoot = mask === fullMask;
      let matCost = Rational.ZERO;
      if (useMaterialization && (matIncludeRoot || !isRoot)) {
        matCost = matPerRow.mul(card).add(matFixed);
      }
      const stepCost = card.add(matCost);
      const cost = leftEntry.cost.add(rightEntry.cost).add(stepCost);

      if (!bestEntry || cost.lt(bestEntry.cost)) {
        const tablesInNode: string[] = [];
        for (let i = 0; i < n; i++) {
          if ((mask & (1 << i)) !== 0) tablesInNode.push(names[i]);
        }
        bestEntry = {
          cost,
          card,
          plan: {
            kind: "join",
            tables: tablesInNode,
            cardinality: card,
            joinSelectivity: joinSel,
            isCrossProduct: cross,
            stepCost,
            materializationCost: matCost,
            cumulativeCost: cost,
            left: leftEntry.plan,
            right: rightEntry.plan,
          },
        };
      }
    }
    best[mask] = bestEntry;
  }

  const finalEntry = best[fullMask];
  if (!finalEntry) {
    throw new Error(
      "在给定约束下不存在完整连接计划（连接图不连通且禁止了笛卡尔积）",
    );
  }

  const estimates: SubplanEstimate[] = [];
  let reachable = 0;
  for (let mask = 1; mask <= fullMask; mask++) {
    const entry = best[mask];
    if (!entry) continue;
    reachable++;
    const tablesInMask: string[] = [];
    for (let i = 0; i < n; i++) {
      if ((mask & (1 << i)) !== 0) tablesInMask.push(names[i]);
    }
    estimates.push({
      tables: tablesInMask,
      cardinality: entry.card,
      bestCost: entry.cost,
    });
  }

  return {
    optimalCost: finalEntry.cost,
    plan: finalEntry.plan,
    stats: {
      tableCount: n,
      subsetsEvaluated: fullMask,
      splitsConsidered,
      reachableSubsets: reachable,
    },
    estimates,
  };
}
