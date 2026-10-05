import { readFileSync } from 'node:fs';
import { plan } from './planner.ts';
import type { Problem } from './types.ts';

const fmt = (v: number): string =>
  v.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });

function main(): void {
  const file = process.argv[2];
  if (!file) {
    console.error('用法: node src/cli.ts <problem.json>');
    process.exit(1);
  }
  const problem = JSON.parse(readFileSync(file, 'utf8')) as Problem;
  const result = plan(problem);

  console.log('========== 采购方案 ==========');
  if (result.status === 'infeasible') {
    console.log('无可行方案：需求无法在约束内被满足。');
    process.exit(2);
  }
  console.log(`状态: ${result.status === 'optimal' ? '已证明最优' : '可行（预算内未证完最优性）'}`);
  console.log(
    `总成本(含运费): ¥${fmt(result.totalCost)}  =  折后货款 ¥${fmt(result.merchandiseCost)} + 运费 ¥${fmt(result.freightCost)}（整单折扣共省 ¥${fmt(result.discountAmount)}）`,
  );
  console.log('');
  for (const order of result.orders) {
    console.log(`--- 供应商 ${order.supplierId} ---`);
    for (const line of order.lines) {
      console.log(
        `  ${line.materialId}: ${line.packs} 包 × ${line.packSize} 件 = ${line.qty} 件 × ¥${fmt(line.unitPrice)} = ¥${fmt(line.amount)}`,
      );
    }
    console.log(`  折前金额: ¥${fmt(order.subtotal)}  折扣: -¥${fmt(order.discountAmount)}  运费: ¥${fmt(order.freight)}  小计: ¥${fmt(order.total)}`);
    console.log(`  阶梯触发依据: ${order.tierRationale}`);
    console.log('');
  }
  console.log('--- 物料满足情况 ---');
  for (const m of result.materials) {
    const over = m.overbuyRatio > 1e-9 ? `（超购 ${(m.overbuyRatio * 100).toFixed(1)}%，上限 ${(m.maxOverbuyRatio * 100).toFixed(0)}%）` : '';
    console.log(`  ${m.materialId}: 需求 ${m.demand}，实采 ${m.purchased}${over}`);
  }
  console.log('');
  console.log(
    `求解统计: LP ${result.stats.lpSolves} 次，分支节点 ${result.stats.branchNodes} 个，耗时 ${result.stats.timeMs}ms`,
  );
}

main();
