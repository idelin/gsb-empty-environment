import { evaluateSolution, supplierOrderCost, EPS } from './evaluate.ts';
import type { Problem, PurchaseLine, Supplier } from './types.ts';

/**  mulberry32 确定性随机数，便于复现。 */
function makeRng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

type Lines = Map<string, Map<string, number>>; // supplierId -> materialId -> packs

function linesToList(lines: Lines): PurchaseLine[] {
  const out: PurchaseLine[] = [];
  for (const [supplierId, byMaterial] of lines) {
    for (const [materialId, packs] of byMaterial) {
      if (packs > 0) out.push({ supplierId, materialId, packs });
    }
  }
  return out;
}

function cloneLines(lines: Lines): Lines {
  const out: Lines = new Map();
  for (const [s, m] of lines) out.set(s, new Map(m));
  return out;
}

interface Ctx {
  problem: Problem;
  supplierById: Map<string, Supplier>;
  demandByMaterial: Map<string, number>;
  upperByMaterial: Map<string, number>;
  maxSuppliers: number;
}

function makeCtx(problem: Problem): Ctx {
  const demandByMaterial = new Map<string, number>();
  const upperByMaterial = new Map<string, number>();
  for (const d of problem.demands) {
    demandByMaterial.set(d.materialId, d.demand);
    upperByMaterial.set(
      d.materialId,
      d.demand * (1 + (d.maxOverbuyRatio ?? problem.maxOverbuyRatio ?? 0)),
    );
  }
  return {
    problem,
    supplierById: new Map(problem.suppliers.map((s) => [s.id, s])),
    demandByMaterial,
    upperByMaterial,
    maxSuppliers: problem.maxSuppliers ?? 6,
  };
}

function getPacks(lines: Lines, supplierId: string, materialId: string): number {
  return lines.get(supplierId)?.get(materialId) ?? 0;
}

function setPacks(lines: Lines, supplierId: string, materialId: string, packs: number): void {
  let byMaterial = lines.get(supplierId);
  if (!byMaterial) {
    byMaterial = new Map();
    lines.set(supplierId, byMaterial);
  }
  if (packs <= 0) byMaterial.delete(materialId);
  else byMaterial.set(materialId, packs);
}

function supplierQtyMap(lines: Lines, supplierId: string, supplier: Supplier): Map<string, number> {
  const qty = new Map<string, number>();
  const byMaterial = lines.get(supplierId);
  if (byMaterial) {
    for (const [materialId, packs] of byMaterial) {
      const item = supplier.items.find((it) => it.materialId === materialId);
      if (item) qty.set(materialId, packs * (item.packSize ?? 1));
    }
  }
  return qty;
}

/** 某供应商当前订单的整单成本（含阶梯折扣与运费）。 */
function supplierCost(ctx: Ctx, lines: Lines, supplierId: string): number {
  const supplier = ctx.supplierById.get(supplierId)!;
  return supplierOrderCost(supplier, supplierQtyMap(lines, supplierId, supplier)).total;
}

function totalCost(ctx: Ctx, lines: Lines): number {
  let total = 0;
  for (const supplierId of lines.keys()) total += supplierCost(ctx, lines, supplierId);
  return total;
}

function purchasedByMaterial(ctx: Ctx, lines: Lines): Map<string, number> {
  const purchased = new Map<string, number>();
  for (const [supplierId, byMaterial] of lines) {
    const supplier = ctx.supplierById.get(supplierId)!;
    for (const [materialId, packs] of byMaterial) {
      const item = supplier.items.find((it) => it.materialId === materialId)!;
      purchased.set(materialId, (purchased.get(materialId) ?? 0) + packs * (item.packSize ?? 1));
    }
  }
  return purchased;
}

function supplierTotalQty(ctx: Ctx, lines: Lines, supplierId: string): number {
  const supplier = ctx.supplierById.get(supplierId)!;
  let total = 0;
  const byMaterial = lines.get(supplierId);
  if (byMaterial) {
    for (const [materialId, packs] of byMaterial) {
      const item = supplier.items.find((it) => it.materialId === materialId)!;
      total += packs * (item.packSize ?? 1);
    }
  }
  return total;
}

/** 检查把 supplierId 的 materialId 调整为 newPacks 后是否仍满足硬约束（除需求下界外）。 */
function changeFeasible(
  ctx: Ctx,
  lines: Lines,
  supplierId: string,
  materialId: string,
  newPacks: number,
  purchased: Map<string, number>,
): boolean {
  const supplier = ctx.supplierById.get(supplierId)!;
  const item = supplier.items.find((it) => it.materialId === materialId)!;
  const pack = item.packSize ?? 1;
  const oldPacks = getPacks(lines, supplierId, materialId);
  const deltaQty = (newPacks - oldPacks) * pack;
  if (newPacks > 0) {
    const moq = item.moq ?? 0;
    if (newPacks * pack + EPS < moq) return false;
    if (item.maxQty != null && newPacks * pack > item.maxQty + EPS) return false;
  }
  const upper = ctx.upperByMaterial.get(materialId);
  if (upper != null && (purchased.get(materialId) ?? 0) + deltaQty > upper + EPS) return false;
  if (supplier.capacity != null) {
    if (supplierTotalQty(ctx, lines, supplierId) + deltaQty > supplier.capacity + EPS) return false;
  }
  if (oldPacks === 0 && newPacks > 0) {
    // 新启用一家供应商时检查家数上限
    const alreadyUsed = (lines.get(supplierId)?.size ?? 0) > 0;
    if (!alreadyUsed) {
      const usedCount = [...lines.values()].filter((m) => m.size > 0).length;
      if (usedCount >= ctx.maxSuppliers) return false;
    }
  }
  return true;
}

/** 需求是否全部满足（不低于需求，且不超过超购上限）。 */
function demandsSatisfied(ctx: Ctx, purchased: Map<string, number>): boolean {
  for (const [materialId, demand] of ctx.demandByMaterial) {
    const got = purchased.get(materialId) ?? 0;
    if (got + EPS < demand) return false;
    if (got > ctx.upperByMaterial.get(materialId)! + EPS) return false;
  }
  return true;
}

/**
 * 构造可行解：逐个物料补货，每次选择“整单成本增量”最小的供应渠道与包数。
 * 增量按真实整单成本（含阶梯折扣跳变与运费）计算，而非拆成单价。
 */
function construct(ctx: Ctx, rng: () => number): Lines | null {
  const lines: Lines = new Map();
  const purchased = new Map<string, number>();
  const materials = [...ctx.demandByMaterial.keys()].sort(() => rng() - 0.5);

  for (const materialId of materials) {
    const demand = ctx.demandByMaterial.get(materialId)!;
    let guard = 0;
    while ((purchased.get(materialId) ?? 0) + EPS < demand) {
      if (++guard > 500) return null;
      const remaining = demand - (purchased.get(materialId) ?? 0);
      let best: { supplierId: string; packs: number; delta: number } | null = null;
      for (const supplier of ctx.problem.suppliers) {
        const item = supplier.items.find((it) => it.materialId === materialId);
        if (!item) continue;
        const pack = item.packSize ?? 1;
        const moq = item.moq ?? 0;
        const minPacks = Math.max(1, Math.ceil((moq - EPS) / pack));
        const curPacks = getPacks(lines, supplier.id, materialId);
        const before = supplierCost(ctx, lines, supplier.id);
        const cover = Math.max(curPacks + Math.ceil((remaining - EPS) / pack), minPacks);
        for (const target of new Set([cover, cover + 1, curPacks + 1])) {
          if (target <= curPacks) continue;
          if (!changeFeasible(ctx, lines, supplier.id, materialId, target, purchased)) continue;
          setPacks(lines, supplier.id, materialId, target);
          const delta = supplierCost(ctx, lines, supplier.id) - before;
          setPacks(lines, supplier.id, materialId, curPacks);
          if (!best || delta < best.delta - EPS || (Math.abs(delta - best.delta) <= EPS && rng() < 0.5)) {
            best = { supplierId: supplier.id, packs: target, delta };
          }
        }
      }
      if (!best) return null;
      const supplier = ctx.supplierById.get(best.supplierId)!;
      const item = supplier.items.find((it) => it.materialId === materialId)!;
      const oldPacks = getPacks(lines, best.supplierId, materialId);
      setPacks(lines, best.supplierId, materialId, best.packs);
      purchased.set(materialId, (purchased.get(materialId) ?? 0) + (best.packs - oldPacks) * (item.packSize ?? 1));
    }
  }
  return lines;
}

/** 局部搜索：调整包数 / 删除采购行 / 同物料跨供应商转移，只接受整单成本下降的可行移动。 */
function descend(ctx: Ctx, start: Lines): Lines {
  let lines = cloneLines(start);
  let current = totalCost(ctx, lines);
  let purchased = purchasedByMaterial(ctx, lines);

  const tryChange = (supplierId: string, materialId: string, newPacks: number): boolean => {
    const oldPacks = getPacks(lines, supplierId, materialId);
    if (oldPacks === newPacks) return false;
    if (!changeFeasible(ctx, lines, supplierId, materialId, newPacks, purchased)) return false;
    const supplier = ctx.supplierById.get(supplierId)!;
    const item = supplier.items.find((it) => it.materialId === materialId)!;
    const pack = item.packSize ?? 1;
    const before = supplierCost(ctx, lines, supplierId);
    setPacks(lines, supplierId, materialId, newPacks);
    const deltaQty = (newPacks - oldPacks) * pack;
    purchased.set(materialId, (purchased.get(materialId) ?? 0) + deltaQty);
    if (!demandsSatisfied(ctx, purchased)) {
      purchased.set(materialId, (purchased.get(materialId) ?? 0) - deltaQty);
      setPacks(lines, supplierId, materialId, oldPacks);
      return false;
    }
    const after = supplierCost(ctx, lines, supplierId);
    if (after < before - 1e-9) {
      current += after - before;
      return true;
    }
    purchased.set(materialId, (purchased.get(materialId) ?? 0) - deltaQty);
    setPacks(lines, supplierId, materialId, oldPacks);
    return false;
  };

  // 移动 3：为某供应商凑单到下一阶梯门槛（阶梯跳变无法靠 ±1 包爬上去）
  const tryReachTier = (supplier: (typeof ctx.problem.suppliers)[number]): boolean => {
    const tiers = [...(supplier.discountTiers ?? [])].sort((a, b) => a.minSubtotal - b.minSubtotal);
    if (tiers.length === 0) return false;
    const qtyMap = supplierQtyMap(lines, supplier.id, supplier);
    const subtotal = supplierOrderCost(supplier, qtyMap).subtotal;
    const itemsByPrice = [...supplier.items].sort((a, b) => a.unitPrice - b.unitPrice);
    for (const tier of tiers) {
      if (tier.minSubtotal <= subtotal + EPS) continue;
      for (const item of itemsByPrice) {
        const pack = item.packSize ?? 1;
        const perPackValue = item.unitPrice * pack;
        if (perPackValue <= 0) continue;
        const addPacks = Math.max(1, Math.ceil((tier.minSubtotal - subtotal - EPS) / perPackValue));
        const curPacks = getPacks(lines, supplier.id, item.materialId);
        if (tryChange(supplier.id, item.materialId, curPacks + addPacks)) return true;
      }
    }
    return false;
  };

  let improved = true;
  let rounds = 0;
  while (improved && rounds++ < 200) {
    improved = false;
    // 移动 1：每行 ±1 包或删除
    for (const [supplierId, byMaterial] of [...lines]) {
      for (const [materialId, packs] of [...byMaterial]) {
        if (tryChange(supplierId, materialId, packs - 1)) improved = true;
        if (tryChange(supplierId, materialId, packs + 1)) improved = true;
        if (packs > 1 && tryChange(supplierId, materialId, 0)) improved = true;
      }
    }
    // 移动 2：同一物料的采购量（全部或部分）转移到另一家供应商，
    // 用于合并订单触发阶梯/省运费，或利用他家更便宜的单价与剩余产能
    for (const materialId of ctx.demandByMaterial.keys()) {
      const holders = [...lines.entries()].filter(([, m]) => (m.get(materialId) ?? 0) > 0);
      for (const [fromId] of holders) {
        const fromPacks = getPacks(lines, fromId, materialId);
        if (fromPacks <= 0) continue;
        const fromSupplier = ctx.supplierById.get(fromId)!;
        const fromItem = fromSupplier.items.find((it) => it.materialId === materialId)!;
        const fromPack = fromItem.packSize ?? 1;
        const fromMoq = fromItem.moq ?? 0;
        const fromMinPacks = Math.max(1, Math.ceil((fromMoq - EPS) / fromPack));
        for (const toSupplier of ctx.problem.suppliers) {
          if (toSupplier.id === fromId) continue;
          const toItem = toSupplier.items.find((it) => it.materialId === materialId);
          if (!toItem) continue;
          const toPack = toItem.packSize ?? 1;
          const toMinPacks = Math.max(1, Math.ceil(((toItem.moq ?? 0) - EPS) / toPack));
          const curTo = getPacks(lines, toSupplier.id, materialId);
          // 枚举转出后 from 剩余的包数：0（全转）或 [minPacks, fromPacks-1]（部分转）
          const remainOptions: number[] = [0];
          for (let r = Math.max(fromMinPacks, fromPacks - 50); r < fromPacks; r++) remainOptions.push(r);
          let applied = false;
          for (const remain of remainOptions) {
            if (remain >= fromPacks) continue;
            const qtyMoved = (fromPacks - remain) * fromPack;
            const target = Math.max(curTo + Math.ceil((qtyMoved - EPS) / toPack), toMinPacks);
            if (target <= curTo) continue;
            const beforeFrom = supplierCost(ctx, lines, fromId);
            const beforeTo = supplierCost(ctx, lines, toSupplier.id);
            if (!changeFeasible(ctx, lines, fromId, materialId, remain, purchased)) continue;
            setPacks(lines, fromId, materialId, remain);
            purchased.set(materialId, (purchased.get(materialId) ?? 0) - qtyMoved);
            let ok = changeFeasible(ctx, lines, toSupplier.id, materialId, target, purchased);
            if (ok) {
              setPacks(lines, toSupplier.id, materialId, target);
              purchased.set(materialId, (purchased.get(materialId) ?? 0) + (target - curTo) * toPack);
              ok = demandsSatisfied(ctx, purchased);
            }
            const after = supplierCost(ctx, lines, fromId) + supplierCost(ctx, lines, toSupplier.id);
            if (ok && after < beforeFrom + beforeTo - 1e-9) {
              current += after - beforeFrom - beforeTo;
              improved = true;
              applied = true;
              break;
            }
            // 回滚
            if (getPacks(lines, toSupplier.id, materialId) !== curTo) {
              purchased.set(materialId, (purchased.get(materialId) ?? 0) - (getPacks(lines, toSupplier.id, materialId) - curTo) * toPack);
              setPacks(lines, toSupplier.id, materialId, curTo);
            }
            purchased.set(materialId, (purchased.get(materialId) ?? 0) + qtyMoved);
            setPacks(lines, fromId, materialId, fromPacks);
          }
          if (applied) break;
        }
      }
    }
    // 移动 3：凑单触发整单阶梯
    for (const supplier of ctx.problem.suppliers) {
      if ((lines.get(supplier.id)?.size ?? 0) === 0) continue;
      if (tryReachTier(supplier)) improved = true;
    }
  }
  void current;
  return lines;
}

export interface HeuristicResult {
  lines: PurchaseLine[];
  cost: number;
}

/** 多起点启发式：构造 + 局部搜索，返回评估可行的最优解；找不到可行解返回 null。 */
export function solveHeuristic(problem: Problem, restarts = 12, seed = 42): HeuristicResult | null {
  const ctx = makeCtx(problem);
  let best: HeuristicResult | null = null;
  for (let r = 0; r < restarts; r++) {
    const rng = makeRng(seed + r * 9973);
    const constructed = construct(ctx, rng);
    if (!constructed) continue;
    const descended = descend(ctx, constructed);
    const list = linesToList(descended);
    const evaluation = evaluateSolution(problem, list);
    if (!evaluation.feasible) continue;
    if (!best || evaluation.totalCost < best.cost - 1e-9) {
      best = { lines: list, cost: evaluation.totalCost };
    }
  }
  return best;
}
