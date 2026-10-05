import { matchTier, solveProcurement } from './solver.ts';
import type { Scenario, SupplierSpec } from './types.ts';

const EPS = 1e-6;

/** 独立实现的暴力枚举：作为最优性对照 */
function bruteForce(sc: Scenario): number | null {
  const ratio = sc.maxOverbuyRatio ?? 0;
  const demands = sc.materials.map((m) => Math.floor(m.demand));
  const limits = sc.materials.map((m) =>
    Math.floor(m.demand) + Math.floor(m.maxOverbuy ?? Math.ceil(m.demand * ratio)));
  const M = demands.length;
  const S = sc.suppliers.length;
  const idxOf = new Map(sc.materials.map((m, i) => [m.id, i]));

  function options(spec: SupplierSpec, m: number, room: number): number[] {
    const it = spec.items.find((x) => idxOf.get(x.materialId) === m);
    if (!it || room <= 0) return [0];
    const pack = Math.max(1, Math.floor(it.packMultiple ?? 1));
    const moq = Math.max(0, Math.floor(it.moq ?? 0));
    const cap = it.maxQty === undefined ? room : Math.min(room, Math.floor(it.maxQty));
    const maxQ = Math.floor(cap / pack) * pack;
    const qMin = moq <= 0 ? pack : Math.max(pack, Math.ceil(moq / pack) * pack);
    if (qMin > maxQ) return [0];
    const opts = [0];
    for (let q = qMin; q <= maxQ; q += pack) opts.push(q);
    return opts;
  }

  function costOf(spec: SupplierSpec, qty: number[]): number {
    let sub = 0;
    for (const it of spec.items) {
      const m = idxOf.get(it.materialId);
      if (m !== undefined) sub += it.unitPrice * qty[m];
    }
    if (sub <= 0) return 0;
    const tier = matchTier([...(spec.tiers ?? [])].sort((a, b) => a.minSubtotal - b.minSubtotal), sub);
    const disc = tier ? sub * (1 - tier.discountPct / 100) : sub;
    const free = spec.freeFreightMinSubtotal ?? Number.POSITIVE_INFINITY;
    return disc + (sub >= free ? 0 : spec.freight ?? 0);
  }

  let best: number | null = null;
  let budget = 3_000_000;
  const fulfilled = new Array(M).fill(0);

  function walkSup(s: number, acc: number): void {
    if (budget-- <= 0) return;
    if (best !== null && acc >= best - EPS) return;
    if (s === S) {
      for (let m = 0; m < M; m++) if (fulfilled[m] < demands[m]) return;
      if (best === null || acc < best - EPS) best = acc;
      return;
    }
    const spec = sc.suppliers[s];
    const qty = new Array(M).fill(0);
    const perMat: number[][] = [];
    for (let m = 0; m < M; m++) perMat.push(options(spec, m, limits[m] - fulfilled[m]));
    const maxTotal = spec.maxTotalQty ?? Number.POSITIVE_INFINITY;
    (function walkMat(m: number, totalQty: number): void {
      if (m === M) {
        const vec = qty.slice();
        for (let i = 0; i < M; i++) fulfilled[i] += vec[i];
        walkSup(s + 1, acc + costOf(spec, vec));
        for (let i = 0; i < M; i++) fulfilled[i] -= vec[i];
        return;
      }
      for (const q of perMat[m]) {
        if (totalQty + q > maxTotal) break;
        qty[m] = q;
        walkMat(m + 1, totalQty + q);
      }
      qty[m] = 0;
    })(0, 0);
  }
  walkSup(0, 0);
  if (budget <= 0) return undefined; // 预算耗尽：跳过该用例
  return best;
}

// 确定性伪随机
let seed = 42;
function rnd(): number {
  seed = (seed * 1103515245 + 12345) % 2147483648;
  return seed / 2147483648;
}
const ri = (lo: number, hi: number): number => lo + Math.floor(rnd() * (hi - lo + 1));

function randomScenario(): Scenario {
  const M = ri(2, 3);
  const materials = Array.from({ length: M }, (_, i) => ({
    id: `M${i}`,
    demand: ri(5, 20),
    ...(rnd() < 0.5 ? { maxOverbuy: ri(0, 4) } : {}),
  }));
  const S = ri(2, 3);
  const suppliers = Array.from({ length: S }, (_, s) => {
    const items = materials
      .filter(() => rnd() < 0.75)
      .map((m) => ({
        materialId: m.id,
        unitPrice: ri(1, 20) + 0.5,
        moq: ri(0, 3) === 0 ? 0 : ri(1, 8),
        packMultiple: ri(1, 4),
        ...(rnd() < 0.4 ? { maxQty: ri(8, 30) } : {}),
      }));
    const tierCount = ri(0, 3);
    let th = ri(50, 200);
    const tiers = Array.from({ length: tierCount }, () => {
      const t = { minSubtotal: th, discountPct: ri(2, 15) };
      th += ri(50, 200);
      return t;
    });
    return {
      id: `S${s}`,
      freight: ri(0, 40),
      ...(rnd() < 0.3 ? { freeFreightMinSubtotal: ri(100, 400) } : {}),
      ...(rnd() < 0.3 ? { maxTotalQty: ri(20, 60) } : {}),
      items,
      tiers,
    };
  });
  // 保证每种物料至少一家供货
  for (const m of materials) {
    if (!suppliers.some((s) => s.items.some((it) => it.materialId === m.id))) {
      suppliers[0].items.push({ materialId: m.id, unitPrice: 25, packMultiple: 1, moq: 0 });
    }
  }
  return { maxOverbuyRatio: 0.2, materials, suppliers };
}

let pass = 0;
let infeasibleCount = 0;
let skipped = 0;
const N = 200;
for (let t = 0; t < N; t++) {
  const sc = randomScenario();
  const plan = solveProcurement(sc);
  const bf = bruteForce(sc);
  if (bf === undefined) { skipped++; continue; } // 暴力预算耗尽，跳过
  if (bf === null) {
    if (plan.feasible) {
      console.error(`用例 ${t}: 暴力无解但求解器给出了 ${plan.totalCost}`);
      console.error(JSON.stringify(sc, null, 2));
      process.exit(1);
    }
    infeasibleCount++;
  } else {
    if (!plan.feasible) {
      console.error(`用例 ${t}: 暴力最优 ${bf} 但求解器判不可行: ${plan.infeasibleReason}`);
      process.exit(1);
    }
    if (Math.abs(plan.totalCost - bf) > EPS) {
      console.error(`用例 ${t}: 求解器 ${plan.totalCost} != 暴力最优 ${bf}`);
      console.error(JSON.stringify(sc, null, 2));
      process.exit(1);
    }
  }
  pass++;
}
console.log(`全部通过：${pass}/${N}（不可行用例 ${infeasibleCount} 个双方判定一致，暴力超时跳过 ${skipped} 个）`);
