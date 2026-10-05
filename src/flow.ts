import type { Problem } from './types.ts';
import type { SolveResult } from './solver.ts';

/** 一条备件流水：从来源到用途 */
export interface FlowRecord {
  part: string;
  /** 来源：'库存' 或 拆解设备 ID */
  from: string;
  /** 用途：修复设备 ID */
  to: string;
  qty: number;
}

export interface PartBalance {
  part: string;
  stock: number;
  salvaged: number;
  /** 回收损耗 = 拆解标称量 * (1 - 回收率) */
  lost: number;
  consumed: number;
  leftover: number;
}

export interface FlowResult {
  flows: FlowRecord[];
  balance: PartBalance[];
}

/**
 * 根据求解结果生成备件流水（来源 -> 用途）。
 * 分配规则：先消耗库存，再按设备 ID 顺序消耗各拆解设备的回收件。
 */
export function buildFlows(problem: Problem, result: SolveResult): FlowResult {
  const yieldOf = (p: string): number => problem.partYield?.[p] ?? problem.recoveryYield;
  const byId = new Map(problem.devices.map((d) => [d.id, d] as const));
  const repairedSet = new Set(result.repaired);
  const dismantledSet = new Set(result.dismantled);

  const parts = new Set<string>();
  for (const p of Object.keys(problem.stock)) parts.add(p);
  for (const d of problem.devices) {
    for (const p of Object.keys(d.repairParts)) parts.add(p);
    for (const p of Object.keys(d.salvageParts)) parts.add(p);
  }

  const flows: FlowRecord[] = [];
  const balance: PartBalance[] = [];

  for (const part of [...parts].sort()) {
    // 供给池：库存 + 各拆解设备的有效回收（保持设备输入顺序，确定性分配）
    const pool: Array<{ from: string; left: number }> = [];
    const stockQty = problem.stock[part] ?? 0;
    if (stockQty > 0) pool.push({ from: '库存', left: stockQty });

    let salvaged = 0;
    let lost = 0;
    for (const d of problem.devices) {
      if (!dismantledSet.has(d.id)) continue;
      const nominal = d.salvageParts[part] ?? 0;
      if (nominal <= 0) continue;
      const gained = Math.floor(nominal * yieldOf(part) + 1e-9);
      salvaged += gained;
      lost += nominal - gained;
      if (gained > 0) pool.push({ from: d.id, left: gained });
    }

    // 用途：各修复设备的齐套需求（按设备输入顺序消耗）
    let consumed = 0;
    for (const d of problem.devices) {
      if (!repairedSet.has(d.id)) continue;
      let need = d.repairParts[part] ?? 0;
      while (need > 0) {
        const src = pool.find((s) => s.left > 0);
        if (!src) {
          throw new Error(`备件 ${part} 不足以修复 ${d.id}（求解结果不可行）`);
        }
        const take = Math.min(need, src.left);
        src.left -= take;
        need -= take;
        consumed += take;
        flows.push({ part, from: src.from, to: d.id, qty: take });
      }
    }

    const leftover = pool.reduce((s, x) => s + x.left, 0);
    balance.push({ part, stock: stockQty, salvaged, lost, consumed, leftover });
  }

  // 未使用设备不应出现在流水里（防御性检查，byId 保留给将来扩展）
  void byId;
  return { flows, balance };
}
