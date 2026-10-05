import type { Analysis, SolutionInfo } from "./types.ts";

function formatSolution(title: string, solution: SolutionInfo): string[] {
  const lines: string[] = [];
  lines.push(`${title}（共 ${solution.combos.length} 个组合，总成本 ${solution.totalCost}）:`);
  solution.combos.forEach((combo, i) => {
    lines.push(
      `  ${i + 1}. [${combo.changes.join(", ")}]  成本 ${combo.cost}，覆盖交互 ${combo.pairs} 对`,
    );
  });
  return lines;
}

export function formatReport(analysis: Analysis): string {
  const lines: string[] = [];
  lines.push("========== 改动交互测试规划报告 ==========");
  lines.push(`改动数量: ${analysis.changeCount}（上限 18）`);
  lines.push(`执行预算: ${analysis.budget}（单个组合成本上限）`);
  lines.push(`合法组合数: ${analysis.legalComboCount}`);
  lines.push(
    `二阶交互: 可达 ${analysis.reachablePairs} / 共 ${analysis.totalPairs} 对`,
  );
  if (analysis.unselectable.length > 0) {
    lines.push(
      `不可选改动（自身依赖闭包超预算）: ${analysis.unselectable.join(", ")}`,
    );
  }
  lines.push("");
  lines.push("---------- 精确解（最大覆盖 + 最少成本） ----------");
  lines.push(...formatSolution("最优组合", analysis.exact));
  lines.push("");
  lines.push("---------- 贪心解（对照） ----------");
  lines.push(...formatSolution("贪心组合", analysis.greedy));
  lines.push("");
  lines.push("---------- 精确 vs 贪心 ----------");
  lines.push(`精确解总成本: ${analysis.exact.totalCost}`);
  lines.push(`贪心解总成本: ${analysis.greedy.totalCost}`);
  lines.push(
    `差距: ${analysis.gap.absolute}（${analysis.gap.percent.toFixed(2)}%）`,
  );
  lines.push("");
  lines.push("---------- 未覆盖项 ----------");
  if (analysis.uncovered.length === 0) {
    lines.push("全部二阶交互均已覆盖，无未覆盖项。");
  } else {
    lines.push(`共 ${analysis.uncovered.length} 对二阶交互不可达：`);
    for (const pair of analysis.uncovered) {
      lines.push(`  - (${pair.a}, ${pair.b}): ${pair.reason}`);
    }
  }
  return lines.join("\n");
}
