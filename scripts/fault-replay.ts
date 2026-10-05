/**
 * Replayable fault-injection script. Every scenario uses a deterministic fault
 * schedule, so re-running produces the same terminal states and business
 * outcomes. Exit code is non-zero if any scenario's expectations fail.
 *
 * Usage: node scripts/fault-replay.ts
 */
import { mkdtempSync, appendFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { Journal, JournalCorruptionError } from '../src/journal.ts';
import { Executor, actionKey, compensationKey } from '../src/executor.ts';
import type { FlowStatus } from '../src/executor.ts';
import {
  makeAdapters,
  buildOrderFlow,
  isConsistent,
  snapshot,
} from '../src/order-flow.ts';
import type { Order, OrderAdapters } from '../src/order-flow.ts';

class SimulatedCrash extends Error {
  constructor() {
    super('simulated process crash');
    this.name = 'SimulatedCrash';
  }
}

const ORDER: Order = {
  orderId: 'order-replay',
  sku: 'SKU-1',
  qty: 2,
  amount: 500,
  address: '1 Infinite Loop',
};

let failures = 0;

function report(name: string, ok: boolean, detail: string): void {
  if (!ok) failures += 1;
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}  | ${detail}`);
}

function freshDir(): string {
  return mkdtempSync(join(tmpdir(), 'saga-replay-'));
}

function run(dir: string, adapters: OrderAdapters, crashAfterAppend?: number): FlowStatus | 'crashed' {
  let appends = 0;
  const hook =
    crashAfterAppend === undefined
      ? undefined
      : () => {
          appends += 1;
          if (appends === crashAfterAppend) throw new SimulatedCrash();
        };
  const executor = new Executor(new Journal(join(dir, 'flow.journal')), hook);
  try {
    return executor.run(buildOrderFlow(ORDER, adapters));
  } catch (err) {
    if (err instanceof SimulatedCrash) return 'crashed';
    throw err;
  }
}

function resume(dir: string, adapters: OrderAdapters): FlowStatus {
  return new Executor(new Journal(join(dir, 'flow.journal'))).resume(
    buildOrderFlow(ORDER, adapters),
  );
}

function describe(dir: string, adapters: OrderAdapters, terminal: string): string {
  const s = snapshot(ORDER, adapters);
  const consistent = isConsistent(ORDER, adapters, terminal);
  return (
    `terminal=${terminal} reserved=${s.reserved} balance=${s.balance} ` +
    `delivery=${s.deliveryScheduled ? 'scheduled' : 'none'} consistent=${consistent}`
  );
}

// --- Scenario 1: clean run -------------------------------------------------
{
  const dir = freshDir();
  const adapters = makeAdapters();
  const status = run(dir, adapters);
  report('clean-run', status === 'completed' && isConsistent(ORDER, adapters, status),
    describe(dir, adapters, status));
}

// --- Scenario 2: restart at every persistence boundary ---------------------
{
  let allOk = true;
  let worst = '';
  for (let n = 1; n <= 8; n++) {
    const dir = freshDir();
    const adapters = makeAdapters();
    run(dir, adapters, n); // crash after the n-th durable record
    const status = resume(dir, adapters);
    const once = ['reserve-inventory', 'charge-payment', 'schedule-delivery'].every((step) => {
      const key = actionKey(ORDER.orderId, step);
      return (
        adapters.inventory.applyCount(key) +
          adapters.payment.applyCount(key) +
          adapters.delivery.applyCount(key) ===
        1
      );
    });
    if (status !== 'completed' || !isConsistent(ORDER, adapters, status) || !once) {
      allOk = false;
      worst = `boundary=${n} ` + describe(dir, adapters, status);
    }
  }
  report('restart-at-every-boundary', allOk, allOk ? '8/8 boundaries recovered to completed' : worst);
}

// --- Scenario 3: lost receipt on charge ------------------------------------
{
  const dir = freshDir();
  const adapters = makeAdapters();
  adapters.payment.injectFault('drop-receipt');
  const first = run(dir, adapters); // suspends with unknown outcome
  const status = resume(dir, adapters);
  const chargeKey = actionKey(ORDER.orderId, 'charge-payment');
  const ok =
    first === 'running' &&
    status === 'completed' &&
    adapters.payment.applyCount(chargeKey) === 1 &&
    isConsistent(ORDER, adapters, status);
  report('lost-receipt-charge', ok,
    `first=${first} ` + describe(dir, adapters, status) +
    ` chargeApplies=${adapters.payment.applyCount(chargeKey)}`);
}

// --- Scenario 4: lost receipt on compensation ------------------------------
{
  const dir = freshDir();
  const adapters = makeAdapters();
  adapters.payment.injectFault('reject');
  adapters.inventory.injectFault('drop-receipt', 'release'); // release executes, receipt lost
  const first = run(dir, adapters);
  const status = resume(dir, adapters);
  const compKey = compensationKey(ORDER.orderId, 'reserve-inventory');
  const ok =
    first === 'compensating' &&
    status === 'failed' &&
    adapters.inventory.applyCount(compKey) === 1 &&
    isConsistent(ORDER, adapters, status);
  report('lost-receipt-compensation', ok,
    `first=${first} ` + describe(dir, adapters, status) +
    ` releaseApplies=${adapters.inventory.applyCount(compKey)}`);
}

// --- Scenario 5: transient compensation failure, then recovery -------------
{
  const dir = freshDir();
  const adapters = makeAdapters();
  adapters.payment.injectFault('reject');
  adapters.inventory.injectFault('transient', 'release');
  const first = run(dir, adapters);
  const midReserved = snapshot(ORDER, adapters).reserved;
  const status = resume(dir, adapters);
  const ok =
    first === 'compensating' && midReserved === 2 && status === 'failed' &&
    isConsistent(ORDER, adapters, status);
  report('compensation-failure-then-recovery', ok,
    `first=${first} reservedWhileCompensating=${midReserved} ` + describe(dir, adapters, status));
}

// --- Scenario 6: duplicate cancel + cancel/success race --------------------
{
  const dirA = freshDir();
  const adaptersA = makeAdapters();
  const execA = new Executor(new Journal(join(dirA, 'flow.journal')));
  execA.cancel(ORDER.orderId);
  execA.cancel(ORDER.orderId); // duplicate
  const statusA = execA.run(buildOrderFlow(ORDER, adaptersA));
  const lateCancel = execA.cancel(ORDER.orderId);
  const terminalsA = execA.journal.records.filter((r) => r.type === 'terminal').length;
  const okA =
    statusA === 'cancelled' && lateCancel === 'cancelled' && terminalsA === 1 &&
    isConsistent(ORDER, adaptersA, statusA);

  const dirB = freshDir();
  const adaptersB = makeAdapters();
  const execB = new Executor(new Journal(join(dirB, 'flow.journal')));
  const statusB = execB.run(buildOrderFlow(ORDER, adaptersB));
  const raceCancel = execB.cancel(ORDER.orderId); // loses the race
  const terminalsB = execB.journal.records.filter((r) => r.type === 'terminal').length;
  const okB =
    statusB === 'completed' && raceCancel === 'completed' && terminalsB === 1 &&
    isConsistent(ORDER, adaptersB, statusB);

  report('duplicate-cancel-and-race', okA && okB,
    `cancelledFlow={terminal=${statusA} terminals=${terminalsA}} ` +
    `completedFlow={terminal=${statusB} terminals=${terminalsB} cancelResult=${raceCancel}}`);
}

// --- Scenario 7: torn tail truncated ---------------------------------------
{
  const dir = freshDir();
  const adapters = makeAdapters();
  run(dir, adapters);
  appendFileSync(join(dir, 'flow.journal'), '{"seq":9,"type":"term');
  const journal = new Journal(join(dir, 'flow.journal'));
  const status = resume(dir, adapters);
  const ok =
    journal.truncatedBytes > 0 && status === 'completed' &&
    isConsistent(ORDER, adapters, status);
  report('torn-tail-truncated', ok,
    `truncatedBytes=${journal.truncatedBytes} ` + describe(dir, adapters, status));
}

// --- Scenario 8: complete-but-corrupt record must error --------------------
{
  const dir = freshDir();
  const adapters = makeAdapters();
  run(dir, adapters);
  appendFileSync(join(dir, 'flow.journal'), '{"seq":9,"type":"terminal","state":"x"}\n');
  let threw = false;
  try {
    new Journal(join(dir, 'flow.journal'));
  } catch (err) {
    threw = err instanceof JournalCorruptionError;
  }
  report('corrupt-complete-record-errors', threw, `JournalCorruptionError=${threw}`);
}

console.log(failures === 0 ? 'ALL SCENARIOS PASSED' : `${failures} SCENARIO(S) FAILED`);
process.exit(failures === 0 ? 0 : 1);
