import type { ExactResult, Model, Solution, UncoveredReason } from "./types";

export function formatSolution(model: Model, s: Solution): string {
  const picks = model.ids.filter((_, i) => s.mask & (1 << i));
  const list = picks.length > 0 ? picks.join(", ") : "（空集）";
  return `选择 [${list}]，成本 ${s.cost}，覆盖 ${s.coveredPairs} 个二阶交互`;
}

export function formatReason(r: UncoveredReason): string {
  switch (r.kind) {
    case "conflict":
      return `不可达 — ${r.detail}`;
    case "budget":
      return `不可达 — 同时执行的最小成本 ${r.minCost} 超过预算 ${r.budget}`;
    case "sacrifice":
      return `可达但未覆盖 — 覆盖该对最多只能覆盖 ${r.bestWithPair} 个交互，需牺牲 ${r.optimal - r.bestWithPair} 个（最优 ${r.optimal} 个）`;
    case "costlier":
      return `可达但未覆盖 — 同等覆盖下最低成本 ${r.costWithPair} 高于最优方案成本 ${r.optimalCost}`;
  }
}

export function formatReport(model: Model, budget: number, exact: ExactResult, greedy: Solution): string {
  const lines: string[] = [];
  lines.push(`== 改动交互测试规划（${model.n} 个改动，预算 ${budget}）==`);
  lines.push(`二阶交互总数 ${exact.totalPairs}，其中可达 ${exact.reachablePairs}，不可达 ${exact.totalPairs - exact.reachablePairs}`);
  lines.push("");
  lines.push(`[精确解] ${formatSolution(model, exact.optimal)}`);
  lines.push(`[贪心解] ${formatSolution(model, greedy)}`);

  const covGap = exact.optimal.coveredPairs - greedy.coveredPairs;
  const costGap = greedy.cost - exact.optimal.cost;
  const pct = exact.optimal.coveredPairs > 0 ? ((covGap / exact.optimal.coveredPairs) * 100).toFixed(1) : "0.0";
  lines.push(
    `[差距] 覆盖差 ${covGap} 个（${pct}%），成本差 ${costGap >= 0 ? "+" : ""}${costGap}（贪心 - 精确）`,
  );
  lines.push("");

  if (exact.uncovered.length === 0) {
    lines.push("未覆盖项：无（全部可达交互均已覆盖）");
  } else {
    lines.push(`未覆盖项（${exact.uncovered.length} 个）：`);
    for (const u of exact.uncovered) {
      lines.push(`  - (${u.a}, ${u.b})：${formatReason(u.reason)}`);
    }
  }
  return lines.join("\n");
}
