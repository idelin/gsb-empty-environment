import * as assert from 'assert';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { EventStore } from '../src/event-store';
import { Executor } from '../src/executor';
import { FaultInjector, SimulatedCrash } from '../src/faults';
import { StepDefinition, StepContext } from '../src/graph';

/** Mock downstream service: applies each idempotency key at most once. */
class Ledger {
  readonly applyCalls: string[] = [];
  readonly effects: string[] = [];
  private appliedKeys = new Set<string>();

  apply(key: string, label: string): void {
    this.applyCalls.push(label);
    if (this.appliedKeys.has(key)) return; // idempotent dedupe
    this.appliedKeys.add(key);
    this.effects.push(label);
  }

  effectCount(label: string): number {
    return this.effects.filter((e) => e === label).length;
  }
}

interface Harness {
  store: EventStore;
  faults: FaultInjector;
  ledger: Ledger;
  compensationLedger: Ledger;
  compensationOrder: string[];
  executeCalls: Map<string, number>;
  compensateCalls: Map<string, number>;
  step(id: string, deps: string[], opts?: { failExecute?: boolean }): StepDefinition;
}

function makeHarness(dir: string, name: string): Harness {
  const store = new EventStore(path.join(dir, `${name}.jsonl`));
  const faults = new FaultInjector();
  const ledger = new Ledger();
  const compensationLedger = new Ledger();
  const compensationOrder: string[] = [];
  const executeCalls = new Map<string, number>();
  const compensateCalls = new Map<string, number>();

  const step = (id: string, deps: string[], opts?: { failExecute?: boolean }): StepDefinition => ({
    id,
    deps,
    execute: (ctx: StepContext) => {
      executeCalls.set(id, (executeCalls.get(id) ?? 0) + 1);
      if (opts?.failExecute) throw new Error(`execute(${id}) blew up`);
      ledger.apply(ctx.idempotencyKey, `exec:${id}`);
    },
    compensate: (ctx: StepContext) => {
      compensateCalls.set(id, (compensateCalls.get(id) ?? 0) + 1);
      compensationOrder.push(id);
      compensationLedger.apply(ctx.idempotencyKey, `comp:${id}`);
    },
  });

  return { store, faults, ledger, compensationLedger, compensationOrder, executeCalls, compensateCalls, step };
}

const tests: Array<{ name: string; fn: () => Promise<void> }> = [];
function test(name: string, fn: () => Promise<void>): void {
  tests.push({ name, fn });
}

// 1. DAG happy path: layered execution, no compensation.
test('DAG happy path executes layers in dependency order', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'saga-'));
  const h = makeHarness(dir, 'happy');
  const exec = new Executor(h.store, h.faults);
  const status = await exec.run('run-happy', [
    h.step('a', []),
    h.step('b1', ['a']),
    h.step('b2', ['a']),
    h.step('c', ['b1', 'b2']),
  ]);
  assert.strictEqual(status, 'completed');
  assert.deepStrictEqual(h.ledger.effects, ['exec:a', 'exec:b1', 'exec:b2', 'exec:c']);
  assert.strictEqual(h.compensationOrder.length, 0);
  const types = h.store.forRun('run-happy').map((e) => e.type);
  assert.ok(types.includes('RunCompleted'));
});

// 2. Branch success: one branch fails, only succeeded steps compensated, reverse dependency order.
test('branch failure compensates only succeeded steps in reverse dependency order', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'saga-'));
  const h = makeHarness(dir, 'branch');
  const exec = new Executor(h.store, h.faults);
  const status = await exec.run('run-branch', [
    h.step('root', []),
    h.step('left1', ['root']),
    h.step('left2', ['left1']),
    h.step('right1', ['root']),
    h.step('right2', ['right1'], { failExecute: true }),
    h.step('right3', ['right2']), // never runs: dep failed
  ]);
  assert.strictEqual(status, 'compensated');
  // left branch fully succeeded; right branch failed at right2.
  // Compensation covers exactly the succeeded steps, dependents first.
  assert.deepStrictEqual(h.compensationOrder, ['left2', 'right1', 'left1', 'root']);
  // failed and never-started steps are not compensated
  assert.strictEqual(h.compensateCalls.get('right2') ?? 0, 0);
  assert.strictEqual(h.compensateCalls.get('right3') ?? 0, 0);
  assert.strictEqual(h.executeCalls.get('right3') ?? 0, 0);
  // event table records the failure and the compensation receipts
  const events = h.store.forRun('run-branch');
  assert.ok(events.some((e) => e.type === 'StepFailed' && e.stepId === 'right2'));
  assert.ok(events.some((e) => e.type === 'RunFailed'));
  assert.ok(events.some((e) => e.type === 'RunCompensated'));
  assert.ok(!events.some((e) => e.type === 'CompensationStarted' && e.stepId === 'right2'));
});

// 3. Receipt loss (回执丢失): crash after execute applied, before StepSucceeded persisted.
test('lost success receipt: resume re-executes with same idempotency key, effect applied once', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'saga-'));
  const h = makeHarness(dir, 'receipt-loss');
  h.faults.crashBeforeSuccessReceipt.add('b');

  const steps = [h.step('a', []), h.step('b', ['a']), h.step('c', ['b'])];
  const exec1 = new Executor(h.store, h.faults);
  await assert.rejects(exec1.run('run-rl', steps), SimulatedCrash);

  // Crash left StepStarted(b) without StepSucceeded(b) in the event table.
  const mid = h.store.forRun('run-rl');
  assert.ok(mid.some((e) => e.type === 'StepStarted' && e.stepId === 'b'));
  assert.ok(!mid.some((e) => e.type === 'StepSucceeded' && e.stepId === 'b'));

  // New executor instance over the same persisted table resumes the run.
  const exec2 = new Executor(h.store, h.faults);
  const status = await exec2.resume('run-rl', steps);
  assert.strictEqual(status, 'completed');

  // b was executed twice but its side effect applied exactly once (idempotent step id).
  assert.strictEqual(h.executeCalls.get('b'), 2);
  assert.strictEqual(h.ledger.effectCount('exec:b'), 1);
  // a was not re-executed: its receipt survived.
  assert.strictEqual(h.executeCalls.get('a'), 1);
  // exactly one StepSucceeded receipt per step
  const receipts = h.store.forRun('run-rl').filter((e) => e.type === 'StepSucceeded');
  assert.strictEqual(receipts.filter((e) => e.stepId === 'b').length, 1);
});

// 4. Compensation failure is resumable; successful compensation is never repeated.
test('compensation failure resumes; succeeded compensations are not repeated', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'saga-'));
  const h = makeHarness(dir, 'comp-fail');
  h.faults.failCompensation('b1', 1); // first compensate(b1) throws

  const steps = [
    h.step('root', []),
    h.step('b1', ['root']),
    h.step('b2', ['root']),
    h.step('boom', ['b1', 'b2'], { failExecute: true }),
  ];
  const exec = new Executor(h.store, h.faults);
  const first = await exec.run('run-cf', steps);
  assert.strictEqual(first, 'compensation_failed');
  // b2 compensated before b1 failed (reverse order); run is stuck on b1.
  // The injected fault throws before b1's compensate callback runs.
  assert.strictEqual(h.compensateCalls.get('b2'), 1);
  assert.strictEqual(h.compensateCalls.get('b1') ?? 0, 0);
  assert.strictEqual(h.compensateCalls.get('root') ?? 0, 0);

  // Resume retries the failed compensation; b2 must NOT be compensated again.
  const second = await exec.resume('run-cf', steps);
  assert.strictEqual(second, 'compensated');
  assert.strictEqual(h.compensateCalls.get('b1'), 1);
  assert.strictEqual(h.compensateCalls.get('b2'), 1, 'successful compensation must not repeat');
  assert.strictEqual(h.compensateCalls.get('root'), 1);
  assert.deepStrictEqual(h.compensationOrder, ['b2', 'b1', 'root']);

  // A further resume is a no-op: the run is already fully compensated.
  const third = await exec.resume('run-cf', steps);
  assert.strictEqual(third, 'compensated');
  assert.strictEqual(h.compensateCalls.get('root'), 1);
  const compReceipts = h.store.forRun('run-cf').filter((e) => e.type === 'CompensationSucceeded');
  assert.strictEqual(compReceipts.length, 3); // b2, b1, root — exactly once each
});

// 5. Compensation receipt loss: compensate applied, crash before receipt; resume dedupes.
test('lost compensation receipt: resume retries with same idempotency key, effect applied once', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'saga-'));
  const h = makeHarness(dir, 'comp-receipt-loss');
  h.faults.crashBeforeCompensationReceipt.add('b');

  const steps = [h.step('a', []), h.step('b', ['a']), h.step('boom', ['b'], { failExecute: true })];
  const exec1 = new Executor(h.store, h.faults);
  await assert.rejects(exec1.run('run-crl', steps), SimulatedCrash);

  const mid = h.store.forRun('run-crl');
  assert.ok(mid.some((e) => e.type === 'CompensationStarted' && e.stepId === 'b'));
  assert.ok(!mid.some((e) => e.type === 'CompensationSucceeded' && e.stepId === 'b'));

  const exec2 = new Executor(h.store, h.faults);
  const status = await exec2.resume('run-crl', steps);
  assert.strictEqual(status, 'compensated');

  // compensate(b) ran twice, effect applied once via idempotency key.
  assert.strictEqual(h.compensateCalls.get('b'), 2);
  assert.strictEqual(h.compensationLedger.effectCount('comp:b'), 1);
  const receipts = h.store.forRun('run-crl').filter((e) => e.type === 'CompensationSucceeded');
  assert.strictEqual(receipts.filter((e) => e.stepId === 'b').length, 1);
});

// 6. Event table survives executor restart (persistence check).
test('event table persists across executor instances', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'saga-'));
  const file = path.join(dir, 'persist.jsonl');
  const store1 = new EventStore(file);
  const h = makeHarness(dir, 'unused');
  const exec1 = new Executor(store1, h.faults);
  await exec1.run('run-persist', [h.step('a', []), h.step('b', ['a'])]);

  const store2 = new EventStore(file); // reload from disk
  const exec2 = new Executor(store2, h.faults);
  const status = await exec2.resume('run-persist', [h.step('a', []), h.step('b', ['a'])]);
  assert.strictEqual(status, 'completed');
  // no step re-executed after reload
  assert.strictEqual(h.executeCalls.get('a') ?? 0, 1);
  assert.strictEqual(h.executeCalls.get('b') ?? 0, 1);
});

(async () => {
  let failed = 0;
  for (const t of tests) {
    try {
      await t.fn();
      console.log(`ok   - ${t.name}`);
    } catch (error) {
      failed++;
      console.error(`FAIL - ${t.name}`);
      console.error(error);
    }
  }
  console.log(`\n${tests.length - failed}/${tests.length} tests passed`);
  process.exit(failed === 0 ? 0 : 1);
})();
