/**
 * 命令行入口：
 *   node src/cli.ts <input.json> [--json]
 *   cat input.json | node src/cli.ts [--json]
 *
 * 输入 JSON 格式：
 * {
 *   "kerf": 5,
 *   "stocks":  [{ "length": 6000, "price": 100 }, ...],
 *   "demands": [{ "length": 1200, "count": 4 }, ...]
 * }
 */
import { readFileSync } from 'node:fs';
import { solveCuttingStock, type PlannerInput, type PlanResult } from './solver.ts';

function fmt(x: number): string {
  return Number.isInteger(x) ? String(x) : x.toFixed(3).replace(/\.?0+$/, '');
}

export function renderPlan(input: PlannerInput, plan: PlanResult): string {
  const lines: string[] = [];
  if (!plan.feasible) {
    lines.push(`无可行方案：${plan.reason ?? '未知原因'}`);
    return lines.join('\n');
  }
  lines.push('=== 最优切割方案 ===');
  lines.push(
    `总采购成本 ${fmt(plan.totalCost)} ｜ 总切割刀数 ${plan.totalCuts} ｜ 采购原料棒 ${plan.totalBars} 根`,
  );
  lines.push(`（锯缝 ${fmt(input.kerf)}，余料可复用阈值 ≥ ${fmt(plan.minUsefulRemnant)}）`);
  lines.push('');
  plan.bars.forEach((bar, i) => {
    const sum = bar.pieces.reduce((a, b) => a + b, 0);
    const consumed = sum + bar.cuts * input.kerf;
    lines.push(
      `第 ${i + 1} 根：原料 长度 ${fmt(bar.stock.length)} / 单价 ${fmt(bar.stock.price)}`,
    );
    lines.push(
      `  切法：${bar.pieces.map(fmt).join(' + ')}  （${bar.pieces.length} 件，${bar.cuts} 刀）`,
    );
    lines.push(
      `  用料：订单 ${fmt(sum)} + 锯缝 ${bar.cuts}×${fmt(input.kerf)}=${fmt(bar.cuts * input.kerf)}` +
        ` = ${fmt(consumed)} ／ 余料 ${fmt(bar.remnant)}（锯缝已扣除，不计入可复用长度）`,
    );
    lines.push(
      bar.remnantFate === 'reusable'
        ? `  余料去向：留存入库，可用于后续 ≥${fmt(plan.minUsefulRemnant)} 的订单`
        : `  余料去向：${bar.remnant > 0 ? '长度不足，作废料处理' : '无剩余'}`,
    );
    if (i < plan.bars.length - 1) lines.push('');
  });
  const reusable = plan.bars.filter((b) => b.remnantFate === 'reusable');
  if (reusable.length > 0) {
    lines.push('');
    lines.push(
      `余料库存：${reusable.map((b) => fmt(b.remnant)).join('、')}（本方案成本/刀数已达最优，无需动用；留作后续订单）`,
    );
  }
  return lines.join('\n');
}

async function main(): Promise<void> {
  const args = process.argv.slice(2);
  const asJson = args.includes('--json');
  const file = args.find((a) => a !== '--json');
  const raw = file
    ? readFileSync(file, 'utf8')
    : readFileSync(0, 'utf8'); // stdin
  const input = JSON.parse(raw) as PlannerInput;
  const plan = solveCuttingStock(input);
  if (asJson) {
    console.log(JSON.stringify(plan, null, 2));
  } else {
    console.log(renderPlan(input, plan));
  }
  if (!plan.feasible) process.exitCode = 1;
}

// 作为脚本直接运行时执行 main（被 import 时不执行）
if (process.argv[1] && import.meta.url.endsWith(process.argv[1].split('/').pop()!)) {
  void main();
}
