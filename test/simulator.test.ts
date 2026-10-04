import test from 'node:test';
import assert from 'node:assert/strict';
import { Simulator } from '../src/simulator.ts';
import { ALL_BOUNDARIES, Boundary, CrashError } from '../src/faults.ts';
import type { FaultPhase } from '../src/faults.ts';
import type { OrderEvent } from '../src/types.ts';
import { assertInvariants } from './invariants.ts';

const EVENTS: OrderEvent[] = [
  { eventId: 'evt-1', orderId: 'order-1', status: 'PAID', amount: 100 },
  { eventId: 'evt-2', orderId: 'order-2', status: 'PAID', amount: 250 },
  { eventId: 'evt-3', orderId: 'order-1', status: 'SHIPPED', amount: 100 },
];

test('baseline: no faults, system reaches quiescence with invariants', () => {
  const sim = new Simulator(EVENTS);
  sim.runToQuiescence();
  assert.equal(sim.stats.crashes, 0);
  assertInvariants(sim, EVENTS);
});

test('duplicate order events are applied only once', () => {
  const duplicated = [...EVENTS, EVENTS[0], EVENTS[2], EVENTS[0]];
  const sim = new Simulator(duplicated);
  sim.runToQuiescence();
  assert.equal(sim.stats.eventRedeliveries, 3);
  assertInvariants(sim, EVENTS);
});

// 逐个持久化边界注入故障：before = 写入丢失；after = 写入已持久但进程随即崩溃。
for (const point of ALL_BOUNDARIES) {
  for (const phase of ['before', 'after'] as FaultPhase[]) {
    test(`fault injection: crash at ${point} (${phase})`, () => {
      const sim = new Simulator(EVENTS);
      sim.runWithCrashAt(point, phase);
      assert.equal(sim.stats.crashes, 1, 'injected fault must have fired exactly once');
      assertInvariants(sim, EVENTS);
    });
  }
}

test('lost delivery ack causes redelivery, receiver dedupes by notification id', () => {
  const sim = new Simulator(EVENTS);
  // 确认（markSent）持久化之前崩溃：通知已投递但确认丢失。
  sim.runWithCrashAt(Boundary.RelayMarkSent, 'before');
  assert.ok(
    sim.notificationReceiver.duplicates >= 1,
    'receiver must have deduplicated at least one redelivered notification',
  );
  assertInvariants(sim, EVENTS);
});

test('commit durable but event ack lost: event redelivered, order not updated twice', () => {
  const sim = new Simulator(EVENTS);
  sim.runWithCrashAt(Boundary.TxnCommit, 'after');
  assert.ok(sim.stats.eventRedeliveries >= 1, 'unacked event must be redelivered');
  assertInvariants(sim, EVENTS);
});

test('chaos: repeated random crashes never lose notifications nor double-apply orders', () => {
  // 确定性伪随机（mulberry32），保证可复现。
  let seed = 0xC0FFEE;
  const random = (): number => {
    seed |= 0;
    seed = (seed + 0x6D2B79F5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  const phases: FaultPhase[] = ['before', 'after'];

  const sim = new Simulator(EVENTS);
  for (let round = 0; round < 40; round += 1) {
    const point = ALL_BOUNDARIES[Math.floor(random() * ALL_BOUNDARIES.length)];
    const phase = phases[Math.floor(random() * phases.length)];
    sim.faults.arm(point, phase);
    try {
      sim.runToQuiescence();
      return assertInvariants(sim, EVENTS); // 提前静止则结束
    } catch (error) {
      assert.ok(error instanceof CrashError, `unexpected error: ${error}`);
      sim.crashAndRestart();
    }
  }
  sim.faults.disarm();
  sim.runToQuiescence();
  assertInvariants(sim, EVENTS);
});
