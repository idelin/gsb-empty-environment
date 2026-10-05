import { solveLpMin } from './simplex.ts';
import { evaluateSolution, EPS } from './evaluate.ts';
import type { Problem, PurchaseLine, Supplier } from './types.ts';

const INF = 1e15;
const TIER_GAP = 1e-6;

/** 订单金额分段：在每个区间内折扣率与固定运费都是常数，问题变为线性。 */
interface Segment {
  lo: number;        // 折前金额下界（含）
  hi: number;        // 折前金额上界（不含），Infinity 表示顶档
  rate: number;      // 该段整单折扣率
  fixed: number;     // 该段固定运费（满免段为 0）
}

interface EdgeNorm {
  s: number;         // 供应商下标
  m: number;         // 物料下标
  price: number;     // 单价
  pack: number;      // 件/包
  minPacks: number;  // 起订量折算的最小包数（半连续约束）
  maxPacks: number;  // 交付上限/产能/需求上界折算的最大包数
}

interface SupplierNorm {
  capacity: number;
  perUnitFreight: number;
  segments: Segment[];
  maxRate: number;
}

interface Norm {
  problem: Problem;
  demands: number[];
  uppers: number[];
  suppliers: SupplierNorm[];
  edges: EdgeNorm[];
  edgesByMaterial: EdgeNorm[][];
  edgesBySupplier: EdgeNorm[][];
  maxSuppliers: number;
}

function buildSegments(supplier: Supplier): Segment[] {
  const freight = supplier.freight ?? { fixed: 0 };
  const tiers = [...(supplier.discountTiers ?? [])].sort((a, b) => a.minSubtotal - b.minSubtotal);
  const breakpoints = new Set<number>([0]);
  for (const t of tiers) if (t.minSubtotal > 0) breakpoints.add(t.minSubtotal);
  const freeAbove = freight.freeAboveSubtotal;
  if (freeAbove != null && freeAbove > 0) breakpoints.add(freeAbove);
  const sorted = [...breakpoints].sort((a, b) => a - b);

  const segments: Segment[] = [];
  for (let i = 0; i < sorted.length; i++) {
    const lo = sorted[i]!;
    const hi = i + 1 < sorted.length ? sorted[i + 1]! : Infinity;
    if (hi - lo < TIER_GAP) continue;
    let rate = 0;
    for (const t of tiers) if (lo + TIER_GAP >= t.minSubtotal) rate = t.discountRate;
    const fixed = freeAbove != null && lo + TIER_GAP >= freeAbove ? 0 : freight.fixed;
    segments.push({ lo, hi, rate, fixed });
  }
  return segments;
}

export function normalize(problem: Problem): Norm {
  const materials = problem.demands.map((d) => d.materialId);
  const materialIndex = new Map(materials.map((id, i) => [id, i]));
  const demands = problem.demands.map((d) => d.demand);
  const uppers = problem.demands.map(
    (d) => d.demand * (1 + (d.maxOverbuyRatio ?? problem.maxOverbuyRatio ?? 0)),
  );

  const suppliers: SupplierNorm[] = problem.suppliers.map((s) => {
    const segments = buildSegments(s);
    return {
      capacity: s.capacity ?? INF,
      perUnitFreight: s.freight?.perUnit ?? 0,
      segments,
      maxRate: segments.reduce((acc, seg) => Math.max(acc, seg.rate), 0),
    };
  });

  const edges: EdgeNorm[] = [];
  for (let s = 0; s < problem.suppliers.length; s++) {
    const supplier = problem.suppliers[s]!;
    for (const item of supplier.items) {
      const m = materialIndex.get(item.materialId);
      if (m == null) continue;
      const pack = item.packSize ?? 1;
      const moq = item.moq ?? 0;
      const minPacks = Math.max(1, Math.ceil((moq - EPS) / pack));
      const maxByItem = item.maxQty ?? INF;
      const maxPacks = Math.floor(
        (Math.min(maxByItem, suppliers[s]!.capacity, uppers[m]!) + EPS) / pack,
      );
      if (maxPacks < minPacks) continue; // 该供应渠道不可用
      edges.push({ s, m, price: item.unitPrice, pack, minPacks, maxPacks });
    }
  }

  const edgesByMaterial: EdgeNorm[][] = materials.map(() => []);
  const edgesBySupplier: EdgeNorm[][] = suppliers.map(() => []);
  for (const e of edges) {
    edgesByMaterial[e.m]!.push(e);
    edgesBySupplier[e.s]!.push(e);
  }

  return {
    problem,
    demands,
    uppers,
    suppliers,
    edges,
    edgesByMaterial,
    edgesBySupplier,
    maxSuppliers: problem.maxSuppliers ?? 6,
  };
}

interface Assignment {
  segIndex: number; // -1 = 不使用该供应商
}

interface LpModel {
  A: number[][];
  b: number[];
  c: number[];
  varEdges: EdgeNorm[];
  consts: number; // 已确定供应商的固定运费合计
}

/**
 * 构建 LP 松弛。decidedCount 之前的供应商按 assignment 固定（不使用/某金额段），
 * 之后的供应商按“最优惠折扣率 + 免固定运费”放松。boundOverrides 用于分支定界中收紧变量界。
 */
function buildLp(
  norm: Norm,
  assignment: Assignment[],
  decidedCount: number,
  boundOverrides: Map<EdgeNorm, { lb: number; ub: number }> | null,
): LpModel {
  const varEdges: EdgeNorm[] = [];
  for (const e of norm.edges) {
    const decided = e.s < decidedCount;
    if (decided && assignment[e.s]!.segIndex < 0) continue; // 供应商不使用
    varEdges.push(e);
  }
  const varIndex = new Map<EdgeNorm, number>();
  varEdges.forEach((e, i) => varIndex.set(e, i));

  const A: number[][] = [];
  const b: number[] = [];
  const c = new Array<number>(varEdges.length).fill(0);
  let consts = 0;

  const rateOf = (e: EdgeNorm): number => {
    if (e.s < decidedCount) {
      const seg = norm.suppliers[e.s]!.segments[assignment[e.s]!.segIndex]!;
      return seg.rate;
    }
    return norm.suppliers[e.s]!.maxRate; // 未决定供应商按最优折扣放松
  };

  varEdges.forEach((e, i) => {
    c[i] = (1 - rateOf(e)) * e.price * e.pack + norm.suppliers[e.s]!.perUnitFreight * e.pack;
  });

  const addRow = (coeffs: Map<number, number>, rhs: number): void => {
    const row = new Array<number>(varEdges.length).fill(0);
    for (const [i, v] of coeffs) row[i] = v;
    A.push(row);
    b.push(rhs);
  };

  // 需求下界 / 超购上界
  for (let m = 0; m < norm.demands.length; m++) {
    const coeffs = new Map<number, number>();
    for (const e of norm.edgesByMaterial[m]!) {
      const i = varIndex.get(e);
      if (i != null) coeffs.set(i, e.pack);
    }
    const lower = new Map<number, number>();
    for (const [i, v] of coeffs) lower.set(i, -v);
    addRow(lower, -norm.demands[m]!);
    addRow(coeffs, norm.uppers[m]!);
  }

  // 供应商产能
  for (let s = 0; s < norm.suppliers.length; s++) {
    const cap = norm.suppliers[s]!.capacity;
    if (cap >= INF) continue;
    const coeffs = new Map<number, number>();
    for (const e of norm.edgesBySupplier[s]!) {
      const i = varIndex.get(e);
      if (i != null) coeffs.set(i, e.pack);
    }
    if (coeffs.size > 0) addRow(coeffs, cap);
  }

  // 变量上界（交付上限/产能/需求上界折算）
  varEdges.forEach((e, i) => {
    addRow(new Map([[i, 1]]), e.maxPacks);
  });

  // 已确定供应商的金额段约束与固定运费
  for (let s = 0; s < decidedCount; s++) {
    const segIndex = assignment[s]!.segIndex;
    if (segIndex < 0) continue;
    const seg = norm.suppliers[s]!.segments[segIndex]!;
    consts += seg.fixed;
    const coeffs = new Map<number, number>();
    for (const e of norm.edgesBySupplier[s]!) {
      const i = varIndex.get(e);
      if (i != null) coeffs.set(i, e.price * e.pack);
    }
    if (seg.lo > 0) {
      const lower = new Map<number, number>();
      for (const [i, v] of coeffs) lower.set(i, -v);
      addRow(lower, -seg.lo);
    }
    if (seg.hi < Infinity) {
      addRow(coeffs, seg.hi - TIER_GAP);
    }
  }

  // 分支定界的变量界覆盖
  if (boundOverrides) {
    for (const [e, bounds] of boundOverrides) {
      const i = varIndex.get(e);
      if (i == null) continue;
      addRow(new Map([[i, 1]]), bounds.ub);
      if (bounds.lb > 0) addRow(new Map([[i, -1]]), -bounds.lb);
    }
  }

  return { A, b, c, varEdges, consts };
}

class BudgetExhausted extends Error {}

export interface ExactResult {
  lines: PurchaseLine[] | null;
  cost: number;
  proven: boolean;
  lpSolves: number;
  branchNodes: number;
}

export interface ExactOptions {
  nodeLimit?: number;
  timeLimitMs?: number;
  incumbent?: { lines: PurchaseLine[]; cost: number } | null;
}

/**
 * 精确求解：枚举每个供应商的“金额段”（不使用 / 各阶梯段），
 * 对每个完整组合做整数分支定界（包数取整 + 起订量半连续约束），
 * LP 松弛提供下界进行剪枝。折扣与运费始终按整单核算，绝不拆成单价。
 */
export function solveExact(problem: Problem, options: ExactOptions = {}): ExactResult {
  const norm = normalize(problem);
  const nodeLimit = options.nodeLimit ?? 200_000;
  const deadline = Date.now() + (options.timeLimitMs ?? 10_000);

  let bestLines: PurchaseLine[] | null = options.incumbent?.lines ?? null;
  let bestCost = options.incumbent?.cost ?? Infinity;
  let lpSolves = 0;
  let branchNodes = 0;
  let exhausted = false;

  const solveLp = (model: LpModel): { objective: number; x: number[] } => {
    if (++lpSolves > nodeLimit || Date.now() > deadline) {
      exhausted = true;
      throw new BudgetExhausted();
    }
    const res = solveLpMin(model.A, model.b, model.c);
    if (res.status === 'infeasible') return { objective: Infinity, x: [] };
    if (res.status === 'unbounded') return { objective: -Infinity, x: res.x };
    return { objective: res.objective + model.consts, x: res.x };
  };

  const considerCandidate = (model: LpModel, x: number[]): void => {
    const lines: PurchaseLine[] = [];
    for (let i = 0; i < model.varEdges.length; i++) {
      const packs = Math.round(x[i]!);
      if (packs <= 0) continue;
      const e = model.varEdges[i]!;
      lines.push({
        supplierId: problem.suppliers[e.s]!.id,
        materialId: problem.demands[e.m]!.materialId,
        packs,
      });
    }
    const evaluation = evaluateSolution(problem, lines);
    if (evaluation.feasible && evaluation.totalCost < bestCost - 1e-9) {
      bestCost = evaluation.totalCost;
      bestLines = lines;
    }
  };

  // 完整金额段组合下的整数分支定界
  const solveLeaf = (assignment: Assignment[]): void => {
    const branchAndBound = (overrides: Map<EdgeNorm, { lb: number; ub: number }>): void => {
      branchNodes++;
      const model = buildLp(norm, assignment, norm.suppliers.length, overrides);
      const { objective, x } = solveLp(model);
      if (objective >= bestCost - 1e-9) return;

      // 找分支变量：优先起订量半连续违约，其次分数包数
      let branchEdge: EdgeNorm | null = null;
      let branchValue = 0;
      let semicontinuous = false;
      for (let i = 0; i < model.varEdges.length; i++) {
        const e = model.varEdges[i]!;
        const v = x[i]!;
        const ov = overrides.get(e);
        const lb = ov?.lb ?? 0;
        if (v > lb + EPS && v < e.minPacks - EPS) {
          branchEdge = e;
          branchValue = v;
          semicontinuous = true;
          break;
        }
        if (!semicontinuous && Math.abs(v - Math.round(v)) > 1e-7) {
          branchEdge = e;
          branchValue = v;
        }
      }
      if (!branchEdge) {
        considerCandidate(model, x);
        return;
      }

      const current = overrides.get(branchEdge) ?? { lb: 0, ub: branchEdge.maxPacks };
      if (semicontinuous) {
        // 分支一：不买；分支二：至少买 minPacks 包
        const left = new Map(overrides);
        left.set(branchEdge, { lb: 0, ub: 0 });
        branchAndBound(left);
        const right = new Map(overrides);
        right.set(branchEdge, { lb: branchEdge.minPacks, ub: current.ub });
        branchAndBound(right);
      } else {
        const left = new Map(overrides);
        left.set(branchEdge, { lb: current.lb, ub: Math.floor(branchValue) });
        branchAndBound(left);
        const right = new Map(overrides);
        right.set(branchEdge, { lb: Math.ceil(branchValue), ub: current.ub });
        branchAndBound(right);
      }
    };
    branchAndBound(new Map());
  };

  // 枚举供应商金额段组合，内部节点用 LP 松弛下界剪枝
  const assignment: Assignment[] = norm.suppliers.map(() => ({ segIndex: -1 }));
  const dfs = (s: number, usedCount: number): void => {
    if (exhausted) return;
    if (s === norm.suppliers.length) {
      solveLeaf(assignment);
      return;
    }
    const segments = norm.suppliers[s]!.segments;
    // 优先尝试折扣力度大的段（更快找到优质解增强剪枝），最后尝试“不使用”
    const order = segments
      .map((seg, segIndex) => ({ seg, segIndex }))
      .sort((a, b) => b.seg.rate - a.seg.rate || a.seg.fixed - b.seg.fixed);
    const choices: number[] = [...order.map((o) => o.segIndex), -1];
    for (const segIndex of choices) {
      if (segIndex >= 0 && usedCount + 1 > norm.maxSuppliers) continue;
      assignment[s] = { segIndex };
      const model = buildLp(norm, assignment, s + 1, null);
      const { objective } = solveLp(model);
      if (objective < bestCost - 1e-9) {
        dfs(s + 1, usedCount + (segIndex >= 0 ? 1 : 0));
      }
      if (exhausted) return;
    }
  };

  try {
    dfs(0, 0);
  } catch (err) {
    if (!(err instanceof BudgetExhausted)) throw err;
  }

  return { lines: bestLines, cost: bestCost, proven: !exhausted, lpSolves, branchNodes };
}
