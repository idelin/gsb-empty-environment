import type { Plan } from './types.ts';

/** 将方案格式化为可读文本：每趟路线、载荷、同货位分订单数量 */
export function formatPlan(plan: Plan): string {
  const lines: string[] = [];
  lines.push(`总成本 = ${plan.totalCost}（行走 ${plan.walkingCost} + 拆单 ${plan.splitCostTotal}），共 ${plan.trips.length} 趟`);
  for (const trip of plan.trips) {
    lines.push('');
    lines.push(`第 ${trip.index} 趟：载荷 ${trip.load} 件，行走 ${trip.distance}，订单 [${trip.orderIds.join(', ')}]`);
    lines.push(`  路线：${trip.route.join(' -> ')}`);
    for (const stop of trip.stops) {
      const picks = stop.picks.map((p) => `${p.orderId}×${p.qty}`).join('，');
      lines.push(`  拣货 @${stop.location}：${picks}`);
    }
  }
  return lines.join('\n');
}
