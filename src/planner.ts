import { evaluateSolution, type SupplierOrderDetail } from './evaluate.ts';
import { solveExact } from './exact.ts';
import { solveHeuristic } from './heuristic.ts';
import type { Problem, PurchaseLine } from './types.ts';

export interface PlanOptions {
  maxSuppliers?: number;   // 方案最多使用的供应商家数，默认 6
  timeLimitMs?: number;    // 精确求解时间预算，默认 8000
  nodeLimit?: number;      // 精确求解 LP 次数预算，默认 200000
  heuristicRestarts?: number;
}

export interface OrderExplanation extends SupplierOrderDetail {
  tierRationale: string;
}

export interface MaterialFulfillment {
  materialId: string;
  demand: number;
  purchased: number;
  overbuyRatio: number;
  maxOverbuyRatio: number;
}

export interface PlanResult {
  status: 'optimal' | 'feasible' | 'infeasible';
  totalCost: number;
  merchandiseCost: number;
  discountAmount: number;
  freightCost: number;
  orders: OrderExplanation[];
  materials: MaterialFulfillment[];
  stats: {
    provenOptimal: boolean;
    lpSolves: number;
    branchNodes: number;
    timeMs: number;
  };
}

function validate(problem: Problem): void {
  if (!problem.demands?.length) throw new Error('至少需要一条物料需求');
  if (!problem.suppliers?.length) throw new Error('至少需要一家供应商');
  for (const d of problem.demands) {
    if (!(d.demand > 0)) throw new Error(`物料 ${d.materialId} 需求必须为正数`);
    const ratio = d.maxOverbuyRatio ?? problem.maxOverbuyRatio ?? 0;
    if (ratio < 0) throw new Error(`物料 ${d.materialId} 超购比例不能为负`);
  }
  for (const s of problem.suppliers) {
    for (const item of s.items) {
      if (!(item.unitPrice >= 0)) throw new Error(`供应商 ${s.id} 物料 ${item.materialId} 单价非法`);
      if (item.packSize != null && (!Number.isInteger(item.packSize) || item.packSize < 1)) {
        throw new Error(`供应商 ${s.id} 物料 ${item.materialId} 整包倍数必须为正整数`);
      }
      if (item.moq != null && item.moq < 0) throw new Error(`供应商 ${s.id} 物料 ${item.materialId} 起订量非法`);
    }
    for (const t of s.discountTiers ?? []) {
      if (!(t.discountRate >= 0 && t.discountRate < 1)) {
        throw new Error(`供应商 ${s.id} 阶梯折扣率必须在 [0, 1) 区间`);
      }
    }
  }
  const offered = new Set(problem.suppliers.flatMap((s) => s.items.map((i) => i.materialId)));
  for (const d of problem.demands) {
    if (!offered.has(d.materialId)) throw new Error(`没有供应商提供物料 ${d.materialId}`);
  }
}

const fmt = (v: number): string => v.toFixed(2);

function buildRationale(order: SupplierOrderDetail, supplierTiersCount: number): string {
  const parts: string[] = [];
  if (order.tierIndex >= 0) {
    parts.push(
      `折前金额 ¥${fmt(order.subtotal)} 达到阶梯#${order.tierIndex + 1}（门槛 ¥${fmt(order.tierThreshold)}），整单享 ${(order.discountRate * 100).toFixed(1)}% 折扣，折让 ¥${fmt(order.discountAmount)}`,
    );
  } else {
    parts.push(
      supplierTiersCount > 0
        ? `折前金额 ¥${fmt(order.subtotal)} 未达最低阶梯门槛，无折扣`
        : `该供应商无阶梯折扣，折前金额 ¥${fmt(order.subtotal)}`,
    );
  }
  if (order.nextThreshold != null) {
    parts.push(`距下一档门槛 ¥${fmt(order.nextThreshold)} 还差 ¥${fmt(order.nextThreshold - order.subtotal)}`);
  } else if (order.tierIndex >= 0) {
    parts.push('已达最高档位');
  }
  parts.push(`运费 ¥${fmt(order.freight)}`);
  return parts.join('；');
}

/**
 * 采购规划主入口：先用启发式找到优质可行解作为上界，
 * 再用“金额段枚举 × 整数分支定界”精确求解并（在预算内）证明最优性。
 */
export function plan(problem: Problem, options: PlanOptions = {}): PlanResult {
  validate(problem);
  const normalized: Problem = { ...problem, maxSuppliers: options.maxSuppliers ?? problem.maxSuppliers ?? 6 };
  const started = Date.now();

  const heuristic = solveHeuristic(normalized, options.heuristicRestarts ?? 12);
  const exact = solveExact(normalized, {
    nodeLimit: options.nodeLimit ?? 200_000,
    timeLimitMs: options.timeLimitMs ?? 8_000,
    incumbent: heuristic,
  });

  const lines: PurchaseLine[] | null = exact.lines ?? heuristic?.lines ?? null;
  if (!lines) {
    return {
      status: 'infeasible',
      totalCost: 0,
      merchandiseCost: 0,
      discountAmount: 0,
      freightCost: 0,
      orders: [],
      materials: problem.demands.map((d) => ({
        materialId: d.materialId,
        demand: d.demand,
        purchased: 0,
        overbuyRatio: 0,
        maxOverbuyRatio: d.maxOverbuyRatio ?? problem.maxOverbuyRatio ?? 0,
      })),
      stats: {
        provenOptimal: exact.proven,
        lpSolves: exact.lpSolves,
        branchNodes: exact.branchNodes,
        timeMs: Date.now() - started,
      },
    };
  }

  const evaluation = evaluateSolution(normalized, lines);
  const supplierById = new Map(normalized.suppliers.map((s) => [s.id, s]));
  const orders: OrderExplanation[] = evaluation.orders
    .map((order) => ({
      ...order,
      tierRationale: buildRationale(order, (supplierById.get(order.supplierId)?.discountTiers ?? []).length),
    }))
    .sort((a, b) => a.supplierId.localeCompare(b.supplierId));

  const materials: MaterialFulfillment[] = normalized.demands.map((d) => {
    const purchased = evaluation.purchasedByMaterial.get(d.materialId) ?? 0;
    return {
      materialId: d.materialId,
      demand: d.demand,
      purchased,
      overbuyRatio: purchased / d.demand - 1,
      maxOverbuyRatio: d.maxOverbuyRatio ?? normalized.maxOverbuyRatio ?? 0,
    };
  });

  return {
    status: exact.proven ? 'optimal' : 'feasible',
    totalCost: evaluation.totalCost,
    merchandiseCost: evaluation.merchandiseCost,
    discountAmount: evaluation.discountAmount,
    freightCost: evaluation.freightCost,
    orders,
    materials,
    stats: {
      provenOptimal: exact.proven,
      lpSolves: exact.lpSolves,
      branchNodes: exact.branchNodes,
      timeMs: Date.now() - started,
    },
  };
}
