/**
 * 验收场景：长采购事务（审批 → 采购 → 付款）。
 * 付款阶段等待外部银行回执，银行一直不响应 → 超时监控唤醒 →
 * 逆向补偿：取消采购单 → 通知审批人，全链路留下审计轨迹。
 */
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { SagaEngine } from '../src/engine.ts';
import type { SagaDefinition } from '../src/types.ts';

/** 模拟外部系统，记录全部副作用调用以便复核。 */
const calls: string[] = [];
const log = (msg: string) => {
  calls.push(msg);
  console.log(`  [外部系统] ${msg}`);
};

const approver = {
  approve: async (orderId: string) => log(`审批人通过采购申请 ${orderId}`),
  notifyCancelled: async (orderId: string) => log(`已通知审批人：采购申请 ${orderId} 已取消`),
};
const supplier = {
  createOrder: async (orderId: string) => log(`供应商创建采购单 PO-${orderId}`),
  cancelOrder: async (orderId: string) => log(`供应商取消采购单 PO-${orderId}`),
};
const bank = {
  // 银行网关只接收请求，永不回执 —— 模拟外部超时。
  transfer: async (orderId: string) => log(`已向银行发起付款请求 PAY-${orderId}（等待回执…）`),
};

const HOUR = 60 * 60 * 1000;
// 演示用时间缩放：把“2 小时付款超时”压缩为 300ms，语义不变。
const TIME_SCALE = 300 / (2 * HOUR);

const procurementSaga: SagaDefinition = {
  name: 'procurement',
  steps: [
    {
      name: '审批',
      action: async (ctx) => {
        const orderId = (ctx.input as { orderId: string }).orderId;
        ctx.data.orderId = orderId;
        await approver.approve(orderId);
        return { kind: 'done', output: 'approved' };
      },
      compensate: async (ctx) => {
        await approver.notifyCancelled(String(ctx.data.orderId));
      },
    },
    {
      name: '采购',
      action: async (ctx) => {
        await supplier.createOrder(String(ctx.data.orderId));
        return { kind: 'done', output: `PO-${ctx.data.orderId}` };
      },
      compensate: async (ctx) => {
        await supplier.cancelOrder(String(ctx.data.orderId));
      },
    },
    {
      name: '付款',
      action: async (ctx) => {
        await bank.transfer(String(ctx.data.orderId));
        return { kind: 'wait' }; // 挂起，等待银行异步回执
      },
      timeoutMs: Math.round(2 * HOUR * TIME_SCALE), // 业务语义：2 小时
    },
  ],
};

const dir = mkdtempSync(join(tmpdir(), 'saga-demo-'));
const engine = new SagaEngine(dir);
engine.registerSaga(procurementSaga);

console.log('== 发起长采购事务（审批 → 采购 → 付款）==');
const id = await engine.start('procurement', { orderId: 'SO-20261003-001', amount: 128000 });

console.log('\n== 付款阶段等待银行回执，银行超时未响应，引擎超时监控唤醒 ==');
const final = await engine.waitFor(id);

console.log(`\n== 终态: ${final.status} ==`);
console.log(`\n== 全链路审计轨迹（持久化于 ${dir}/saga-events.log）==`);
for (const e of engine.auditTrail(id)) {
  const { type, at, instanceId, ...rest } = e;
  const detail = Object.entries(rest)
    .map(([k, v]) => `${k}=${typeof v === 'object' ? JSON.stringify(v) : v}`)
    .join(' ');
  console.log(`  ${new Date(at).toISOString()}  ${type}${detail ? '  ' + detail : ''}`);
}
await engine.shutdown();
