import { readFileSync } from 'node:fs';
import {
  solveCuttingProblem,
  type BarPlan,
  type Plan,
  type Problem,
} from './cuttingPlanner.ts';

function leftoverText(bar: BarPlan, reusableMin: number): string {
  if (bar.leftoverUse === 'none') return '无剩余（恰好用尽）';
  if (bar.leftoverUse === 'reusable') {
    return `可复用余料，留存备用（≥ 复用门槛 ${reusableMin}）`;
  }
  return `废料（不足复用门槛 ${reusableMin}，不可再用于订单）`;
}

function barSignature(bar: BarPlan): string {
  const lens = bar.pieces.map((p) => p.length).sort((a, b) => b - a);
  return `${bar.stockLength}|${lens.join(',')}|${bar.leftover}`;
}

export function formatPlan(plan: Plan, problem: Problem): string {
  const reusableMin =
    problem.reusableMinLength ??
    Math.min(...problem.demands.map((d) => d.length));
  const lines: string[] = [];
  lines.push('===== 切割方案 =====');
  lines.push(
    `采购 ${plan.bars.length} 根原料棒，总成本 ${plan.totalCost}，` +
      `总切割 ${plan.totalCuts} 次（锯缝 ${problem.kerf}，共 ${plan.totalPieces} 件）`,
  );
  lines.push('说明：余料长度已扣除全部锯缝，锯缝不计入可复用长度。');

  // 相同切法的棒合并展示
  const groups = new Map<string, BarPlan[]>();
  for (const bar of plan.bars) {
    const key = barSignature(bar);
    const group = groups.get(key);
    if (group) group.push(bar);
    else groups.set(key, [bar]);
  }

  let groupNo = 0;
  for (const group of groups.values()) {
    groupNo++;
    const bar = group[0];
    const countText = group.length > 1 ? `（相同切法 × ${group.length} 根）` : '';
    lines.push('');
    lines.push(
      `棒型 ${groupNo}：原料 ${bar.stockLength}，单价 ${bar.price}${countText}`,
    );
    const cutsText = bar.pieces
      .map((p) => `订单#${p.orderId} ${p.length}`)
      .join(' → ');
    lines.push(`  切法：${cutsText}`);
    lines.push(
      `  切割 ${bar.cuts} 次；余料 ${bar.leftover} → ${leftoverText(bar, reusableMin)}`,
    );
  }
  return lines.join('\n');
}

function main(): void {
  const inputPath = process.argv[2];
  if (!inputPath) {
    console.error('用法: node src/cli.ts <input.json>');
    console.error(
      '输入格式: {"kerf":3,"stocks":[{"length":6000,"price":50}],"demands":[{"length":1200,"quantity":4}],"reusableMinLength":800}',
    );
    process.exit(1);
  }
  const problem = JSON.parse(readFileSync(inputPath, 'utf8')) as Problem;
  try {
    const plan = solveCuttingProblem(problem);
    console.log(formatPlan(plan, problem));
  } catch (err) {
    console.error(`求解失败：${(err as Error).message}`);
    process.exit(2);
  }
}

main();
