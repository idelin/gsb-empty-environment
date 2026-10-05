import { MinCostFlow } from './minCostFlow.ts';
import type { EdgeHandle } from './minCostFlow.ts';
import type {
  OrderItem,
  SolveOptions,
  SolveResult,
  Warehouse,
  WarehouseAllocation,
} from './types.ts';

export const MAX_WAREHOUSES = 8;
export const MAX_SKUS = 12;

const INF = 1 << 29;

interface NormalizedInput {
  skus: string[];
  demand: number[];
  warehouses: Warehouse[];
  allowOverDelivery: boolean;
}

function validateAndNormalize(
  order: OrderItem[],
  warehouses: Warehouse[],
  options: SolveOptions,
): NormalizedInput {
  if (warehouses.length === 0) throw new Error('at least one warehouse is required');
  if (warehouses.length > MAX_WAREHOUSES) {
    throw new Error(`at most ${MAX_WAREHOUSES} warehouses are supported`);
  }
  const skuSet: string[] = [];
  const demandMap = new Map<string, number>();
  for (const item of order) {
    if (!Number.isInteger(item.quantity) || item.quantity < 0) {
      throw new Error(`invalid quantity for sku ${item.sku}`);
    }
    if (!demandMap.has(item.sku)) {
      skuSet.push(item.sku);
      demandMap.set(item.sku, 0);
    }
    demandMap.set(item.sku, demandMap.get(item.sku)! + item.quantity);
  }
  if (skuSet.length > MAX_SKUS) {
    throw new Error(`at most ${MAX_SKUS} distinct skus are supported`);
  }
  for (const w of warehouses) {
    if (!Number.isFinite(w.fixedFee) || w.fixedFee < 0) {
      throw new Error(`invalid fixedFee for warehouse ${w.id}`);
    }
    if (!Number.isFinite(w.perItemFee) || w.perItemFee < 0) {
      throw new Error(`invalid perItemFee for warehouse ${w.id}`);
    }
    if (
      w.freeShippingThreshold !== undefined &&
      (!Number.isInteger(w.freeShippingThreshold) || w.freeShippingThreshold < 1)
    ) {
      throw new Error(`invalid freeShippingThreshold for warehouse ${w.id}`);
    }
    for (const sku of skuSet) {
      const s = w.stock[sku] ?? 0;
      if (!Number.isInteger(s) || s < 0) {
        throw new Error(`invalid stock of ${sku} for warehouse ${w.id}`);
      }
    }
  }
  return {
    skus: skuSet,
    demand: skuSet.map((s) => demandMap.get(s)!),
    warehouses,
    allowOverDelivery: options.allowOverDelivery !== false,
  };
}

interface ModeSpec {
  /** 该仓发货件数下界（普通模式为 1，免运模式为门槛值） */
  lower: number;
  /** 上界（普通模式为门槛-1，免运模式不限） */
  upper: number;
  /** 每件运费（免运模式为 0） */
  unitCost: number;
  free: boolean;
}

function warehouseMode(w: Warehouse, free: boolean): ModeSpec | null {
  if (free) {
    if (w.freeShippingThreshold === undefined) return null;
    return {
      lower: Math.max(1, w.freeShippingThreshold),
      upper: INF,
      unitCost: 0,
      free: true,
    };
  }
  const upper = w.freeShippingThreshold !== undefined ? w.freeShippingThreshold - 1 : INF;
  if (upper < 1) return null;
  return { lower: 1, upper, unitCost: w.perItemFee, free: false };
}

interface Candidate {
  cost: number;
  packages: number;
  shipped: number;
  /** alloc[w][skuIndex]，仅入选仓 */
  alloc: Map<number, number[]>;
  modes: Map<number, ModeSpec>;
}

/**
 * 精确求解：枚举仓集合（2^W）× 每仓模式（普通/免运，共 3^W 种组合），
 * 每个组合归结为带下界的最小费用流。主目标总运费最低，次目标包裹数最少，
 * 再次目标总发货件数最少（减少超发）。
 */
export function solve(
  order: OrderItem[],
  warehouses: Warehouse[],
  options: SolveOptions = {},
): SolveResult {
  const input = validateAndNormalize(order, warehouses, options);
  const W = input.warehouses.length;
  const S = input.skus.length;
  const totalDemand = input.demand.reduce((a, b) => a + b, 0);

  const lowerBound = computeLowerBound(input);

  if (totalDemand === 0) {
    return {
      status: 'optimal',
      totalCost: 0,
      packages: 0,
      lowerBound: 0,
      gap: 0,
      allocations: [],
      overDelivery: {},
    };
  }

  // 节点编号：0 = 源，1..W = 仓，W+1..W+S = SKU，W+S+1 = 汇
  const SRC = 0;
  const SNK = W + S + 1;
  const skuNode = (j: number) => W + 1 + j;
  const whNode = (i: number) => 1 + i;

  let best: Candidate | null = null;

  for (let mask = 1; mask < 1 << W; mask++) {
    const members: number[] = [];
    for (let i = 0; i < W; i++) if (mask & (1 << i)) members.push(i);

    // 库存可行性预检
    let stockOk = true;
    for (let j = 0; j < S && stockOk; j++) {
      let cap = 0;
      for (const i of members) cap += input.warehouses[i].stock[input.skus[j]] ?? 0;
      if (cap < input.demand[j]) stockOk = false;
    }
    if (!stockOk) continue;

    const freeCapable = members.filter(
      (i) => input.warehouses[i].freeShippingThreshold !== undefined,
    );

    for (let modeMask = 0; modeMask < 1 << freeCapable.length; modeMask++) {
      const modes = new Map<number, ModeSpec>();
      let valid = true;
      for (const i of members) {
        const freeIdx = freeCapable.indexOf(i);
        const free = freeIdx >= 0 && (modeMask & (1 << freeIdx)) !== 0;
        const mode = warehouseMode(input.warehouses[i], free);
        if (mode === null) {
          valid = false;
          break;
        }
        modes.set(i, mode);
      }
      if (!valid) continue;

      const mcf = new MinCostFlow(W + S + 2);
      const srcEdges = new Map<number, EdgeHandle>();
      const cellEdges: { w: number; j: number; h: EdgeHandle }[] = [];
      for (const i of members) {
        const mode = modes.get(i)!;
        srcEdges.set(
          i,
          mcf.addEdge(SRC, whNode(i), mode.lower, mode.upper, mode.unitCost),
        );
        for (let j = 0; j < S; j++) {
          const stock = input.warehouses[i].stock[input.skus[j]] ?? 0;
          if (stock > 0) {
            cellEdges.push({
              w: i,
              j,
              h: mcf.addEdge(whNode(i), skuNode(j), 0, stock, 0),
            });
          }
        }
      }
      for (let j = 0; j < S; j++) {
        const upper = input.allowOverDelivery ? INF : input.demand[j];
        mcf.addEdge(skuNode(j), SNK, input.demand[j], upper, 0);
      }
      mcf.addEdge(SNK, SRC, 0, INF, 0);

      const flowCost = mcf.solve();
      if (flowCost === null) continue;

      let cost = 0;
      let shipped = 0;
      for (const i of members) {
        const mode = modes.get(i)!;
        const x = mcf.edgeFlow(srcEdges.get(i)!);
        shipped += x;
        if (!mode.free) cost += input.warehouses[i].fixedFee + input.warehouses[i].perItemFee * x;
      }

      const packages = members.length;
      if (
        best === null ||
        cost < best.cost ||
        (cost === best.cost && packages < best.packages) ||
        (cost === best.cost && packages === best.packages && shipped < best.shipped)
      ) {
        const alloc = new Map<number, number[]>();
        for (const i of members) alloc.set(i, new Array<number>(S).fill(0));
        for (const { w, j, h } of cellEdges) {
          alloc.get(w)![j] = mcf.edgeFlow(h);
        }
        best = { cost, packages, shipped, alloc, modes };
      }
    }
  }

  if (best === null) {
    return {
      status: 'infeasible',
      totalCost: null,
      packages: null,
      lowerBound: null,
      gap: null,
      allocations: [],
      overDelivery: {},
    };
  }

  trimOverDelivery(best, input);

  const allocations: WarehouseAllocation[] = [];
  const overDelivery: Record<string, number> = {};
  for (const [i, mode] of best.modes) {
    const w = input.warehouses[i];
    const row = best.alloc.get(i)!;
    const items: Record<string, number> = {};
    let totalItems = 0;
    for (let j = 0; j < S; j++) {
      if (row[j] > 0) {
        items[input.skus[j]] = row[j];
        totalItems += row[j];
      }
    }
    if (totalItems === 0) continue;
    allocations.push({
      warehouseId: w.id,
      items,
      totalItems,
      shippingCost: mode.free ? 0 : w.fixedFee + w.perItemFee * totalItems,
      freeShippingApplied: mode.free,
    });
  }
  for (let j = 0; j < S; j++) {
    let shippedSku = 0;
    for (const row of best.alloc.values()) shippedSku += row[j];
    if (shippedSku > input.demand[j]) {
      overDelivery[input.skus[j]] = shippedSku - input.demand[j];
    }
  }

  return {
    status: 'optimal',
    totalCost: best.cost,
    packages: allocations.length,
    lowerBound,
    gap: lowerBound === null ? null : best.cost - lowerBound,
    allocations,
    overDelivery,
  };
}

/**
 * 裁剪超发：在费用与包裹数不变的前提下，把每个 SKU 的发货量降回需求量。
 * 只在单位运费为 0 的仓（免运模式或单件运费为 0）上裁剪，保证费用不变；
 * 且不低于该仓模式的下界（免运仓保持在门槛之上）。
 */
function trimOverDelivery(best: Candidate, input: NormalizedInput): void {
  const slack = new Map<number, number>();
  for (const [i, mode] of best.modes) {
    const row = best.alloc.get(i)!;
    const x = row.reduce((a, b) => a + b, 0);
    slack.set(i, x - mode.lower);
  }
  for (let j = 0; j < input.skus.length; j++) {
    let excess = -input.demand[j];
    for (const row of best.alloc.values()) excess += row[j];
    if (excess <= 0) continue;
    for (const [i, mode] of best.modes) {
      if (excess <= 0) break;
      if (!mode.free && input.warehouses[i].perItemFee > 0) continue;
      const row = best.alloc.get(i)!;
      const take = Math.min(row[j], slack.get(i)!, excess);
      if (take > 0) {
        row[j] -= take;
        slack.set(i, slack.get(i)! - take);
        excess -= take;
      }
    }
  }
}

/**
 * 最优性下界：把每仓运费松弛为 g_w(x)：
 * - 无免运门槛的仓：g_w(x) = fixedFee + perItemFee * x（x>=1），g_w(0)=0 —— 即原费用；
 * - 有免运门槛的仓：g_w(x) = 0（松弛掉全部运费，因为免运模式费用为 0）。
 * 枚举无门槛仓的启用子集把固定费用问题精确化，每个子集是一个运输问题，
 * 用最小费用流求解。LB = min_U [ sum_{w in U} fixedFee_w + T(U ∪ 门槛仓) ] <= OPT。
 */
function computeLowerBound(input: NormalizedInput): number | null {
  const W = input.warehouses.length;
  const S = input.skus.length;
  const noThreshold: number[] = [];
  const withThreshold: number[] = [];
  for (let i = 0; i < W; i++) {
    if (input.warehouses[i].freeShippingThreshold === undefined) noThreshold.push(i);
    else withThreshold.push(i);
  }

  const SRC = 0;
  const SNK = W + S + 1;
  const skuNode = (j: number) => W + 1 + j;
  const whNode = (i: number) => 1 + i;

  let lb = Infinity;
  for (let uMask = 0; uMask < 1 << noThreshold.length; uMask++) {
    const active: number[] = [...withThreshold];
    let fixed = 0;
    for (let k = 0; k < noThreshold.length; k++) {
      if (uMask & (1 << k)) {
        active.push(noThreshold[k]);
        fixed += input.warehouses[noThreshold[k]].fixedFee;
      }
    }
    // 库存可行性预检
    let stockOk = true;
    for (let j = 0; j < S && stockOk; j++) {
      let cap = 0;
      for (const i of active) cap += input.warehouses[i].stock[input.skus[j]] ?? 0;
      if (cap < input.demand[j]) stockOk = false;
    }
    if (!stockOk) continue;

    const mcf = new MinCostFlow(W + S + 2);
    for (const i of active) {
      const w = input.warehouses[i];
      const unit = w.freeShippingThreshold !== undefined ? 0 : w.perItemFee;
      mcf.addEdge(SRC, whNode(i), 0, INF, unit);
      for (let j = 0; j < S; j++) {
        const stock = w.stock[input.skus[j]] ?? 0;
        if (stock > 0) mcf.addEdge(whNode(i), skuNode(j), 0, stock, 0);
      }
    }
    for (let j = 0; j < S; j++) {
      mcf.addEdge(skuNode(j), SNK, input.demand[j], input.demand[j], 0);
    }
    mcf.addEdge(SNK, SRC, 0, INF, 0);

    const cost = mcf.solve();
    if (cost === null) continue;
    if (fixed + cost < lb) lb = fixed + cost;
  }
  return lb === Infinity ? null : lb;
}
