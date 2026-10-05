import { Problem, Solution } from './types';

const fmt = (v: number): string => v.toFixed(2);

/** 把求解结果格式化为人类可读的时间线 */
export function formatSolution(problem: Problem, sol: Solution): string {
  const ids = problem.stations.map((s) => s.id);
  const lines: string[] = [];

  lines.push('=== 调度方案 ===');
  lines.push(
    `总成本 ${fmt(sol.totalCost)} = 里程成本 ${fmt(sol.mileageCost)}` +
      `（${fmt(sol.totalKm)} km × ${fmt(problem.costPerKm)}）` +
      ` + 缺车罚金 ${fmt(sol.penalty)}`,
  );
  lines.push('');

  const invOf = (stepIdx: number): number[] =>
    stepIdx < 0 ? problem.stations.map((s) => s.initial) : (sol.steps[stepIdx]?.invAfter as number[]);

  const invStr = (inv: number[]): string =>
    inv
      .map((v, i) => {
        const s = problem.stations[i];
        const target = Math.min((s?.demand ?? 0), (s?.capacity ?? 0));
        const mark = v < target ? ` (缺 ${target - v})` : '';
        return `${ids[i] ?? '?'}=${v}${mark}`;
      })
      .join('  ');

  lines.push(`[初始] 车载 0，站点库存：${invStr(invOf(-1))}`);

  let cumKm = 0;
  sol.steps.forEach((step, idx) => {
    cumKm += step.driveKm;
    const from = step.from < 0 ? '仓库' : ids[step.from];
    const to = step.to < 0 ? '仓库' : ids[step.to];
    let action: string;
    if (step.to < 0) {
      action = '回仓';
    } else if (step.delta > 0) {
      action = `卸车 ${step.delta} 辆`;
    } else if (step.delta < 0) {
      action = `装车 ${-step.delta} 辆`;
    } else {
      action = '不装卸';
    }
    lines.push(
      `[步骤 ${idx + 1}] ${from} -> ${to}，行驶 ${fmt(step.driveKm)} km（累计 ${fmt(cumKm)} km），` +
        `${action}，车载 ${step.loadAfter}，站点库存：${invStr(step.invAfter)}`,
    );
  });

  lines.push('');
  lines.push('=== 最终结果 ===');
  problem.stations.forEach((s, i) => {
    const target = Math.min(s.demand, s.capacity);
    const finalInv = sol.steps.length > 0 ? (invOf(sol.steps.length - 1)[i] as number) : s.initial;
    const lack = sol.shortage[i] as number;
    lines.push(
      `站点 ${s.id}：最终库存 ${finalInv} / 容量 ${s.capacity}，需求目标 ${target}，` +
        (lack > 0 ? `缺 ${lack} 辆（罚金 ${fmt(lack * problem.penaltyPerBike)}）` : '满足需求'),
    );
  });
  const lastLoad = sol.steps.length > 0 ? (sol.steps[sol.steps.length - 1]?.loadAfter as number) : 0;
  lines.push(`车辆已回仓，回仓时车载 ${lastLoad} 辆。`);
  return lines.join('\n');
}
