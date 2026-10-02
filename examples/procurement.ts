/**
 * 验收场景演示：长采购事务（审批 -> 采购 -> 付款）
 *
 * 第一部分：付款阶段等待外部银行超时 -> 超时监控唤醒 -> 逆向取消采购单、
 *           通知审批人 -> 输出全链路审计轨迹。
 * 第二部分：断电续跑 —— 事务在等待银行回调时"断电"，新进程从日志恢复，
 *           银行回调到达后事务完成，已执行步骤不重复（不重不漏）。
 *
 * 演示中用 300ms 代表数小时的银行响应窗口；测试中用 ManualClock 真实模拟小时级超时。
 */
import { rmSync } from 'node:fs';
import { SagaEngine } from '../src/engine.ts';
import type { SagaDefinition } from '../src/types.ts';

const JOURNAL = 'demo-data/journal.jsonl';
const HOUR = 60 * 60 * 1000;

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const log = (msg: string) => console.log(`  ${msg}`);

function buildProcurementSaga(opts: { bankTimeoutMs: number; tag: string }): SagaDefinition {
  const t = opts.tag;
  return {
    name: 'procurement',
    steps: [
      {
        name: 'approval',
        timeoutMs: 4 * HOUR,
        action: async (ctx) => {
          log(`[${t}] 提交审批系统 (idempotencyKey=${ctx.idempotencyKey})`);
          await sleep(20);
          log(`[${t}] 审批通过，审批人=王经理`);
          return { type: 'done', output: { approver: '王经理', approvalId: 'AP-1001' } };
        },
        compensate: async () => {
          log(`[${t}] 【补偿】通知审批人王经理：采购事务已取消`);
          await sleep(10);
        },
      },
      {
        name: 'purchase',
        timeoutMs: 4 * HOUR,
        action: async (ctx) => {
          log(`[${t}] 调用采购系统创建采购单 (idempotencyKey=${ctx.idempotencyKey})`);
          await sleep(20);
          log(`[${t}] 采购单创建成功 PO-2026-0001`);
          return { type: 'done', output: { orderId: 'PO-2026-0001' } };
        },
        compensate: async () => {
          log(`[${t}] 【补偿】取消采购单 PO-2026-0001`);
          await sleep(10);
        },
      },
      {
        name: 'payment',
        timeoutMs: opts.bankTimeoutMs,
        action: async () => {
          log(`[${t}] 向银行发起付款请求，等待异步回调（窗口 ${opts.bankTimeoutMs}ms，代表数小时）...`);
          return { type: 'pending' }; // 银行永不回调 -> 超时
        },
      },
    ],
  };
}

function printAuditTrail(engine: SagaEngine, txId: string): void {
  console.log(`\n===== 审计轨迹 ${txId} =====`);
  for (const e of engine.getAuditTrail(txId)) {
    const step = 'step' in e ? ` ${e.step}` : '';
    const extra =
      e.type === 'STEP_TIMED_OUT' ? ` deadline=${e.deadline}` :
      e.type === 'COMPENSATION_STARTED' ? ` 原因: ${e.reason}` :
      e.type === 'STEP_COMPLETED' && e.output !== undefined ? ` output=${JSON.stringify(e.output)}` : '';
    console.log(`  #${String(e.seq).padStart(2)} ${e.type}${step}${extra}`);
  }
  const tx = engine.getTransaction(txId)!;
  console.log(`  最终状态: ${tx.status} | 步骤: ${tx.steps.map((s) => `${s.name}=${s.state}`).join(', ')}`);
}

async function part1(): Promise<void> {
  console.log('===== 第一部分：付款超时 -> 逆向补偿 =====');
  const engine = new SagaEngine({ journalPath: JOURNAL });
  engine.registerSaga(buildProcurementSaga({ bankTimeoutMs: 300, tag: 'tx-001' }));
  engine.recover();

  console.log('发起长采购事务 tx-001（审批 -> 采购 -> 付款）');
  engine.startTransaction('procurement', 'tx-001', { item: '服务器', amount: 88000 });
  await engine.whenIdle();
  console.log('事务挂起，等待银行回调中（此处跨越数小时）...');

  await sleep(500); // 银行始终不回调 -> 超时监控唤醒
  await engine.whenIdle();
  printAuditTrail(engine, 'tx-001');
  engine.shutdown();
}

async function part2(): Promise<void> {
  console.log('\n===== 第二部分：断电续跑（不重不漏） =====');
  let purchaseCalls = 0;
  const saga = buildProcurementSaga({ bankTimeoutMs: 10_000, tag: 'tx-002' });
  const originalPurchase = saga.steps[1].action;
  saga.steps[1].action = async (ctx) => {
    purchaseCalls++;
    return originalPurchase(ctx);
  };

  const engine1 = new SagaEngine({ journalPath: JOURNAL });
  engine1.registerSaga(saga);
  engine1.recover();
  console.log('发起长采购事务 tx-002');
  engine1.startTransaction('procurement', 'tx-002', { item: '交换机', amount: 12000 });
  await engine1.whenIdle();
  console.log(`等待银行回调中... 此时"断电"（purchase 动作已执行 ${purchaseCalls} 次）`);
  engine1.shutdown(); // 模拟进程消亡

  console.log('重启：新引擎实例从日志恢复状态机');
  const engine2 = new SagaEngine({ journalPath: JOURNAL });
  engine2.registerSaga(saga);
  engine2.recover();
  await engine2.whenIdle();
  console.log(`恢复后 purchase 动作累计执行 ${purchaseCalls} 次（仍为 1 = 未重复执行）`);

  console.log('银行回调到达：付款成功');
  const accepted = engine2.resolveExternal('tx-002', 'payment', { bankTxn: 'BK-7788' });
  await engine2.whenIdle();
  console.log(`回调被受理: ${accepted}`);
  printAuditTrail(engine2, 'tx-002');
  engine2.shutdown();
}

rmSync('demo-data', { recursive: true, force: true });
await part1();
await part2();
