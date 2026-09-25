import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ObligationError,
  ObligationExecutor,
  type Obligation,
} from '../src/executor.ts';
import type { Policy } from '../src/policy.ts';

const allowAll: Policy = { id: 'p', condition: { op: 'in', attr: 'ok', set: [true] } };
const attrs = { ok: true };

function recorder(log: string[]) {
  return (name: string) => () => {
    log.push(name);
  };
}

test('denied policy executes nothing and returns denied', async () => {
  const log: string[] = [];
  const ex = new ObligationExecutor();
  const result = await ex.authorizeAndRun(
    allowAll,
    { ok: false },
    [{ id: 'a', kind: 'rollbackable', execute: recorder(log)('exec:a'), compensate: recorder(log)('comp:a') }],
    'exec-deny',
  );
  assert.equal(result.status, 'denied');
  assert.deepEqual(log, []);
});

test('runs rollbackable obligations in dependency order, then commit phase', async () => {
  const log: string[] = [];
  const r = recorder(log);
  const obligations: Obligation[] = [
    { id: 'charge', kind: 'rollbackable', dependsOn: ['reserve'], execute: r('exec:charge'), compensate: r('comp:charge') },
    { id: 'reserve', kind: 'rollbackable', execute: r('exec:reserve'), compensate: r('comp:reserve') },
    { id: 'email', kind: 'non-rollbackable', execute: r('exec:email') },
  ];
  const ex = new ObligationExecutor();
  const result = await ex.authorizeAndRun(allowAll, attrs, obligations, 'exec-ok');
  assert.equal(result.status, 'committed');
  assert.deepEqual(log, ['exec:reserve', 'exec:charge', 'exec:email']);
  assert.deepEqual(result.executed, ['reserve', 'charge']);
});

test('second obligation failure compensates the first in reverse order', async () => {
  const log: string[] = [];
  const r = recorder(log);
  const obligations: Obligation[] = [
    { id: 'first', kind: 'rollbackable', execute: r('exec:first'), compensate: r('comp:first') },
    {
      id: 'second',
      kind: 'rollbackable',
      dependsOn: ['first'],
      execute: () => { log.push('exec:second'); throw new Error('boom'); },
      compensate: r('comp:second'),
    },
    { id: 'email', kind: 'non-rollbackable', execute: r('exec:email') },
  ];
  const ex = new ObligationExecutor();
  const result = await ex.authorizeAndRun(allowAll, attrs, obligations, 'exec-fail');
  assert.equal(result.status, 'compensated');
  assert.equal(result.failedObligation, 'second');
  assert.deepEqual(log, ['exec:first', 'exec:second', 'comp:first']);
  assert.deepEqual(result.compensated, ['first']);
});

test('compensation failure keeps compensation_pending, resume is idempotent', async () => {
  const log: string[] = [];
  const keys: string[] = [];
  let compAAttempts = 0;
  let compBCalls = 0;
  const obligations: Obligation[] = [
    {
      id: 'a',
      kind: 'rollbackable',
      execute: () => { log.push('exec:a'); },
      compensate: (ctx) => {
        compAAttempts++;
        keys.push(ctx.idempotencyKeyFor('a', 'compensate'));
        if (compAAttempts <= 3) throw new Error('comp-a down');
        log.push('comp:a');
      },
    },
    {
      id: 'b',
      kind: 'rollbackable',
      dependsOn: ['a'],
      execute: () => { log.push('exec:b'); },
      compensate: () => { compBCalls++; log.push('comp:b'); },
    },
    {
      id: 'c',
      kind: 'rollbackable',
      dependsOn: ['b'],
      execute: () => { log.push('exec:c'); throw new Error('c failed'); },
      compensate: () => { log.push('comp:c'); },
    },
  ];
  const ex = new ObligationExecutor({ maxCompensationAttempts: 3 });
  const result = await ex.authorizeAndRun(allowAll, attrs, obligations, 'exec-pend');

  assert.equal(result.status, 'compensation_pending');
  assert.deepEqual(result.pendingCompensations, ['a']);
  assert.deepEqual(result.compensated, ['b']);
  assert.deepEqual(log, ['exec:a', 'exec:b', 'exec:c', 'comp:b']);
  assert.equal(compAAttempts, 3);
  assert.equal(new Set(keys).size, 1, 'idempotency key stable across retries');

  const resumed = await ex.resumePendingCompensations('exec-pend', obligations);
  assert.equal(resumed.status, 'compensated');
  assert.deepEqual(resumed.pendingCompensations, []);
  assert.equal(compBCalls, 1, 'already-compensated obligation is not re-invoked');
  assert.deepEqual(log, ['exec:a', 'exec:b', 'exec:c', 'comp:b', 'comp:a']);

  const again = await ex.resumePendingCompensations('exec-pend', obligations);
  assert.equal(again.status, 'compensated');
  assert.equal(compAAttempts, 4, 'no further compensation once settled');
});

test('non-rollbackable obligations never run before the commit phase', async () => {
  const log: string[] = [];
  const r = recorder(log);
  const obligations: Obligation[] = [
    { id: 'email', kind: 'non-rollbackable', execute: r('exec:email') },
    {
      id: 'charge',
      kind: 'rollbackable',
      execute: () => { log.push('exec:charge'); throw new Error('declined'); },
      compensate: r('comp:charge'),
    },
  ];
  const ex = new ObligationExecutor();
  const result = await ex.authorizeAndRun(allowAll, attrs, obligations, 'exec-nocommit');
  assert.equal(result.status, 'compensated');
  assert.ok(!log.includes('exec:email'), 'commit-phase obligation must not run on failure');
});

test('commit-phase failure reports commit_failed without rolling back', async () => {
  const log: string[] = [];
  const r = recorder(log);
  const obligations: Obligation[] = [
    { id: 'reserve', kind: 'rollbackable', execute: r('exec:reserve'), compensate: r('comp:reserve') },
    { id: 'email', kind: 'non-rollbackable', execute: () => { throw new Error('smtp down'); } },
  ];
  const ex = new ObligationExecutor();
  const result = await ex.authorizeAndRun(allowAll, attrs, obligations, 'exec-commitfail');
  assert.equal(result.status, 'commit_failed');
  assert.equal(result.failedObligation, 'email');
  assert.deepEqual(log, ['exec:reserve']);
});

test('rejects invalid obligation graphs', async () => {
  const ex = new ObligationExecutor();
  await assert.rejects(
    ex.authorizeAndRun(allowAll, attrs, [{ id: 'a', kind: 'rollbackable', execute: () => {} }], 'x1'),
    ObligationError,
  );
  const cycle: Obligation[] = [
    { id: 'a', kind: 'rollbackable', dependsOn: ['b'], execute: () => {}, compensate: () => {} },
    { id: 'b', kind: 'rollbackable', dependsOn: ['a'], execute: () => {}, compensate: () => {} },
  ];
  await assert.rejects(ex.authorizeAndRun(allowAll, attrs, cycle, 'x2'), ObligationError);
  const badDep: Obligation[] = [
    { id: 'a', kind: 'rollbackable', dependsOn: ['n'], execute: () => {}, compensate: () => {} },
    { id: 'n', kind: 'non-rollbackable', execute: () => {} },
  ];
  await assert.rejects(ex.authorizeAndRun(allowAll, attrs, badDep, 'x3'), ObligationError);
});
