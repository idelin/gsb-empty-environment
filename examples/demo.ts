/**
 * Runnable demo: `npm run demo` (or `node examples/demo.ts`).
 *
 * Scenario: a 1500 B/tick link shared by voice / interactive / bulk.
 * Deterministic pseudo-random traffic, a duplicate and a conflicting packet,
 * and a mid-run reconfiguration that shrinks the bulk queue limit.
 */

import { Scheduler, CLASS_ORDER, type ClassConfig, type ClassName } from '../src/scheduler.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const classes: Record<ClassName, ClassConfig> = {
  voice: { tokenRatePerTick: 400, tokenBurstBytes: 800, maxQueueBytes: 4_000, weight: 4 },
  interactive: { tokenRatePerTick: 500, tokenBurstBytes: 1000, maxQueueBytes: 8_000, weight: 3 },
  bulk: { tokenRatePerTick: 600, tokenBurstBytes: 1200, maxQueueBytes: 20_000, weight: 1 },
};

const sim = new Scheduler(1500, classes, 128);

const rand = mulberry32(20261004);
let id = 0;
for (let t = 0; t < 60; t++) {
  const n = Math.floor(rand() * 4); // 0..3 arrivals per tick
  for (let k = 0; k < n; k++) {
    const className = CLASS_ORDER[Math.floor(rand() * 3)];
    const bytes =
      className === 'voice'
        ? 60 + Math.floor(rand() * 120) // small
        : className === 'interactive'
          ? 100 + Math.floor(rand() * 400) // medium
          : 500 + Math.floor(rand() * 2500); // large
    sim.submit({ id: `pkt-${id++}`, arrivalTick: t, bytes, className });
  }
}

// Idempotency / conflict showcase at tick 5.
sim.submit({ id: 'dup', arrivalTick: 5, bytes: 200, className: 'voice' });
sim.submit({ id: 'dup', arrivalTick: 5, bytes: 200, className: 'voice' }); // ignored
sim.submit({ id: 'dup', arrivalTick: 5, bytes: 999, className: 'voice' }); // id_conflict

// At tick 30 the bulk queue limit shrinks to 2 kB: queued packets stay,
// only new admissions are rejected while usage exceeds the new limit.
sim.scheduleReconfig({ tick: 30, className: 'bulk', patch: { maxQueueBytes: 2000 } });

console.log('tick |  sent (V/I/B)  | total | tokens (V/I/B)      | queued (V/I/B)     | events');
console.log('-----+----------------+-------+---------------------+--------------------+-------');

const ledgers = sim.runUntilIdle(500);
for (const l of ledgers) {
  const s = CLASS_ORDER.map((c) => String(l.sentByClass[c]).padStart(4)).join('/');
  const tok = CLASS_ORDER.map((c) => String(l.tokensAfter[c]).padStart(5)).join('/');
  const q = CLASS_ORDER.map((c) => String(l.queuedBytesAfter[c]).padStart(5)).join('/');
  const events = [
    ...l.completed.map((c) => `done:${c.packetId}(+${c.latencyTicks}t)`),
    ...l.dropped.map((d) => `drop:${d.packetId}:${d.reason}`),
  ].join(' ');
  console.log(
    `${String(l.tick).padStart(4)} | ${s} | ${String(l.totalSent).padStart(5)} | ${tok} | ${q} | ${events}`,
  );
}

const t = sim.totals;
console.log('\n--- totals ---');
console.log(`submitted: ${t.submittedPackets} pkts / ${t.submittedBytes} B`);
console.log(`admitted : ${t.admittedPackets} pkts / ${t.admittedBytes} B`);
console.log(`dropped  : ${t.droppedPackets} pkts / ${t.droppedBytes} B`);
console.log(`completed: ${t.completedPackets} pkts, sent ${t.sentBytes} B`);
const overCapacity = ledgers.filter((l) => l.totalSent > l.linkCapacity).length;
console.log(`capacity check: ${overCapacity === 0 ? 'OK (never exceeded)' : 'VIOLATED'}`);
