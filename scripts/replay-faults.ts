// Deterministic fault-replay harness.
//
// Every scenario runs against a fixed fault script, so `node scripts/replay-faults.ts`
// always reproduces the same sequence of faults, restarts and outcomes. The
// expected output is committed at replay-results.txt; regenerate with:
//
//     node scripts/replay-faults.ts | tee replay-results.txt
import assert from 'node:assert/strict';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import type { FaultEntry } from '../src/adapters.ts';
import { SimulatedCrashError, WorkflowInconclusiveError, WorkflowPausedError } from '../src/errors.ts';
import { Journal } from '../src/journal.ts';
import { createOrderExecutor } from '../src/order-workflow.ts';
import type { OrderInput } from '../src/order-workflow.ts';
import { DeliveryAdapter, InventoryAdapter, PaymentAdapter } from '../src/adapters.ts';
import type { TerminalState } from '../src/executor.ts';

const ORDER: OrderInput = {
  orderId: 'O-REPLAY',
  sku: 'SKU-9',
  quantity: 2,
  account: 'acct-1',
  amount: 150,
  address: '1 Infinite Loop',
};
const STOCK = 10;
const BALANCE = 1000;

function world(faults: FaultEntry[]) {
  const dir = mkdtempSync(join(tmpdir(), 'saga-replay-'));
  return {
    journalPath: join(dir, 'workflow.journal'),
    inventory: new InventoryAdapter({ [ORDER.sku]: STOCK }, faults),
    payment: new PaymentAdapter({ [ORDER.account]: BALANCE }, faults),
    delivery: new DeliveryAdapter(faults),
    clearFaults() {
      this.inventory.setFaultScript([]);
      this.payment.setFaultScript([]);
      this.delivery.setFaultScript([]);
    },
  };
}
type World = ReturnType<typeof world>;

function boot(w: World, crashAfter = -1) {
  const journal = Journal.open(w.journalPath, {
    beforeAppend: (nextSeq) => {
      if (nextSeq === crashAfter) throw new SimulatedCrashError();
    },
  });
  const executor = createOrderExecutor(
    ORDER,
    { inventory: w.inventory, payment: w.payment, delivery: w.delivery },
    journal,
  );
  return { journal, executor };
}

function snapshot(w: World) {
  return `stock=${w.inventory.stock.get(ORDER.sku)} balance=${w.payment.balances.get(ORDER.account)} shipment=${w.delivery.shipments.get(ORDER.orderId)?.status ?? 'none'}`;
}

function expectConsistent(w: World, terminal: TerminalState): void {
  if (terminal === 'completed') {
    assert.equal(w.inventory.stock.get(ORDER.sku), STOCK - ORDER.quantity);
    assert.equal(w.payment.balances.get(ORDER.account), BALANCE - ORDER.amount);
    assert.equal(w.delivery.shipments.get(ORDER.orderId)?.status, 'scheduled');
  } else {
    assert.equal(w.inventory.stock.get(ORDER.sku), STOCK);
    assert.equal(w.payment.balances.get(ORDER.account), BALANCE);
    assert.notEqual(w.delivery.shipments.get(ORDER.orderId)?.status, 'scheduled');
  }
}

const results: string[] = [];
function report(name: string, detail: string): void {
  results.push(`PASS ${name} :: ${detail}`);
}

// S1: receipt lost on charge -> restart -> reconcile by idempotency key.
async function s1(): Promise<void> {
  const w = world([{ method: 'payment.charge', occurrence: 1, fault: 'lost-receipt' }]);
  const first = boot(w);
  await assert.rejects(first.executor.run(), WorkflowInconclusiveError);
  first.journal.close();
  w.clearFaults();
  const second = boot(w);
  const terminal = await second.executor.run();
  assert.equal(terminal, 'completed');
  assert.equal(w.payment.callCount('payment.charge'), 1);
  expectConsistent(w, terminal);
  second.journal.close();
  report('S1 lost-receipt-on-charge', `terminal=completed chargeCalls=1 ${snapshot(w)}`);
}

// S2: receipt lost on the refund compensation -> restart -> reconcile.
async function s2(): Promise<void> {
  const w = world([
    { method: 'delivery.schedule', occurrence: 1, fault: 'error' },
    { method: 'payment.refund', occurrence: 1, fault: 'lost-receipt' },
  ]);
  const first = boot(w);
  await assert.rejects(first.executor.run(), WorkflowInconclusiveError);
  first.journal.close();
  w.clearFaults();
  w.delivery.setFaultScript([{ method: 'delivery.schedule', occurrence: 1, fault: 'error' }]);
  const second = boot(w);
  const terminal = await second.executor.run();
  assert.equal(terminal, 'compensated');
  assert.equal(w.payment.callCount('payment.refund'), 1);
  expectConsistent(w, terminal);
  second.journal.close();
  report('S2 lost-receipt-on-refund', `terminal=compensated refundCalls=1 ${snapshot(w)}`);
}

// S3: transient refund failure -> restart -> compensation resumes and finishes.
async function s3(): Promise<void> {
  const w = world([
    { method: 'delivery.schedule', occurrence: 1, fault: 'error' },
    { method: 'payment.refund', occurrence: 1, fault: 'transient' },
  ]);
  const first = boot(w);
  await assert.rejects(first.executor.run(), WorkflowPausedError);
  first.journal.close();
  w.clearFaults();
  w.delivery.setFaultScript([{ method: 'delivery.schedule', occurrence: 1, fault: 'error' }]);
  const second = boot(w);
  const terminal = await second.executor.run();
  assert.equal(terminal, 'compensated');
  assert.equal(w.inventory.callCount('inventory.release'), 1);
  expectConsistent(w, terminal);
  second.journal.close();
  report('S3 transient-refund-then-recover', `terminal=compensated releaseCalls=1 ${snapshot(w)}`);
}

// S4: crash at every persistence boundary (9 boundaries) -> always converges.
async function s4(): Promise<void> {
  // A fault-free run journals 8 records (started + 3x(intent,result) + terminal),
  // giving 8 persistence boundaries to crash at (before each record).
  let converged = 0;
  for (let crashAfter = 0; crashAfter < 8; crashAfter++) {
    const w = world([]);
    const crashed = boot(w, crashAfter);
    await assert.rejects(crashed.executor.run(), SimulatedCrashError);
    crashed.journal.close();
    const recovered = boot(w);
    const terminal = await recovered.executor.run();
    assert.equal(terminal, 'completed');
    expectConsistent(w, terminal);
    recovered.journal.close();
    converged++;
  }
  report('S4 crash-at-every-boundary', `boundaries=${converged} all terminal=completed`);
}

// S5: cancel vs success race, both orderings -> exactly one terminal each.
async function s5(): Promise<void> {
  // Cancel wins: injected after the last action, before the terminal write.
  const w1 = world([]);
  const j1 = Journal.open(w1.journalPath);
  const e1 = createOrderExecutor(ORDER, { inventory: w1.inventory, payment: w1.payment, delivery: w1.delivery }, j1, {
    afterStep: (i) => {
      if (i === 2) e1.cancel();
    },
  });
  const t1 = await e1.run();
  const terminals1 = j1.records.filter((r) => r.type === 'terminal').length;
  assert.equal(t1, 'cancelled');
  assert.equal(terminals1, 1);
  expectConsistent(w1, t1);
  j1.close();

  // Success wins: cancel after completion is a no-op.
  const w2 = world([]);
  const j2 = Journal.open(w2.journalPath);
  const e2 = createOrderExecutor(ORDER, { inventory: w2.inventory, payment: w2.payment, delivery: w2.delivery }, j2);
  const t2 = await e2.run();
  const cancelResult = e2.cancel();
  const terminals2 = j2.records.filter((r) => r.type === 'terminal').length;
  assert.equal(t2, 'completed');
  assert.equal(cancelResult, 'completed');
  assert.equal(terminals2, 1);
  expectConsistent(w2, t2);
  j2.close();
  report('S5 cancel-vs-success-race', `cancel-first=${t1}(terminals=${terminals1}) success-first=${t2}(terminals=${terminals2})`);
}

// S6: repeated cancellation -> one cancel record, one terminal, stable state.
async function s6(): Promise<void> {
  const w = world([]);
  const { journal, executor } = boot(w);
  executor.cancel();
  executor.cancel();
  const t1 = await executor.run();
  executor.cancel();
  const t2 = await executor.run();
  assert.equal(t1, 'cancelled');
  assert.equal(t2, 'cancelled');
  assert.equal(journal.records.filter((r) => r.type === 'cancel').length, 1);
  assert.equal(journal.records.filter((r) => r.type === 'terminal').length, 1);
  expectConsistent(w, 'cancelled');
  journal.close();
  report('S6 repeated-cancel', `terminal=cancelled cancelRecords=1 terminalRecords=1 ${snapshot(w)}`);
}

const scenarios: Array<[string, () => Promise<void>]> = [
  ['S1', s1],
  ['S2', s2],
  ['S3', s3],
  ['S4', s4],
  ['S5', s5],
  ['S6', s6],
];

console.log(`fault-replay run at ${new Date().toISOString()} (deterministic fault scripts)`);
let failed = 0;
for (const [name, fn] of scenarios) {
  try {
    await fn();
  } catch (error) {
    failed++;
    results.push(`FAIL ${name} :: ${(error as Error).message}`);
  }
}
for (const line of results) console.log(line);
console.log(failed === 0 ? `ALL ${scenarios.length} SCENARIOS PASSED` : `${failed}/${scenarios.length} SCENARIOS FAILED`);
if (failed > 0) process.exit(1);
