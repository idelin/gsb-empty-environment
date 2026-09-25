import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ObligationExecutor } from '../src/executor.ts';
import { buildPlan, InMemoryCompensationLedger, type Obligation } from '../src/obligations.ts';
import type { Policy } from '../src/policy.ts';

const allowAll: Policy = {
  name: 'allow-all',
  requiredAttributes: ['role'],
  condition: { op: 'in', attr: 'role', set: ['admin', 'operator'] },
};
const attrs = { role: 'admin' };

function recording(id: string, log: string[], overrides: Partial<Obligation & { kind: 'rollbackable' }> = {}) {
  return {
    kind: 'rollbackable' as const,
    id,
    execute: () => {
      log.push(`execute:${id}`);
    },
    compensate: () => {
      log.push(`compensate:${id}`);
    },
    ...overrides,
  };
}

test('denied policy runs no obligations and never reports success', async () => {
  const log: string[] = [];
  const executor = new ObligationExecutor(allowAll, [recording('a', log)]);
  const result = await executor.run({ role: 'intruder' });
  assert.equal(result.status, 'denied');
  assert.deepEqual(log, []);
});

test('rollbackable obligations run in dependency order, commit obligations after the commit point', async () => {
  const log: string[] = [];
  const obligations: Obligation[] = [
    recording('c-audit', log, { kind: 'commit' as const, dependsOn: ['b'] } as never),
    recording('b', log, { dependsOn: ['a'] }),
    recording('a', log),
  ];
  const executor = new ObligationExecutor(allowAll, obligations);
  const result = await executor.run(attrs);
  assert.equal(result.status, 'committed');
  assert.deepEqual(log, ['execute:a', 'execute:b', 'execute:c-audit']);
  assert.deepEqual((result as { committed: string[] }).committed, ['c-audit']);
});

test('second obligation failure compensates the first in reverse order', async () => {
  const log: string[] = [];
  const obligations: Obligation[] = [
    recording('first', log),
    recording('second', log, {
      execute: () => {
        log.push('execute:second');
        throw new Error('boom');
      },
    }),
    recording('never-runs', log, { kind: 'commit' as const } as never),
  ];
  const executor = new ObligationExecutor(allowAll, obligations);
  const result = await executor.run(attrs);
  assert.equal(result.status, 'compensated');
  assert.equal((result as { failedObligation: string }).failedObligation, 'second');
  assert.deepEqual(log, ['execute:first', 'execute:second', 'compensate:first']);
});

test('compensation retries are idempotent: effect applied exactly once', async () => {
  let compensateCalls = 0;
  let effects = 0;
  const obligations: Obligation[] = [
    {
      kind: 'rollbackable',
      id: 'flaky',
      execute: () => {},
      compensate: () => {
        compensateCalls++;
        if (compensateCalls < 3) throw new Error('transient');
        effects++; // 幂等效果：无论重试多少次只生效一次
      },
    },
    {
      kind: 'rollbackable',
      id: 'failing',
      execute: () => {
        throw new Error('trigger rollback');
      },
      compensate: () => {},
    },
  ];
  const executor = new ObligationExecutor(allowAll, obligations, { maxCompensationAttempts: 5 });
  const result = await executor.run(attrs);
  assert.equal(result.status, 'compensated');
  assert.equal(compensateCalls, 3);
  assert.equal(effects, 1);
});

test('compensation failure keeps pending state; resume completes it later', async () => {
  const ledger = new InMemoryCompensationLedger();
  let shouldFail = true;
  let effects = 0;
  const obligations: Obligation[] = [
    {
      kind: 'rollbackable',
      id: 'stubborn',
      execute: () => {},
      compensate: () => {
        if (shouldFail) throw new Error('still broken');
        effects++;
      },
    },
    {
      kind: 'rollbackable',
      id: 'trigger',
      execute: () => {
        throw new Error('fail');
      },
      compensate: () => {},
    },
  ];
  const executor = new ObligationExecutor(allowAll, obligations, {
    maxCompensationAttempts: 2,
    ledger,
  });
  const first = await executor.run(attrs);
  assert.equal(first.status, 'pending_compensation');
  assert.deepEqual((first as { pending: string[] }).pending, ['stubborn']);
  assert.deepEqual(ledger.pending, ['stubborn']);

  // 恢复后重试，幂等效果只发生一次，待处理状态清空。
  shouldFail = false;
  const resumed = await executor.resumePendingCompensation(attrs);
  assert.equal(resumed?.status, 'compensated');
  assert.equal(effects, 1);
  assert.deepEqual(ledger.pending, []);
});

test('success is only final after the commit phase completes', async () => {
  const log: string[] = [];
  const obligations: Obligation[] = [
    recording('a', log),
    recording('commit-step', log, {
      kind: 'commit' as const,
      execute: () => {
        log.push('execute:commit-step');
        throw new Error('commit blew up');
      },
    } as never),
  ];
  const executor = new ObligationExecutor(allowAll, obligations);
  await assert.rejects(() => executor.run(attrs), /commit blew up/);
  // 回滚阶段成功也绝不提前返回 committed。
  assert.deepEqual(log, ['execute:a', 'execute:commit-step']);
});

test('rollbackable obligation may not depend on a non-rollbackable one', () => {
  const obligations: Obligation[] = [
    { kind: 'commit', id: 'irreversible', execute: () => {} },
    { kind: 'rollbackable', id: 'a', dependsOn: ['irreversible'], execute: () => {}, compensate: () => {} },
  ];
  assert.throws(() => buildPlan(obligations), /cannot depend on non-rollbackable/);
});

test('dependency cycles are rejected', () => {
  const obligations: Obligation[] = [
    { kind: 'rollbackable', id: 'a', dependsOn: ['b'], execute: () => {}, compensate: () => {} },
    { kind: 'rollbackable', id: 'b', dependsOn: ['a'], execute: () => {}, compensate: () => {} },
  ];
  assert.throws(() => buildPlan(obligations), /cycle/);
});
