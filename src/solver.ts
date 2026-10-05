import type {
  DiscountTier,
  MaterialSpec,
  Scenario,
  SupplierItem,
  SupplierSpec,
} from './types.ts';

const EPS = 1e-9;

export interface PurchaseLine {
  materialId: string;
  qty: number;
  unitPrice: number;
  amount: number;
}

export interface SupplierPurchase {
  supplierId: string;
  supplierName: string;
  lines: PurchaseLine[];
  /** 折前小计 */
  subtotal: number;
  /** 命中的阶梯（无折扣为 null） */
  tier: DiscountTier | null;
  /** 阶梯触发依据（人可读） */
  tierReason: string;
  discountAmount: number;
  freight: number;
  total: number;
}

export interface MaterialFulfillment {
  materialId: string;
  demand: number;
  purchased: number;
  overbuy: number;
  overbuyLimit: number;
}

export interface Plan {
  feasible: boolean;
  infeasibleReason?: string;
  totalCost: number;
  purchases: SupplierPurchase[];
  fulfillment: MaterialFulfillment[];
  exploredStates: number;
}

interface SupplierModel {
  spec: SupplierSpec;
  name: string;
  freight: number;
  freeFreightMinSubtotal: number;
  maxTotalQty: number;
  tiers: DiscountTier[];
  maxRate: number;
  /** 与 material 下标对齐；null 表示该供应商不供此物料 */
  items: (SupplierItem | null)[];
}

/** 命中阶梯：折前小计对应的最高档 */
export function matchTier(tiers: DiscountTier[], subtotal: number): DiscountTier | null {
  let hit: DiscountTier | null = null;
  for (const t of tiers) {
    if (subtotal + EPS >= t.minSubtotal && (!hit || t.minSubtotal > hit.minSubtotal)) hit = t;
  }
  return hit;
}

/** 单个供应商一个采购向量（qty 与 material 下标对齐）的整单成本：阶梯折扣作用于整单小计，不拆单价 */
export function vectorCost(sup: SupplierModel, qty: number[]): number {
  let subtotal = 0;
  for (let m = 0; m < qty.length; m++) {
    const item = sup.items[m];
    if (item && qty[m] > 0) subtotal += item.unitPrice * qty[m];
  }
  if (subtotal <= EPS) return 0;
  const tier = matchTier(sup.tiers, subtotal);
  const discounted = tier ? subtotal * (1 - tier.discountPct / 100) : subtotal;
  const freight = subtotal + EPS >= sup.freeFreightMinSubtotal ? 0 : sup.freight;
  return discounted + freight;
}

/** 某物料在剩余可购空间 room 下的合法采购量集合（含 0） */
function qtyOptions(item: SupplierItem | null, room: number): number[] {
  if (!item || room <= 0) return [0];
  const pack = Math.max(1, Math.floor(item.packMultiple ?? 1));
  const moq = Math.max(0, Math.floor(item.moq ?? 0));
  const cap = item.maxQty === undefined ? room : Math.min(room, Math.floor(item.maxQty));
  const maxQ = Math.floor(cap / pack) * pack;
  if (maxQ <= 0) return [0];
  const qMin = moq <= 0 ? pack : Math.max(pack, Math.ceil(moq / pack) * pack);
  if (qMin > maxQ) return [0];
  const opts = [0];
  for (let q = qMin; q <= maxQ; q += pack) opts.push(q);
  return opts;
}

function buildTierReason(sup: SupplierModel, subtotal: number, tier: DiscountTier | null): string {
  const money = (v: number): string => `¥${v.toFixed(2)}`;
  const sorted = [...sup.tiers].sort((a, b) => a.minSubtotal - b.minSubtotal);
  if (sorted.length === 0) return '该供应商无阶梯折扣';
  if (!tier) {
    const first = sorted[0];
    return `折前小计 ${money(subtotal)} 未达首档门槛 ${money(first.minSubtotal)}` +
      `（差 ${money(first.minSubtotal - subtotal)}），本单无折扣`;
  }
  const idx = sorted.findIndex((t) => t.minSubtotal === tier.minSubtotal);
  const saved = subtotal * (tier.discountPct / 100);
  let reason = `折前小计 ${money(subtotal)} ≥ 门槛 ${money(tier.minSubtotal)}，` +
    `触发第 ${idx + 1} 档整单 ${tier.discountPct}% 折扣（省 ${money(saved)}）`;
  const next = sorted[idx + 1];
  if (next) {
    reason += `；距下一档（满 ${money(next.minSubtotal)} 享 ${next.discountPct}%）还差 ${money(next.minSubtotal - subtotal)}`;
  } else {
    reason += '；已是最高档';
  }
  return reason;
}

interface FrontierEntry {
  g: number;
  parentKey: string;
  qty: number[] | null; // 在进入该状态的那家供应商的采购向量；起始为 null
}

interface Candidate {
  g: number;
  level: number; // 最后采购的供应商下标
  parentKey: string;
  qty: number[];
}

/**
 * 精确求解最低含运费采购方案。
 *
 * 方法：按供应商逐层做动态规划，状态为「各物料已购量向量」，只按
 * （状态 -> 最低成本）精确去重，不做任何有上限场景下不安全的支配剪枝；
 * 每个供应商的整单成本按其折前小计命中阶梯后一次性计算，折扣不拆成
 * 独立单价、不做贪心分配。配合贪心 incumbent + 可采纳下界剪枝控制规模，
 * 结果为全局最优。
 */
export function solveProcurement(scenario: Scenario, maxExpansions = 5_000_000): Plan {
  const materials: MaterialSpec[] = scenario.materials;
  const M = materials.length;
  const S = scenario.suppliers.length;
  if (S > 6) throw new Error(`供应商数量 ${S} 超过上限 6`);
  const ratio = scenario.maxOverbuyRatio ?? 0;
  const demands = materials.map((m) => Math.floor(m.demand));
  const overLimits = materials.map((m) =>
    Math.floor(m.maxOverbuy ?? Math.ceil(m.demand * ratio)));
  const limits = demands.map((d, i) => d + overLimits[i]);

  const sups: SupplierModel[] = scenario.suppliers.map((spec) => {
    const items: (SupplierItem | null)[] = new Array(M).fill(null);
    for (const it of spec.items) {
      const idx = materials.findIndex((m) => m.id === it.materialId);
      if (idx >= 0) items[idx] = it;
    }
    const tiers = [...(spec.tiers ?? [])].sort((a, b) => a.minSubtotal - b.minSubtotal);
    return {
      spec,
      name: spec.name ?? spec.id,
      freight: spec.freight ?? 0,
      freeFreightMinSubtotal: spec.freeFreightMinSubtotal ?? Number.POSITIVE_INFINITY,
      maxTotalQty: spec.maxTotalQty ?? Number.POSITIVE_INFINITY,
      tiers,
      maxRate: tiers.reduce((r, t) => Math.max(r, t.discountPct / 100), 0),
      items,
    };
  });

  // 可行性预检 + 下界用的最低有效单价
  const minEffPrice = new Array<number>(M).fill(Number.POSITIVE_INFINITY);
  const maxSupply = new Array<number>(M).fill(0);
  for (const sup of sups) {
    for (let m = 0; m < M; m++) {
      const item = sup.items[m];
      if (!item) continue;
      minEffPrice[m] = Math.min(minEffPrice[m], item.unitPrice * (1 - sup.maxRate));
      const pack = Math.max(1, Math.floor(item.packMultiple ?? 1));
      const cap = item.maxQty === undefined ? limits[m] : Math.min(limits[m], item.maxQty);
      maxSupply[m] += Math.floor(cap / pack) * pack;
    }
  }
  for (let m = 0; m < M; m++) {
    if (!Number.isFinite(minEffPrice[m]) && demands[m] > 0) {
      return fail(`物料 ${materials[m].id} 没有任何供应商供货`);
    }
    if (maxSupply[m] < demands[m]) {
      return fail(
        `物料 ${materials[m].id} 需求 ${demands[m]}，但所有供应商交付上限合计仅 ${maxSupply[m]}`,
      );
    }
  }

  const covers = (vec: number[]): boolean => {
    for (let m = 0; m < M; m++) if (vec[m] < demands[m]) return false;
    return true;
  };
  // 后缀最低有效单价：suffixPrice[s][m] = 第 s..S-1 家供应商中物料 m 的最低可能有效单价
  const suffixPrice: number[][] = new Array(S + 1);
  suffixPrice[S] = new Array(M).fill(Number.POSITIVE_INFINITY);
  for (let s = S - 1; s >= 0; s--) {
    suffixPrice[s] = suffixPrice[s + 1].slice();
    for (let m = 0; m < M; m++) {
      const item = sups[s].items[m];
      if (item) {
        suffixPrice[s][m] = Math.min(suffixPrice[s][m], item.unitPrice * (1 - sups[s].maxRate));
      }
    }
  }
  /** 可采纳下界：剩余需求 × 剩余供应商的各物料最低可能有效单价（忽略起订量/整包/运费/门槛） */
  const lowerBound = (vec: number[], fromSup: number): number => {
    let h = 0;
    for (let m = 0; m < M; m++) {
      const rem = demands[m] - vec[m];
      if (rem > 0) {
        const p = suffixPrice[fromSup][m];
        if (!Number.isFinite(p)) return Number.POSITIVE_INFINITY; // 后续无人能供，死路
        h += rem * p;
      }
    }
    return h;
  };

  /** 贪心 incumbent：每种物料按有效单价升序尝试供应商，求一个可行上界与对应采购向量 */
  const greedyIncumbent = (): { total: number; chosen: number[][] | null } => {
    const bought = new Array(M).fill(0);
    const chosen: number[][] = sups.map(() => new Array(M).fill(0));
    const totalQty = new Array(S).fill(0);
    for (let m = 0; m < M; m++) {
      let need = demands[m];
      // 按有效单价升序尝试
      const order = sups
        .map((sup, s) => ({ sup, s, item: sup.items[m] }))
        .filter((x) => x.item !== null)
        .sort((a, b) =>
          a.item!.unitPrice * (1 - a.sup.maxRate) - b.item!.unitPrice * (1 - b.sup.maxRate));
      for (const { sup, s, item } of order) {
        if (need <= 0) break;
        const pack = Math.max(1, Math.floor(item!.packMultiple ?? 1));
        const moq = Math.max(0, Math.floor(item!.moq ?? 0));
        const roomTotal = sup.maxTotalQty === Number.POSITIVE_INFINITY
          ? Number.POSITIVE_INFINITY
          : sup.maxTotalQty - totalQty[s];
        let cap = Math.min(limits[m] - bought[m], roomTotal);
        if (item!.maxQty !== undefined) cap = Math.min(cap, item!.maxQty);
        cap = Math.floor(cap / pack) * pack;
        const qMin = moq <= 0 ? pack : Math.max(pack, Math.ceil(moq / pack) * pack);
        if (cap < qMin) continue; // 剩余额度连起订量都满足不了
        const want = Math.max(Math.ceil(need / pack) * pack, qMin);
        const q = Math.min(want, cap);
        if (q <= 0) continue;
        chosen[s][m] += q;
        totalQty[s] += q;
        bought[m] += q;
        need -= q;
      }
      if (need > 0) return { total: Number.POSITIVE_INFINITY, chosen: null }; // 贪心失败，交给精确搜索
    }
    let total = 0;
    for (let s = 0; s < S; s++) total += vectorCost(sups[s], chosen[s]);
    return { total, chosen };
  };

  const greedy = greedyIncumbent();
  let incumbent = greedy.total;
  let best: Candidate | null = null;
  let explored = 0;

  const zeroKey = new Array(M).fill(0).join(',');
  let frontier = new Map<string, FrontierEntry>([[zeroKey, { g: 0, parentKey: '', qty: null }]]);
  const levels: Map<string, FrontierEntry>[] = [frontier];

  for (let s = 0; s < S; s++) {
    const sup = sups[s];
    const next = new Map<string, FrontierEntry>();
    for (const [key, entry] of frontier) {
      const baseVec = key.split(',').map(Number);
      if (entry.g + lowerBound(baseVec, s) >= incumbent - EPS) continue;
      // 该状态下该供应商的各物料可选采购量
      const perMat: number[][] = new Array(M);
      for (let m = 0; m < M; m++) {
        perMat[m] = qtyOptions(sup.items[m], limits[m] - baseVec[m]);
      }
      const qty = new Array(M).fill(0);
      const child = new Array(M).fill(0);
      // 该供应商对各物料最多还能提供的数量（用于下界估计）
      const maxAdd = perMat.map((opts) => opts[opts.length - 1]);
      // 递归枚举该供应商的采购向量；用 incumbent 做下界剪枝
      const walk = (m: number, subtotal: number, totalQty: number): void => {
        // 可采纳下界：本单已选小计按最大折扣折算（运费 ≥ 0，后续小计只会更多），
        // 加上「假设本供应商剩余物料都按最大量买」后仍缺的需求 × 最低有效单价
        let lb = 0;
        for (let i = 0; i < M; i++) {
          const covered = baseVec[i] + (i < m ? qty[i] : maxAdd[i]);
          const rem = demands[i] - covered;
          if (rem > 0) {
            const p = suffixPrice[s + 1][i];
            if (!Number.isFinite(p)) { lb = Number.POSITIVE_INFINITY; break; }
            lb += rem * p;
          }
        }
        if (entry.g + subtotal * (1 - sup.maxRate) + lb >= incumbent - EPS) return;
        if (m === M) {
          if (++explored > maxExpansions) {
            throw new Error(`搜索状态超过 ${maxExpansions}，请收紧超购上限或增大整包倍数`);
          }
          const cost = vectorCost(sup, qty);
          const g2 = entry.g + cost;
          for (let i = 0; i < M; i++) child[i] = baseVec[i] + qty[i];
          if (g2 + lowerBound(child, s + 1) >= incumbent - EPS) return;
          if (covers(child)) {
            incumbent = g2;
            best = { g: g2, level: s, parentKey: key, qty: qty.slice() };
            return;
          }
          const ck = child.join(',');
          const prev = next.get(ck);
          if (prev === undefined || g2 < prev.g - EPS) {
            next.set(ck, { g: g2, parentKey: key, qty: qty.slice() });
          }
          return;
        }
        for (const q of perMat[m]) {
          if (totalQty + q > sup.maxTotalQty + EPS) break; // 整单交付上限
          qty[m] = q;
          const item = sup.items[m];
          const add = item && q > 0 ? item.unitPrice * q : 0;
          walk(m + 1, subtotal + add, totalQty + q);
        }
        qty[m] = 0;
      };
      walk(0, 0, 0);
    }
    frontier = next;
    levels.push(frontier);
    if (process.env.DEBUG_DP) {
      console.error(`level ${s} (${sup.name}): frontier=${frontier.size} explored=${explored} incumbent=${incumbent}`);
    }
    if (frontier.size === 0) break;
  }

  let chosen: (number[] | null)[];
  if (best) {
    // 回溯每个供应商的采购向量
    chosen = new Array(S).fill(null);
    let cursor: Candidate = best;
    for (;;) {
      chosen[cursor.level] = cursor.qty;
      const parentEntry = levels[cursor.level].get(cursor.parentKey);
      if (!parentEntry || parentEntry.qty === null) break;
      cursor = { g: 0, level: cursor.level - 1, parentKey: parentEntry.parentKey, qty: parentEntry.qty };
    }
  } else if (greedy.chosen) {
    // 精确搜索未找到严格优于贪心 incumbent 的方案，贪心解即最优
    chosen = greedy.chosen;
  } else {
    return fail('在当前起订量/整包/交付上限/超购上限约束下无可行采购方案');
  }

  const purchases: SupplierPurchase[] = [];
  const purchased = new Array(M).fill(0);
  let totalCost = 0;
  for (let s = 0; s < S; s++) {
    const qty = chosen[s];
    if (!qty) continue;
    const sup = sups[s];
    const lines: PurchaseLine[] = [];
    let subtotal = 0;
    for (let m = 0; m < M; m++) {
      if (qty[m] > 0) {
        const item = sup.items[m]!;
        const amount = item.unitPrice * qty[m];
        lines.push({ materialId: materials[m].id, qty: qty[m], unitPrice: item.unitPrice, amount });
        subtotal += amount;
        purchased[m] += qty[m];
      }
    }
    if (lines.length === 0) continue;
    const tier = matchTier(sup.tiers, subtotal);
    const discountAmount = tier ? subtotal * (tier.discountPct / 100) : 0;
    const freight = subtotal + EPS >= sup.freeFreightMinSubtotal ? 0 : sup.freight;
    purchases.push({
      supplierId: sup.spec.id,
      supplierName: sup.name,
      lines,
      subtotal,
      tier,
      tierReason: buildTierReason(sup, subtotal, tier),
      discountAmount,
      freight,
      total: subtotal - discountAmount + freight,
    });
    totalCost += subtotal - discountAmount + freight;
  }

  return {
    feasible: true,
    totalCost,
    purchases,
    fulfillment: materials.map((m, i) => ({
      materialId: m.id,
      demand: demands[i],
      purchased: purchased[i],
      overbuy: purchased[i] - demands[i],
      overbuyLimit: limits[i],
    })),
    exploredStates: explored,
  };

  function fail(reason: string): Plan {
    return {
      feasible: false, infeasibleReason: reason, totalCost: 0,
      purchases: [], fulfillment: [], exploredStates: 0,
    };
  }
}
