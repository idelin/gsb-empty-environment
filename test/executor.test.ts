import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ObligationExecutor } from '../src/executor.ts';
import type { Obligation } from '../src/obligations.ts';

const o1: Obligation = { kind: 'quota', resource: 'api', limit: 5 };
const o2: Obligation = { kind: 'field-mask', fields: ['ssn'] };
const o3: Obligation = { kind: 'output-format', format: 'json' };

test('第二义务执行失败 → 已应用义务按逆序补偿', async () => {
  const events: string[] = [];
  const executor = new ObligationExecutor({
    apply: (obligation) => {
      events.push(`apply:${obligation.kind}`);
      if (obligation === o2) throw new Error('apply failed');
    },
    compensate: (obligation) => {
      events.push(`compensate:${obligation.kind}`);
    },
  });
  const state = await executor.execute([o1, o2, o3]);
  assert.equal(state.status, 'compensated');
  assert.deepEqual(state.applied, [o1]);
  assert.deepEqual(state.failedObligation, o2);
  assert.deepEqual(events, ['apply:quota', 'apply:field-mask', 'compensate:quota']);
});

test('多个已应用义务严格按逆序补偿', async () => {
  const compensated: string[] = [];
  const executor = new ObligationExecutor({
    apply: (obligation) => {
      if (obligation === o3) throw new Error('third failed');
    },
    compensate: (obligation) => {
      compensated.push(obligation.kind);
    },
  });
  const state = await executor.execute([o1, o2, o3]);
  assert.equal(state.status, 'compensated');
  assert.deepEqual(compensated, ['field-mask', 'quota']);
});

test('补偿失败保留可重试状态，retry 从断点恢复', async () => {
  let compensateAttempts = 0;
  const compensated: string[] = [];
  const executor = new ObligationExecutor({
    apply: (obligation) => {
      if (obligation === o3) throw new Error('apply failed');
    },
    compensate: (obligation) => {
      compensateAttempts += 1;
      if (compensateAttempts === 1) throw new Error('compensation failed once');
      compensated.push(obligation.kind);
    },
  });

  const state = await executor.execute([o1, o2, o3]);
  assert.equal(state.status, 'compensation-pending');
  assert.deepEqual(state.pendingCompensations, [o2, o1]);
  assert.equal(state.compensationErrors.length, 1);

  const retried = await executor.retry(state);
  assert.equal(retried.status, 'compensated');
  assert.deepEqual(retried.pendingCompensations, []);
  assert.deepEqual(compensated, ['field-mask', 'quota']);

  // 原始失败状态不可变，仍是可重试快照
  assert.equal(state.status, 'compensation-pending');
  assert.deepEqual(state.pendingCompensations, [o2, o1]);
});
