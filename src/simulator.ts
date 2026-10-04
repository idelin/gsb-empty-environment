import { CrashError, FaultInjector, TransactionLog } from './log';
import { Channel, OrderEvent, OrderSystem, Receiver } from './system';

/**
 * Scenario: order events are delivered at-least-once (each one twice).
 * The driver is fully idempotent, so after a crash it is simply re-run.
 */
const EVENTS: OrderEvent[] = [
  { eventId: 'ev-1', orderId: 'order-A', amount: 100 },
  { eventId: 'ev-2', orderId: 'order-A', amount: 250 },
  { eventId: 'ev-3', orderId: 'order-B', amount: 75 },
];

function expectedTotals(): Map<string, number> {
  const totals = new Map<string, number>();
  for (const e of EVENTS) totals.set(e.orderId, (totals.get(e.orderId) || 0) + e.amount);
  return totals;
}

function driver(system: OrderSystem, channel: Channel, receiver: Receiver): void {
  for (let round = 0; round < 2; round++) {
    for (const event of EVENTS) system.processEvent(event); // duplicate delivery
  }
  while (system.pendingCount() > 0) {
    system.dispatchPending(channel, receiver); // redelivers until every ack persists
  }
}

interface Run {
  system: OrderSystem;
  receiver: Receiver;
  log: TransactionLog;
  crashes: number;
}

function runScenario(
  crashAtDurableCount: number | null,
  dropAckAttempts: number[],
  dropDeliveryAttempts: number[],
): Run {
  const injector = new FaultInjector();
  const log = new TransactionLog(injector);
  const system = new OrderSystem(log);
  const receiver = new Receiver();
  const channel = new Channel();
  if (dropAckAttempts.length > 0) channel.dropAckOnAttempts(...dropAckAttempts);
  if (dropDeliveryAttempts.length > 0) channel.dropDeliveryOnAttempts(...dropDeliveryAttempts);
  if (crashAtDurableCount !== null) injector.armCrashWhenDurableCountReaches(crashAtDurableCount);

  let crashes = 0;
  for (;;) {
    try {
      driver(system, channel, receiver);
      break;
    } catch (err) {
      if (err instanceof CrashError) {
        crashes++;
        system.crashAndRecover();
        continue;
      }
      throw err;
    }
  }

  // One final crash to prove the finished state is fully durable.
  system.crashAndRecover();
  return { system, receiver, log, crashes };
}

// ---------- invariant checking ----------

let failures = 0;
let checks = 0;

function check(condition: boolean, message: string): void {
  checks++;
  if (!condition) {
    failures++;
    console.error(`  FAIL: ${message}`);
  }
}

function assertInvariants(run: Run, label: string): void {
  const { system, receiver } = run;

  check(
    system.businessApplies === EVENTS.length,
    `[${label}] order update applied exactly once per event (got ${system.businessApplies}, want ${EVENTS.length})`,
  );
  check(
    system.appliedEventCount() === EVENTS.length,
    `[${label}] every event marked applied exactly once`,
  );
  for (const [orderId, total] of expectedTotals()) {
    check(
      system.orderTotal(orderId) === total,
      `[${label}] ${orderId} total = ${system.orderTotal(orderId)}, want ${total}`,
    );
  }

  const wantIds = EVENTS.map(e => `ntf:${e.eventId}`).sort();
  const gotIds = receiver.applied.map(n => n.notificationId).sort();
  check(
    JSON.stringify(gotIds) === JSON.stringify(wantIds),
    `[${label}] receiver applied each notification exactly once (got ${gotIds.join(',')})`,
  );
  check(
    system.pendingCount() === 0,
    `[${label}] no notification permanently stuck in the outbox (${system.pendingCount()} pending)`,
  );
}

// ---------- harness ----------

function main(): void {
  // Baseline: no faults. Also measures the number of log appends (= boundaries).
  const baseline = runScenario(null, [], []);
  assertInvariants(baseline, 'baseline');
  const totalAppends = baseline.log.size;
  console.log(`baseline: ${totalAppends} log appends -> ${totalAppends + 1} crash boundaries`);

  // Phase 1: crash at every single persistence boundary.
  // Boundaries 0..totalAppends-1 are injected mid-run; the last boundary
  // (crash after the final append) is covered by the final crashAndRecover()
  // that every scenario performs before invariant checking.
  for (let k = 0; k < totalAppends; k++) {
    const run = runScenario(k, [], []);
    check(run.crashes === 1, `[crash@${k}] exactly one crash injected (got ${run.crashes})`);
    assertInvariants(run, `crash@${k}`);
  }
  console.log(`phase 1: crash injected at all ${totalAppends} mid-run persistence boundaries (+ final boundary via post-run crash)`);

  // Phase 2: no crashes; lose individual acks / deliveries (receiver must dedup).
  const baselineAttempts = EVENTS.length; // one delivery attempt per notification
  for (let j = 1; j <= baselineAttempts; j++) {
    assertInvariants(runScenario(null, [j], []), `ack-lost@${j}`);
    assertInvariants(runScenario(null, [], [j]), `delivery-lost@${j}`);
  }
  assertInvariants(runScenario(null, [1, 2], []), 'ack-lost@1,2');
  console.log(`phase 2: ack loss and delivery loss injected per attempt`);

  // Phase 3: combined — crash at every boundary while acks are being lost.
  for (let k = 0; k < totalAppends; k++) {
    assertInvariants(runScenario(k, [1, 2], []), `crash@${k}+ack-lost@1,2`);
  }
  console.log(`phase 3: crash x ack-loss cross product verified`);

  console.log(`\n${checks} checks, ${failures} failure(s)`);
  if (failures > 0) process.exit(1);
  console.log('OK: orders updated exactly once; no notification permanently lost.');
}

main();
