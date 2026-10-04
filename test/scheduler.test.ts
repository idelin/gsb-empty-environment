import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Scheduler,
  CLASS_ORDER,
  type ClassConfig,
  type ClassName,
  type PacketSpec,
  type TickLedger,
} from '../src/scheduler.ts';
import { ReferenceScheduler } from '../src/reference.ts';

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

function cfg(partial: Partial<ClassConfig> = {}): ClassConfig {
  return {
    tokenRatePerTick: 1_000_000,
    tokenBurstBytes: 1_000_000,
    maxQueueBytes: 10_000_000,
    weight: 1,
    ...partial,
  };
}

function generousClasses(): Record<ClassName, ClassConfig> {
  return { voice: cfg(), interactive: cfg(), bulk: cfg() };
}

function assertLedgerInvariants(ledger: TickLedger): void {
  const sum = CLASS_ORDER.reduce((s, c) => s + ledger.sentByClass[c], 0);
  assert.equal(sum, ledger.totalSent, `tick ${ledger.tick}: per-class sum != totalSent`);
  assert.ok(
    ledger.totalSent <= ledger.linkCapacity,
    `tick ${ledger.tick}: sent ${ledger.totalSent} exceeds link capacity ${ledger.linkCapacity}`,
  );
  for (const c of CLASS_ORDER) {
    assert.ok(ledger.tokensAfter[c] >= 0, `tick ${ledger.tick}: negative tokens for ${c}`);
    assert.ok(ledger.queuedBytesAfter[c] >= 0, `tick ${ledger.tick}: negative queue for ${c}`);
    assert.ok(ledger.deficitAfter[c] >= 0, `tick ${ledger.tick}: negative deficit for ${c}`);
  }
}

test('large packet spans multiple ticks, completion reported once', () => {
  const sim = new Scheduler(300, generousClasses(), 1000);
  sim.submit({ id: 'big', arrivalTick: 0, bytes: 1000, className: 'voice' });
  const ledgers = sim.run(5);

  assert.deepEqual(
    ledgers.map((l) => l.totalSent),
    [300, 300, 300, 100, 0],
  );
  const completions = ledgers.flatMap((l) => l.completed);
  assert.equal(completions.length, 1);
  assert.equal(completions[0].packetId, 'big');
  assert.equal(completions[0].tick, 3);
  assert.equal(completions[0].latencyTicks, 3);
  for (const l of ledgers) assertLedgerInvariants(l);
});

test('initial tokens equal burst; empty bucket with zero rate stalls without drift', () => {
  const classes = generousClasses();
  classes.voice = cfg({ tokenRatePerTick: 0, tokenBurstBytes: 50 });
  const sim = new Scheduler(1000, classes, 1000);
  sim.submit({ id: 'p', arrivalTick: 0, bytes: 100, className: 'voice' });
  const ledgers = sim.run(5);
  assert.deepEqual(
    ledgers.map((l) => l.sentByClass.voice),
    [50, 0, 0, 0, 0],
  );
  assert.equal(ledgers.flatMap((l) => l.completed).length, 0);
  assert.equal(sim.queuedBytes('voice'), 50);
});

test('token bucket throttles service when tokens are scarce', () => {
  const classes = generousClasses();
  classes.voice = cfg({ tokenRatePerTick: 10, tokenBurstBytes: 20 });
  const sim = new Scheduler(10_000, classes, 1000);
  sim.submit({ id: 'p', arrivalTick: 0, bytes: 100, className: 'voice' });
  const ledgers = sim.run(9);

  // tick 0: full burst 20; ticks 1..8: 10 each -> 20 + 8*10 = 100
  assert.deepEqual(
    ledgers.map((l) => l.sentByClass.voice),
    [20, 10, 10, 10, 10, 10, 10, 10, 10],
  );
  const completions = ledgers.flatMap((l) => l.completed);
  assert.equal(completions.length, 1);
  assert.equal(completions[0].tick, 8);
  for (const l of ledgers) {
    assert.ok(l.tokensAfter.voice >= 0);
    assert.ok(l.sentByClass.voice <= 20);
  }
});

test('queue shrink keeps existing packets, rejects only new admissions', () => {
  const classes = generousClasses();
  classes.bulk = cfg({ maxQueueBytes: 1000 });
  const sim = new Scheduler(100, classes, 10_000);
  for (let i = 0; i < 8; i++) {
    sim.submit({ id: `b${i}`, arrivalTick: 0, bytes: 100, className: 'bulk' });
  }
  sim.scheduleReconfig({ tick: 2, className: 'bulk', patch: { maxQueueBytes: 150 } });
  sim.submit({ id: 'rejected', arrivalTick: 3, bytes: 100, className: 'bulk' });
  sim.submit({ id: 'accepted', arrivalTick: 8, bytes: 100, className: 'bulk' });

  const ledgers = sim.run(10);

  const drops = ledgers.flatMap((l) => l.dropped);
  assert.equal(drops.length, 1);
  assert.equal(drops[0].packetId, 'rejected');
  assert.equal(drops[0].reason, 'queue_overflow');
  assert.equal(drops[0].tick, 3);

  const completedIds = ledgers.flatMap((l) => l.completed).map((c) => c.packetId);
  for (let i = 0; i < 8; i++) assert.ok(completedIds.includes(`b${i}`), `b${i} must complete`);
  assert.ok(completedIds.includes('accepted'));
  assert.equal(completedIds.length, 9);
  for (const l of ledgers) assertLedgerInvariants(l);
});

test('duplicate id with identical content is idempotent; different content conflicts', () => {
  const sim = new Scheduler(1000, generousClasses(), 1000);
  const spec: PacketSpec = { id: 'x', arrivalTick: 0, bytes: 100, className: 'voice' };
  sim.submit(spec);
  sim.submit(spec);
  sim.submit({ id: 'x', arrivalTick: 0, bytes: 100, className: 'voice' });
  sim.submit({ id: 'x', arrivalTick: 0, bytes: 200, className: 'voice' });
  sim.submit({ id: 'x', arrivalTick: 1, bytes: 100, className: 'voice' });

  const ledgers = sim.run(2);
  assert.equal(sim.totals.admittedPackets, 1);
  const completions = ledgers.flatMap((l) => l.completed);
  assert.equal(completions.length, 1);
  assert.equal(completions[0].bytes, 100);

  const drops = ledgers.flatMap((l) => l.dropped);
  assert.equal(drops.length, 2);
  assert.ok(drops.every((d) => d.reason === 'id_conflict'));
  assert.equal(sim.totals.sentBytes, 100);
});

test('backlogged classes share the link by DRR weights (no strict-priority starvation)', () => {
  const classes = generousClasses();
  classes.voice = cfg({ weight: 2 });
  classes.bulk = cfg({ weight: 1 });
  const sim = new Scheduler(3000, classes, 100);
  // 400 x 500B = 200kB per class; the run moves ~180kB total, so both
  // classes stay backlogged for the whole measurement window.
  for (let i = 0; i < 400; i++) {
    sim.submit({ id: `v${i}`, arrivalTick: 0, bytes: 500, className: 'voice' });
    sim.submit({ id: `b${i}`, arrivalTick: 0, bytes: 500, className: 'bulk' });
  }
  const ledgers = sim.run(60);
  const sent = (c: ClassName) => ledgers.reduce((s, l) => s + l.sentByClass[c], 0);
  const voice = sent('voice');
  const bulk = sent('bulk');
  assert.ok(bulk > 0, 'bulk must make progress');
  const ratio = voice / bulk;
  assert.ok(ratio > 1.8 && ratio < 2.2, `expected ~2:1 share, got ${ratio}`);
  for (const l of ledgers) assertLedgerInvariants(l);
});

test('chunked scheduler matches byte-at-a-time reference on mixed workload', () => {
  const rand = mulberry32(42);
  const classes: Record<ClassName, ClassConfig> = {
    voice: cfg({ tokenRatePerTick: 150, tokenBurstBytes: 400, maxQueueBytes: 5000, weight: 3 }),
    interactive: cfg({ tokenRatePerTick: 200, tokenBurstBytes: 500, maxQueueBytes: 8000, weight: 2 }),
    bulk: cfg({ tokenRatePerTick: 300, tokenBurstBytes: 900, maxQueueBytes: 20000, weight: 1 }),
  };
  const link = 500;
  const quantum = 64;
  const main = new Scheduler(link, classes, quantum);
  const ref = new ReferenceScheduler(link, classes, quantum);

  const packets: PacketSpec[] = [];
  for (let i = 0; i < 250; i++) {
    const arrivalTick = Math.floor(rand() * 120);
    const bytes = rand() < 0.6 ? 1 + Math.floor(rand() * 80) : 800 + Math.floor(rand() * 800);
    const className = CLASS_ORDER[Math.floor(rand() * 3)];
    packets.push({ id: `p${i}`, arrivalTick, bytes, className });
  }
  packets.push({ ...packets[10] });
  packets.push({ ...packets[20], bytes: packets[20].bytes + 1 });

  for (const p of packets) {
    main.submit(p);
    ref.submit(p);
  }
  main.scheduleReconfig({ tick: 40, className: 'bulk', patch: { maxQueueBytes: 3000 } });
  ref.scheduleReconfig(40, 'bulk', { maxQueueBytes: 3000 });
  main.scheduleReconfig({ tick: 70, className: 'voice', patch: { weight: 1, tokenRatePerTick: 100 } });
  ref.scheduleReconfig(70, 'voice', { weight: 1, tokenRatePerTick: 100 });

  for (let t = 0; t < 400; t++) {
    const a = main.step();
    const b = ref.step();
    assert.deepEqual(a, b, `ledger mismatch at tick ${t}`);
    assertLedgerInvariants(a);
  }
  assert.ok(main.isIdle(), 'all packets drained within 400 ticks');
});

test('long-run load: byte conservation, exactly-once completion, no starvation', () => {
  const rand = mulberry32(7);
  const classes: Record<ClassName, ClassConfig> = {
    voice: cfg({ tokenRatePerTick: 250, tokenBurstBytes: 500, maxQueueBytes: 100_000, weight: 1 }),
    interactive: cfg({ tokenRatePerTick: 250, tokenBurstBytes: 500, maxQueueBytes: 100_000, weight: 1 }),
    bulk: cfg({ tokenRatePerTick: 400, tokenBurstBytes: 800, maxQueueBytes: 200_000, weight: 1 }),
  };
  const sim = new Scheduler(1000, classes, 100);

  let id = 0;
  for (let t = 0; t < 4000; t++) {
    const n = rand() < 0.8 ? 1 + Math.floor(rand() * 2) : 0;
    for (let k = 0; k < n; k++) {
      sim.submit({
        id: `p${id++}`,
        arrivalTick: t,
        bytes: 1 + Math.floor(rand() * 600),
        className: CLASS_ORDER[Math.floor(rand() * 3)],
      });
    }
  }

  const ledgers = sim.runUntilIdle(20_000);
  const seenCompletions = new Set<string>();
  let completedBytes = 0;
  for (const l of ledgers) {
    assertLedgerInvariants(l);
    for (const c of l.completed) {
      assert.ok(!seenCompletions.has(c.packetId), `duplicate completion for ${c.packetId}`);
      seenCompletions.add(c.packetId);
      completedBytes += c.bytes;
    }
  }

  assert.ok(sim.isIdle(), 'queues must drain under sustainable load (no starvation)');
  const t = sim.totals;
  assert.equal(t.admittedBytes, t.sentBytes);
  assert.equal(completedBytes, t.sentBytes);
  assert.equal(t.completedPackets, t.admittedPackets);
  assert.equal(t.submittedBytes, t.admittedBytes + t.droppedBytes);
  assert.equal(t.submittedPackets, t.admittedPackets + t.droppedPackets);
});

test('late arrivals and past reconfigs are rejected', () => {
  const sim = new Scheduler(100, generousClasses(), 100);
  sim.run(3);
  assert.throws(() => sim.submit({ id: 'late', arrivalTick: 2, bytes: 10, className: 'voice' }));
  assert.throws(() => sim.scheduleReconfig({ tick: 1, className: 'voice', patch: { weight: 2 } }));
});
