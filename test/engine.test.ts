import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { SagaEngine } from '../src/engine.ts';
import { ManualClock } from '../src/clock.ts';
import type { SagaDefinition, StepContext } from '../src/types.ts';

const HOUR = 60 * 60 * 1000;

function tempJournal(): string {
  return join(mkdtempSync(join(tmpdir(), 'saga-')), 'journal.jsonl');
}

interface Counters {
  approval: number;
  purchase: number;
  payment: number;
  cancelPurchase: number;
  notifyApprover: number;
  calls: string[];
  idempotencyKeys: string[];
}

function makeCounters(): Counters {
  return { approval: 0, purchase: 0, payment: 0, cancelPurchase: 0, notifyApprover: 0, calls: [], idempotencyKeys: [] };
}

/** 验收用采购 Saga：审批 -> 采购 -> 付款（付款等待外部银行回调） */
function makeProcurementSaga(c: Counters, opts: { paymentMode: 'pending' | 'done' }): SagaDefinition {
  const track = (name: string, ctx: StepContext) => {
    c.calls.push(name);
    c.idempotencyKeys.push(ctx.idempotencyKey);
  };
  return {
    name: 'procurement',
    steps: [
      {
        name: 'approval',
        timeoutMs: 2 * HOUR,
        action: async (ctx) => {
          c.approval++;
          track('approval', ctx);
          return { type: 'done', output: { approver: '王经理' } };
        },
        compensate: async () => {
          c.notifyApprover++;
          c.calls.push('notify-approver');
        },
      },
      {
        name: 'purchase',
        timeoutMs: 2 * HOUR,
        action: async (ctx) => {
          c.purchase++;
          track('purchase', ctx);
          return { type: 'done', output: { orderId: 'PO-1' } };
        },
        compensate: async () => {
          c.cancelPurchase++;
          c.calls.push('cancel-purchase');
        },
      },
      {
        name: 'payment',
        timeoutMs: 2 * HOUR,
        action: async (ctx) => {
          c.payment++;
          track('payment', ctx);
          return opts.paymentMode === 'pending' ? { type: 'pending' } : { type: 'done', output: { bankTxn: 'BK-1' } };
        },
      },
    ],
  };
}

test('正常路径：三步依次完成，审计轨迹完整有序', async () => {
  const clock = new ManualClock(1_000_000);
  const engine = new SagaEngine({ journalPath: tempJournal(), clock });
  const c = makeCounters();
  engine.registerSaga(makeProcurementSaga(c, { paymentMode: 'done' }));
  engine.recover();

  engine.startTransaction('procurement', 'tx-ok', { item: 'laptop' });
  await engine.whenIdle();

  assert.equal(engine.getTransaction('tx-ok')?.status, 'COMPLETED');
  assert.deepEqual(c.calls, ['approval', 'purchase', 'payment']);
  assert.deepEqual(
    engine.getAuditTrail('tx-ok').map((e) => e.type),
    ['TX_STARTED', 'STEP_STARTED', 'STEP_COMPLETED', 'STEP_STARTED', 'STEP_COMPLETED', 'STEP_STARTED', 'STEP_COMPLETED', 'TX_COMPLETED'],
  );
});

test('验收场景：付款等待银行超时 -> 逆向取消采购单并通知审批人，全程留痕', async () => {
  const clock = new ManualClock(1_000_000);
  const engine = new SagaEngine({ journalPath: tempJournal(), clock });
  const c = makeCounters();
  engine.registerSaga(makeProcurementSaga(c, { paymentMode: 'pending' }));
  engine.recover();

  engine.startTransaction('procurement', 'tx-timeout', { item: 'server' });
  await engine.whenIdle();
  assert.equal(engine.getTransaction('tx-timeout')?.status, 'WAITING_EXTERNAL');

  // 跨越数小时：银行始终未回调，超时监控唤醒
  clock.advance(3 * HOUR);
  await engine.whenIdle();

  const tx = engine.getTransaction('tx-timeout')!;
  assert.equal(tx.status, 'COMPENSATED');
  assert.deepEqual(tx.steps.map((s) => s.state), ['COMPENSATED', 'COMPENSATED', 'TIMED_OUT']);
  // 逆向顺序：先取消采购单，再通知审批人；付款未完成无需补偿
  assert.deepEqual(c.calls.slice(3), ['cancel-purchase', 'notify-approver']);

  const types = engine.getAuditTrail('tx-timeout').map((e) => e.type);
  assert.ok(types.includes('STEP_TIMED_OUT'));
  assert.ok(types.indexOf('STEP_TIMED_OUT') < types.indexOf('COMPENSATION_STARTED'));
  assert.ok(types.indexOf('COMPENSATION_STARTED') < types.indexOf('TX_COMPENSATED'));

  // 银行迟到回调必须被忽略（不重）
  assert.equal(engine.resolveExternal('tx-timeout', 'payment', { bankTxn: 'late' }), false);
  assert.equal(engine.getTransaction('tx-timeout')?.status, 'COMPENSATED');
});

test('外部拒绝：银行拒付触发同样的反向补偿', async () => {
  const clock = new ManualClock(1_000_000);
  const engine = new SagaEngine({ journalPath: tempJournal(), clock });
  const c = makeCounters();
  engine.registerSaga(makeProcurementSaga(c, { paymentMode: 'pending' }));
  engine.recover();

  engine.startTransaction('procurement', 'tx-reject', {});
  await engine.whenIdle();

  assert.equal(engine.rejectExternal('tx-reject', 'payment', '余额不足'), true);
  await engine.whenIdle();

  assert.equal(engine.getTransaction('tx-reject')?.status, 'COMPENSATED');
  assert.deepEqual(c.calls.slice(3), ['cancel-purchase', 'notify-approver']);
  const types = engine.getAuditTrail('tx-reject').map((e) => e.type);
  assert.ok(types.includes('STEP_FAILED'));
  assert.ok(types.includes('TX_COMPENSATED'));
});

test('断电续跑：等待外部回调时崩溃，恢复后超时监控仍然生效，步骤不重复执行', async () => {
  const journalPath = tempJournal();
  const clock = new ManualClock(1_000_000);
  const c = makeCounters();

  const engine1 = new SagaEngine({ journalPath, clock });
  engine1.registerSaga(makeProcurementSaga(c, { paymentMode: 'pending' }));
  engine1.recover();
  engine1.startTransaction('procurement', 'tx-crash', {});
  await engine1.whenIdle();
  assert.equal(c.purchase, 1);
  engine1.shutdown(); // 模拟断电：进程消亡，定时器丢失

  // 重启：新引擎从日志重放恢复
  const engine2 = new SagaEngine({ journalPath, clock });
  engine2.registerSaga(makeProcurementSaga(c, { paymentMode: 'pending' }));
  engine2.recover();
  await engine2.whenIdle();
  assert.equal(engine2.getTransaction('tx-crash')?.status, 'WAITING_EXTERNAL');
  assert.equal(c.purchase, 1); // 不重：已完成的步骤未再执行

  clock.advance(3 * HOUR); // 恢复后超时监控继续计时
  await engine2.whenIdle();
  assert.equal(engine2.getTransaction('tx-crash')?.status, 'COMPENSATED');
  assert.equal(c.cancelPurchase, 1); // 不漏：补偿恰好各执行一次
  assert.equal(c.notifyApprover, 1);
});

test('断电续跑：步骤动作执行中崩溃，恢复后以同一幂等键重放该步骤', async () => {
  const journalPath = tempJournal();
  const clock = new ManualClock(1_000_000);
  const c1 = makeCounters();

  // 第一进程：purchase 动作永不返回（模拟执行中断电）
  let purchaseStarted!: () => void;
  const started = new Promise<void>((resolve) => (purchaseStarted = resolve));
  const saga1 = makeProcurementSaga(c1, { paymentMode: 'done' });
  saga1.steps[1].action = () => {
    purchaseStarted();
    return new Promise(() => {});
  };
  const engine1 = new SagaEngine({ journalPath, clock });
  engine1.registerSaga(saga1);
  engine1.recover();
  engine1.startTransaction('procurement', 'tx-mid', {});
  await started; // purchase 动作已在飞（永不返回，模拟断电瞬间）
  assert.equal(c1.approval, 1);
  engine1.shutdown();

  // 第二进程：恢复后重放 purchase，approval 不重放
  const c2 = makeCounters();
  const engine2 = new SagaEngine({ journalPath, clock });
  engine2.registerSaga(makeProcurementSaga(c2, { paymentMode: 'done' }));
  engine2.recover();
  await engine2.whenIdle();

  assert.equal(engine2.getTransaction('tx-mid')?.status, 'COMPLETED');
  assert.equal(c2.approval, 0); // 已完成步骤不重放
  assert.equal(c2.purchase, 1); // 中断步骤恰好重放一次
  assert.equal(c2.idempotencyKeys[0], 'tx-mid:purchase'); // 幂等键稳定，外部系统可去重
});

test('断电续跑：补偿执行到一半崩溃，恢复后从未完成的补偿步骤继续', async () => {
  const journalPath = tempJournal();
  // 手工构造"补偿取消采购单已完成、通知审批人未执行"时断电的日志
  const events = [
    { type: 'TX_STARTED', txId: 'tx-half', saga: 'procurement', data: {}, steps: ['approval', 'purchase', 'payment'] },
    { type: 'STEP_STARTED', txId: 'tx-half', index: 0, step: 'approval', deadline: 1 },
    { type: 'STEP_COMPLETED', txId: 'tx-half', index: 0, step: 'approval' },
    { type: 'STEP_STARTED', txId: 'tx-half', index: 1, step: 'purchase', deadline: 2 },
    { type: 'STEP_COMPLETED', txId: 'tx-half', index: 1, step: 'purchase' },
    { type: 'STEP_STARTED', txId: 'tx-half', index: 2, step: 'payment', deadline: 3 },
    { type: 'STEP_WAITING', txId: 'tx-half', index: 2, step: 'payment' },
    { type: 'STEP_TIMED_OUT', txId: 'tx-half', index: 2, step: 'payment', deadline: 3 },
    { type: 'COMPENSATION_STARTED', txId: 'tx-half', reason: 'step "payment" timed out' },
    { type: 'COMPENSATION_STEP_STARTED', txId: 'tx-half', index: 1, step: 'purchase' },
    { type: 'COMPENSATION_STEP_DONE', txId: 'tx-half', index: 1, step: 'purchase' },
    // <-- 此处断电：approval 的补偿（通知审批人）尚未执行
  ];
  writeFileSync(journalPath, events.map((e) => JSON.stringify(e)).join('\n') + '\n');

  const c = makeCounters();
  const engine = new SagaEngine({ journalPath, clock: new ManualClock(1_000_000) });
  engine.registerSaga(makeProcurementSaga(c, { paymentMode: 'pending' }));
  engine.recover();
  await engine.whenIdle();

  assert.equal(engine.getTransaction('tx-half')?.status, 'COMPENSATED');
  assert.equal(c.cancelPurchase, 0); // 崩溃前已补偿，不重
  assert.equal(c.notifyApprover, 1); // 剩余补偿被续跑，不漏
  assert.deepEqual(c.calls, ['notify-approver']);
});

test('断电续跑：恢复后银行回调仍可完成事务', async () => {
  const journalPath = tempJournal();
  const clock = new ManualClock(1_000_000);
  const c = makeCounters();

  const engine1 = new SagaEngine({ journalPath, clock });
  engine1.registerSaga(makeProcurementSaga(c, { paymentMode: 'pending' }));
  engine1.recover();
  engine1.startTransaction('procurement', 'tx-resume', {});
  await engine1.whenIdle();
  engine1.shutdown();

  const engine2 = new SagaEngine({ journalPath, clock });
  engine2.registerSaga(makeProcurementSaga(c, { paymentMode: 'pending' }));
  engine2.recover();
  await engine2.whenIdle();

  assert.equal(engine2.resolveExternal('tx-resume', 'payment', { bankTxn: 'BK-9' }), true);
  await engine2.whenIdle();
  assert.equal(engine2.getTransaction('tx-resume')?.status, 'COMPLETED');
  assert.equal(c.purchase, 1);
});
