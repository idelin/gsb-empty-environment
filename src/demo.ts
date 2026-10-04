import { Simulator } from './simulator.ts';
import { Boundary } from './faults.ts';
import type { OrderEvent } from './types.ts';

const events: OrderEvent[] = [
  { eventId: 'evt-1', orderId: 'order-1', status: 'PAID', amount: 100 },
  { eventId: 'evt-2', orderId: 'order-1', status: 'SHIPPED', amount: 100 },
];

const sim = new Simulator(events);
console.log('== 注入故障：通知已投递，但“已发送”确认持久化之前崩溃 ==');
sim.runWithCrashAt(Boundary.RelayMarkSent, 'before');

console.log(`崩溃次数:            ${sim.stats.crashes}`);
console.log(`事件重投次数:        ${sim.stats.eventRedeliveries}`);
console.log(`接收方去重次数:      ${sim.notificationReceiver.duplicates}`);
for (const [eventId, count] of sim.stats.orderApplications) {
  console.log(`订单事件 ${eventId} 应用次数: ${count}`);
}
for (const [ntfId, count] of sim.stats.notificationEffects) {
  console.log(`通知 ${ntfId} 生效次数: ${count}`);
}
console.log('订单终态:', sim.database.getOrder('order-1'));
