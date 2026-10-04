import type { Decision, Flow, Problem } from "./types.ts";

const STOCK = "库存";
const PURCHASE = "采购";

/**
 * 由最优决策重建“来源 -> 用途”的备件流水。
 * 分配顺序（确定性）：先用库存，再用拆解回收（按设备输入顺序），最后外购。
 * 由于修复与拆解互斥，设备间流向是 拆解机 -> 修复机 的二部图，天然无环，
 * 即“不能循环产料”：回收备件不会再次进入拆解环节。
 */
export function buildFlows(
  problem: Problem,
  decisions: Record<string, Decision>,
  purchases: Record<string, number>,
): { flows: Flow[]; leftoverRecovered: Flow[] } {
  const globalRate = problem.recoveryRate ?? 1;
  const flows: Flow[] = [];

  // 各来源的可用量。
  const stockLeft = new Map<string, number>();
  for (const [part, spec] of Object.entries(problem.parts)) {
    stockLeft.set(part, (stockLeft.get(part) ?? 0) + spec.stock);
  }
  const donorLeft = new Map<string, Map<string, number>>(); // 拆解机 -> 备件 -> 余量
  for (const d of problem.devices) {
    if (decisions[d.id] !== "dismantle") continue;
    const rate = d.recoveryRate ?? globalRate;
    const bag = new Map<string, number>();
    for (const [part, qty] of Object.entries(d.contains)) {
      const got = Math.floor(qty * rate);
      if (got > 0) bag.set(part, got);
    }
    donorLeft.set(d.id, bag);
  }

  // 逐台修复设备、逐个备件分配来源。
  for (const d of problem.devices) {
    if (decisions[d.id] !== "repair") continue;
    for (const [part, rawQty] of Object.entries(d.needs)) {
      let remain = rawQty;
      const fromStock = Math.min(stockLeft.get(part) ?? 0, remain);
      if (fromStock > 0) {
        flows.push({ from: STOCK, to: d.id, part, qty: fromStock });
        stockLeft.set(part, (stockLeft.get(part) ?? 0) - fromStock);
        remain -= fromStock;
      }
      if (remain > 0) {
        for (const [donor, bag] of donorLeft) {
          const got = Math.min(bag.get(part) ?? 0, remain);
          if (got > 0) {
            flows.push({ from: donor, to: d.id, part, qty: got });
            bag.set(part, (bag.get(part) ?? 0) - got);
            remain -= got;
            if (remain === 0) break;
          }
        }
      }
      if (remain > 0) {
        flows.push({ from: PURCHASE, to: d.id, part, qty: remain });
      }
    }
  }

  // 校验：外购量与求解器口径一致。
  const purchasedUsed = new Map<string, number>();
  for (const f of flows) {
    if (f.from === PURCHASE) purchasedUsed.set(f.part, (purchasedUsed.get(f.part) ?? 0) + f.qty);
  }
  for (const [part, qty] of Object.entries(purchases)) {
    if ((purchasedUsed.get(part) ?? 0) !== qty) {
      throw new Error(`内部不一致：备件 ${part} 采购量对账失败`);
    }
  }

  // 无环校验（拆解机 -> 修复机 二部图，拓扑排序应成功）。
  const edges = new Map<string, string[]>();
  for (const f of flows) {
    if (f.from === STOCK || f.from === PURCHASE) continue;
    (edges.get(f.from) ?? edges.set(f.from, []).get(f.from)!).push(f.to);
  }
  const indeg = new Map<string, number>();
  for (const [from, tos] of edges) {
    indeg.set(from, indeg.get(from) ?? 0);
    for (const to of tos) indeg.set(to, (indeg.get(to) ?? 0) + 1);
  }
  const queue = [...indeg.entries()].filter(([, d]) => d === 0).map(([id]) => id);
  let visited = 0;
  while (queue.length > 0) {
    const cur = queue.pop()!;
    visited++;
    for (const to of edges.get(cur) ?? []) {
      const d = (indeg.get(to) ?? 0) - 1;
      indeg.set(to, d);
      if (d === 0) queue.push(to);
    }
  }
  if (visited !== indeg.size) throw new Error("备件流水存在循环，违反“不能循环产料”约束");

  // 拆解后未被使用的回收备件（损耗剩余，报废）。
  const leftoverRecovered: Flow[] = [];
  for (const [donor, bag] of donorLeft) {
    for (const [part, qty] of bag) {
      if (qty > 0) leftoverRecovered.push({ from: donor, to: "报废", part, qty });
    }
  }

  return { flows, leftoverRecovered };
}
