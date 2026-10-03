import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { SagaEngine } from '../src/engine.ts';
import type { SagaDefinition } from '../src/types.ts';

interface BankBehavior {
  mode: 'approve' | 'reject' | 'silent';
}

/** 构造采购 Saga（审批 → 采购 → 付款），外部系统调用全部记录到 calls。 */
function makeProcurementSaga(calls: string[], bank: BankBehavior): SagaDefinition {
  return {
    name: 'procurement',
    steps: [
      {
        name: '审批',
        action: async (ctx) => {
          ctx.data.orderId = (ctx.input as { orderId: string }).orderId;
          calls.push(`approve:${ctx.data.orderId}`);
          return { kind: 'done' };
        },
        compensate: async (ctx) => {
          calls.push(`notifyApprover:${ctx.data.orderId}`);
        },
      },
      {
        name: '采购',
        action: async (ctx) => {
          calls.push(`createPO:${ctx.data.orderId}`);
          return { kind: 'done' };
        },
        compensate: async (ctx) => {
          calls.push(`cancelPO:${ctx.data.orderId}`);
        },
      },
      {
        name: '付款',
        action: async (ctx) => {
          calls.push(`pay:${ctx.data.orderId}`);
          if (bank.mode === 'silent') return { kind: 'wait' };
          // 银行同步网关：立即回调
          setTimeout(() => {
            bank.notify?.(ctx.instanceId, bank.mode === 'approve'
              ? { status: 'completed', output: 'bank-receipt-1' }
              : { status: 'rejected', reason: '余额不足' });
        }, 10);
          return { kind: 'wait' };
        },
        timeoutMs: 120,
      },
    ],
  };
}

let notifyFn: ((id: string, o: unknown) => void) | null = null;

function makeDir(): string {
  return mkdtempSync(join(tmpdir(), 'saga-test-'));
}

async function waitUntil(cond: () => boolean, ms = 3000): Promise<void> {
  const start = Date.now();
  while (!cond()) {
    if (Date.now() - start > ms) throw new Error('等待条件超时');
    await new Promise((r) => setTimeout(r, 5));
  }
}

test('正向流程：审批 → 采购 → 付款全部完成，终态 completed', async () => {
  const dir = makeDir();
  const calls: string[] = [];
  const engine = new SagaEngine(dir);
  const bank: BankBehavior & { notify?: typeof notifyFn } = { mode: 'approve' };
  const saga = makeProcurementSaga(calls, bank);
  bank.notify = (id, o) => engine.notify(id, o as never);
  engine.registerSaga(saga);

  const id = await engine.start('procurement', { orderId: 'SO-1' });
  const final = await engine.waitFor(id);

  assert.equal(final.status, 'completed');
  assert.deepEqual(calls, ['approve:SO-1', 'createPO:SO-1', 'pay:SO-1']);
  const types = engine.auditTrail(id).map((e) => e.type);
  assert.deepEqual(types, [
    'instance_started',
    'step_started', 'step_completed',
    'step_started', 'step_completed',
    'step_started', 'step_waiting',
    'callback_received', 'step_completed',
    'instance_completed',
  ]);
  await engine.shutdown();
  rmSync(dir, { recursive: true, force: true });
});

test('付款超时：引擎唤醒超时监控，逆向取消采购单并通知审批人', async () => {
  const dir = makeDir();
  const calls: string[] = [];
  const engine = new SagaEngine(dir);
  engine.registerSaga(makeProcurementSaga(calls, { mode: 'silent' }));

  const id = await engine.start('procurement', { orderId: 'SO-2' });
  const final = await engine.waitFor(id);

  assert.equal(final.status, 'compensated');
  // 补偿严格逆序：先取消采购单，再通知审批人；付款未成功无需补偿
  assert.deepEqual(calls, [
    'approve:SO-2', 'createPO:SO-2', 'pay:SO-2',
    'cancelPO:SO-2', 'notifyApprover:SO-2',
  ]);
  const trail = engine.auditTrail(id);
  const types = trail.map((e) => e.type);
  assert.ok(types.includes('timeout_fired'), '审计轨迹应包含超时事件');
  assert.ok(types.includes('compensation_started'));
  assert.equal(types.filter((t) => t === 'compensation_step_completed').length, 2);
  assert.equal(types.at(-1), 'instance_compensated');
  // 审计轨迹时间戳单调不减，可复核
  const ats = trail.map((e) => e.at);
  assert.deepEqual(ats, [...ats].sort((a, b) => a - b));
  await engine.shutdown();
  rmSync(dir, { recursive: true, force: true });
});

test('外部拒绝：银行回绝付款触发逆向补偿', async () => {
  const dir = makeDir();
  const calls: string[] = [];
  const engine = new SagaEngine(dir);
  const bank: BankBehavior & { notify?: typeof notifyFn } = { mode: 'reject' };
  const saga = makeProcurementSaga(calls, bank);
  bank.notify = (id, o) => engine.notify(id, o as never);
  engine.registerSaga(saga);

  const id = await engine.start('procurement', { orderId: 'SO-3' });
  const final = await engine.waitFor(id);

  assert.equal(final.status, 'compensated');
  assert.deepEqual(calls, [
    'approve:SO-3', 'createPO:SO-3', 'pay:SO-3',
    'cancelPO:SO-3', 'notifyApprover:SO-3',
  ]);
  const types = engine.auditTrail(id).map((e) => e.type);
  assert.ok(types.includes('callback_received'));
  assert.ok(types.includes('step_failed'));
  assert.ok(!types.includes('timeout_fired'), '拒绝路径不应触发超时');
  await engine.shutdown();
  rmSync(dir, { recursive: true, force: true });
});

test('断电续跑：付款等待中崩溃，恢复后超时补偿精确执行一次（不重不漏）', async () => {
  const dir = makeDir();
  const calls1: string[] = [];
  const engine1 = new SagaEngine(dir);
  engine1.registerSaga(makeProcurementSaga(calls1, { mode: 'silent' }));

  const id = await engine1.start('procurement', { orderId: 'SO-4' });
  await waitUntil(() => engine1.getState(id)?.status === 'waiting');
  await engine1.shutdown(); // 模拟断电：定时器丢失，状态已落盘

  // 重启：新引擎重放审计日志恢复状态机
  const calls2: string[] = [];
  const engine2 = new SagaEngine(dir);
  engine2.registerSaga(makeProcurementSaga(calls2, { mode: 'silent' }));
  await engine2.recover();
  assert.equal(engine2.getState(id)?.status, 'waiting');

  const final = await engine2.waitFor(id);
  assert.equal(final.status, 'compensated');

  // 不重：崩溃前已完成的步骤绝不重跑
  assert.deepEqual(calls2, ['cancelPO:SO-4', 'notifyApprover:SO-4']);
  const trail = engine2.auditTrail(id);
  assert.equal(trail.filter((e) => e.type === 'step_started').length, 3, '每个正向步骤只启动一次');
  assert.equal(trail.filter((e) => e.type === 'compensation_step_completed').length, 2, '补偿步骤只执行一次');
  assert.equal(trail.filter((e) => e.type === 'timeout_fired').length, 1, '超时只触发一次');
  // 不漏：全链路轨迹完整
  assert.deepEqual(
    [...calls1, ...calls2],
    ['approve:SO-4', 'createPO:SO-4', 'pay:SO-4', 'cancelPO:SO-4', 'notifyApprover:SO-4'],
  );
  await engine2.shutdown();
  rmSync(dir, { recursive: true, force: true });
});

test('恢复时已过期：超时 deadline 在断电期间到达，恢复后立即触发补偿', async () => {
  const dir = makeDir();
  const calls1: string[] = [];
  const engine1 = new SagaEngine(dir);
  engine1.registerSaga(makeProcurementSaga(calls1, { mode: 'silent' }));

  const id = await engine1.start('procurement', { orderId: 'SO-5' });
  await waitUntil(() => engine1.getState(id)?.status === 'waiting');
  await engine1.shutdown();
  await new Promise((r) => setTimeout(r, 200)); // 断电期间 deadline(120ms) 已过

  const calls2: string[] = [];
  const engine2 = new SagaEngine(dir);
  engine2.registerSaga(makeProcurementSaga(calls2, { mode: 'silent' }));
  await engine2.recover();

  const final = await engine2.waitFor(id);
  assert.equal(final.status, 'compensated');
  assert.deepEqual(calls2, ['cancelPO:SO-5', 'notifyApprover:SO-5']);
  await engine2.shutdown();
  rmSync(dir, { recursive: true, force: true });
});
