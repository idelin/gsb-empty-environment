import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, appendFileSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { Journal, JournalCorruptionError } from '../src/journal.ts';
import { Executor, actionKey, compensationKey } from '../src/executor.ts';
import {
  makeAdapters,
  buildOrderFlow,
  isConsistent,
  snapshot,
} from '../src/order-flow.ts';
import type { Order, OrderAdapters } from '../src/order-flow.ts';

class SimulatedCrash extends Error {
  constructor() {
    super('simulated process crash (record already fsync-durable)');
    this.name = 'SimulatedCrash';
  }
}

const ORDER: Order = {
  orderId: 'order-1',
  sku: 'SKU-1',
  qty: 2,
  amount: 500,
  address: '1 Infinite Loop',
};

function freshDir(): string {
  return mkdtempSync(join(tmpdir(), 'saga-'));
}

function journalPath(dir: string): string {
  return join(dir, 'flow.journal');
}

/** Run a flow to a terminal state, tolerating SimulatedCrash. Returns status. */
function runCatching(dir: string, adapters: OrderAdapters, crashAfterAppend?: number) {
  let appends = 0;
  const hook =
    crashAfterAppend === undefined
      ? undefined
      : () => {
          appends += 1;
          if (appends === crashAfterAppend) throw new SimulatedCrash();
        };
  const executor = new Executor(new Journal(journalPath(dir)), hook);
  const flow = buildOrderFlow(ORDER, adapters);
  try {
    return executor.run(flow);
  } catch (err) {
    if (err instanceof SimulatedCrash) return 'crashed';
    throw err;
  }
}

function resume(dir: string, adapters: OrderAdapters) {
  const executor = new Executor(new Journal(journalPath(dir)));
  return executor.resume(buildOrderFlow(ORDER, adapters));
}

test('happy path: completes and business state matches terminal state', () => {
  const dir = freshDir();
  const adapters = makeAdapters();
  const status = runCatching(dir, adapters);
  assert.equal(status, 'completed');
  assert.ok(isConsistent(ORDER, adapters, status));
  assert.deepEqual(snapshot(ORDER, adapters), {
    reserved: 2,
    balance: 500,
    deliveryScheduled: true,
  });
  const types = new Journal(journalPath(dir)).records.map((r) => r.type);
  assert.deepEqual(types, [
    'flow_start',
    'action_intent',
    'action_result',
    'action_intent',
    'action_result',
    'action_intent',
    'action_result',
    'terminal',
  ]);
});

test('restart at every persistence boundary (happy path)', () => {
  // A clean run appends 8 records; crash after the n-th append for every n.
  const total = 8;
  for (let n = 1; n <= total; n++) {
    const dir = freshDir();
    const adapters = makeAdapters();
    assert.equal(runCatching(dir, adapters, n), 'crashed');
    const status = resume(dir, adapters);
    assert.equal(status, 'completed', `boundary ${n}`);
    assert.ok(isConsistent(ORDER, adapters, status), `boundary ${n}`);
    for (const step of ['reserve-inventory', 'charge-payment', 'schedule-delivery']) {
      const key = actionKey(ORDER.orderId, step);
      const applied =
        adapters.inventory.applyCount(key) +
        adapters.payment.applyCount(key) +
        adapters.delivery.applyCount(key);
      assert.equal(applied, 1, `step ${step} applied exactly once at boundary ${n}`);
    }
  }
});

test('restart at every persistence boundary (payment rejected -> compensation)', () => {
  // flow_start + intent + result(ok) + intent + result(failed)
  //   + comp_intent + comp_result + terminal = 8 records.
  const total = 8;
  for (let n = 1; n <= total; n++) {
    const dir = freshDir();
    const adapters = makeAdapters();
    adapters.payment.injectFault('reject'); // definitive decline, not executed
    assert.equal(runCatching(dir, adapters, n), 'crashed');
    const status = resume(dir, adapters);
    assert.equal(status, 'failed', `boundary ${n}`);
    assert.ok(isConsistent(ORDER, adapters, status), `boundary ${n}`);
    assert.equal(
      adapters.inventory.applyCount(compensationKey(ORDER.orderId, 'reserve-inventory')),
      1,
      `release applied exactly once at boundary ${n}`,
    );
    assert.equal(adapters.payment.applyCount(compensationKey(ORDER.orderId, 'charge-payment')), 0);
  }
});

test('lost receipt: recovery queries by idempotency key instead of redoing', () => {
  const dir = freshDir();
  const adapters = makeAdapters();
  adapters.payment.injectFault('drop-receipt'); // charge executes, receipt lost
  const first = runCatching(dir, adapters);
  assert.equal(first, 'running'); // suspended with an unknown outcome

  const journal = new Journal(journalPath(dir));
  const unknown = journal.records.find((r) => r.type === 'action_result' && r.status === 'unknown');
  assert.ok(unknown, 'unknown outcome was logged');

  const status = resume(dir, adapters);
  assert.equal(status, 'completed');
  assert.ok(isConsistent(ORDER, adapters, status));
  // The charge was applied exactly once: recovery confirmed it via query().
  const chargeKey = actionKey(ORDER.orderId, 'charge-payment');
  assert.equal(adapters.payment.applyCount(chargeKey), 1);
  const resolved = new Journal(journalPath(dir)).records.find(
    (r) => r.type === 'action_result' && r.via === 'recovery-query',
  );
  assert.ok(resolved, 'recovery resolved the unknown outcome via idempotency-key query');
});

test('lost receipt on compensation: recovery reconciles, no double release', () => {
  const dir = freshDir();
  const adapters = makeAdapters();
  adapters.payment.injectFault('reject'); // force compensation of reserve
  adapters.inventory.injectFault('drop-receipt', 'release'); // release executes, receipt lost
  const first = runCatching(dir, adapters);
  assert.equal(first, 'compensating');

  const status = resume(dir, adapters);
  assert.equal(status, 'failed');
  assert.ok(isConsistent(ORDER, adapters, status));
  const compKey = compensationKey(ORDER.orderId, 'reserve-inventory');
  assert.equal(adapters.inventory.applyCount(compKey), 1, 'release applied exactly once');
});

test('transient compensation failure is recoverable on resume', () => {
  const dir = freshDir();
  const adapters = makeAdapters();
  adapters.payment.injectFault('reject');
  adapters.inventory.injectFault('transient', 'release'); // release fails once, not executed
  const first = runCatching(dir, adapters);
  assert.equal(first, 'compensating');
  assert.equal(snapshot(ORDER, adapters).reserved, 2, 'still reserved while compensating');

  const status = resume(dir, adapters);
  assert.equal(status, 'failed');
  assert.ok(isConsistent(ORDER, adapters, status));
});

test('completed compensation is never re-applied on repeated resume', () => {
  const dir = freshDir();
  const adapters = makeAdapters();
  adapters.payment.injectFault('reject');
  assert.equal(runCatching(dir, adapters), 'failed');
  const before = new Journal(journalPath(dir)).records.length;
  const releaseKey = compensationKey(ORDER.orderId, 'reserve-inventory');
  assert.equal(adapters.inventory.applyCount(releaseKey), 1);

  assert.equal(resume(dir, adapters), 'failed'); // terminal is sticky
  const after = new Journal(journalPath(dir)).records.length;
  assert.equal(after, before, 'no new records written');
  assert.equal(adapters.inventory.applyCount(releaseKey), 1, 'release not re-applied');
  assert.ok(isConsistent(ORDER, adapters, 'failed'));
});

test('duplicate cancel and cancel-vs-success race yield exactly one terminal state', () => {
  // Case 1: cancel before the run starts, cancel twice.
  const dir1 = freshDir();
  const adapters1 = makeAdapters();
  const exec1 = new Executor(new Journal(journalPath(dir1)));
  assert.equal(exec1.cancel(ORDER.orderId), 'cancel_requested');
  assert.equal(exec1.cancel(ORDER.orderId), 'cancel_requested'); // duplicate: no-op
  const status1 = exec1.run(buildOrderFlow(ORDER, adapters1));
  assert.equal(status1, 'cancelled');
  assert.ok(isConsistent(ORDER, adapters1, status1));
  const records1 = exec1.journal.records;
  assert.equal(records1.filter((r) => r.type === 'cancel_requested').length, 1);
  assert.equal(records1.filter((r) => r.type === 'terminal').length, 1);
  assert.equal(exec1.cancel(ORDER.orderId), 'cancelled'); // late cancel: no-op

  // Case 2: cancel arrives mid-flow (after step 1 succeeds).
  const dir2 = freshDir();
  const adapters2 = makeAdapters();
  const exec2 = new Executor(new Journal(journalPath(dir2)), (rec) => {
    if (rec.type === 'action_result' && rec.step === 'reserve-inventory') {
      exec2.cancel(ORDER.orderId);
    }
  });
  const status2 = exec2.run(buildOrderFlow(ORDER, adapters2));
  assert.equal(status2, 'cancelled');
  assert.ok(isConsistent(ORDER, adapters2, status2));
  assert.equal(exec2.journal.records.filter((r) => r.type === 'terminal').length, 1);

  // Case 3: cancel races with final success and loses — exactly one terminal.
  const dir3 = freshDir();
  const adapters3 = makeAdapters();
  const exec3 = new Executor(new Journal(journalPath(dir3)));
  assert.equal(exec3.run(buildOrderFlow(ORDER, adapters3)), 'completed');
  assert.equal(exec3.cancel(ORDER.orderId), 'completed'); // no-op, success wins
  assert.equal(exec3.journal.records.filter((r) => r.type === 'terminal').length, 1);
  assert.equal(exec3.journal.records.filter((r) => r.type === 'cancel_requested').length, 0);
  assert.ok(isConsistent(ORDER, adapters3, 'completed'));
});

test('journal protocol: torn last record is truncated, resume continues', () => {
  const dir = freshDir();
  const adapters = makeAdapters();
  assert.equal(runCatching(dir, adapters), 'completed');
  // Simulate a torn write: append half a record without a newline.
  appendFileSync(journalPath(dir), '{"seq":9,"ts":123,"type":"term');
  const journal = new Journal(journalPath(dir));
  assert.ok(journal.truncatedBytes > 0, 'torn tail was truncated');
  assert.equal(journal.records.at(-1)?.type, 'terminal');
  // Flow still resumes to the same terminal state.
  assert.equal(resume(dir, adapters), 'completed');
  assert.ok(isConsistent(ORDER, adapters, 'completed'));
});

test('journal protocol: complete-but-corrupt record raises an error', () => {
  const dir = freshDir();
  const adapters = makeAdapters();
  assert.equal(runCatching(dir, adapters), 'completed');

  // Case 1: newline-terminated garbage line.
  appendFileSync(journalPath(dir), '{"seq":9,"type":"terminal","state":"x\n');
  assert.throws(() => new Journal(journalPath(dir)), JournalCorruptionError);

  // Case 2: bit flip inside a complete record (CRC mismatch).
  const dir2 = freshDir();
  const adapters2 = makeAdapters();
  assert.equal(runCatching(dir2, adapters2), 'completed');
  const content = readFileSync(journalPath(dir2), 'utf8');
  const idx = content.indexOf('reserve-inventory');
  const flipped =
    content.slice(0, idx) + 'X' + content.slice(idx + 1); // corrupt one byte, keep newline
  writeFileSync(journalPath(dir2), flipped);
  assert.throws(() => new Journal(journalPath(dir2)), JournalCorruptionError);
});
