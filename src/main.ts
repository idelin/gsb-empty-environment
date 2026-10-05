import { readFileSync } from 'node:fs';
import { solveProcurement, type Plan } from './solver.ts';
import type { Scenario } from './types.ts';

function money(v: number): string {
  return `¥${v.toFixed(2)}`;
}

export function renderReport(plan: Plan): string {
  const out: string[] = [];
  if (!plan.feasible) {
    out.push(`无可行方案：${plan.infeasibleReason}`);
    return out.join('\n');
  }
  out.push('========== 最低成本采购方案（含运费） ==========');
  for (const p of plan.purchases) {
    out.push('');
    out.push(`【${p.supplierName}】(${p.supplierId})`);
    for (const l of p.lines) {
      out.push(`  - ${l.materialId}: ${l.qty} 件 × ${money(l.unitPrice)} = ${money(l.amount)}`);
    }
    out.push(`  折前小计: ${money(p.subtotal)}`);
    out.push(`  阶梯依据: ${p.tierReason}`);
    if (p.discountAmount > 0) out.push(`  折扣金额: -${money(p.discountAmount)}`);
    out.push(`  运费: ${p.freight > 0 ? money(p.freight) : '免运费'}`);
    out.push(`  本供应商应付: ${money(p.total)}`);
  }
  out.push('');
  out.push('---------- 物料满足情况 ----------');
  for (const f of plan.fulfillment) {
    const over = f.overbuy > 0 ? `，超购 ${f.overbuy}（上限内）` : '';
    out.push(`  ${f.materialId}: 需求 ${f.demand}，实购 ${f.purchased}${over}`);
  }
  out.push('');
  out.push(`总成本（含运费）: ${money(plan.totalCost)}`);
  out.push(`(搜索状态数: ${plan.exploredStates})`);
  return out.join('\n');
}

const scenarioPath = process.argv[2];
if (!scenarioPath) {
  console.error('用法: node src/main.ts <scenario.json>');
  process.exit(1);
}
const scenario = JSON.parse(readFileSync(scenarioPath, 'utf8')) as Scenario;
const plan = solveProcurement(scenario);
console.log(renderReport(plan));
