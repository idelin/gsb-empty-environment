import test from 'node:test';
import assert from 'node:assert/strict';
import { Kernel } from '../src/kernel.ts';
import {
  buildScenario,
  KERNEL_SEED,
  TRACE_PACKETS,
  TRACE_TIMEOUTS,
} from '../src/scenario.ts';

test('trace records 50 packet arrivals and 3 timeouts', () => {
  const kernel = buildScenario();
  const counts = { packet: 0, timeout: 0, reseed: 0 };
  for (const e of kernel.eventLog) counts[e.kind] += 1;
  assert.equal(counts.packet, TRACE_PACKETS);
  assert.equal(counts.timeout, TRACE_TIMEOUTS);
  assert.equal(kernel.eventCount, 55);
  // Log is totally ordered by (tick, seq).
  for (let i = 1; i < kernel.eventCount; i += 1) {
    const prev = kernel.eventLog[i - 1];
    const curr = kernel.eventLog[i];
    assert.ok(prev.tick < curr.tick || (prev.tick === curr.tick && prev.seq < curr.seq));
  }
});

test('acceptance: replay to event 35 matches the historical snapshot byte-for-byte', () => {
  const kernel = buildScenario();
  const recorded = kernel.snapshots[34]; // snapshot after the 35th event
  const traveled = kernel.travelTo(35);
  assert.equal(traveled.hash, recorded.hash);
  assert.ok(traveled.bytes.equals(recorded.bytes), 'state bytes must be identical');
  const result = kernel.verifyAt(35);
  assert.ok(result.match, `expected ${result.expected}, got ${result.actual}`);
});

test('every historical tick reproduces byte-identically', () => {
  const kernel = buildScenario();
  for (let i = 1; i <= kernel.eventCount; i += 1) {
    const result = kernel.verifyAt(i);
    assert.ok(result.match, `mismatch at event #${i}`);
  }
});

test('time travel backward then resume reproduces the final state', () => {
  const kernel = buildScenario();
  const finalHash = kernel.snapshots[kernel.snapshots.length - 1].hash;
  kernel.travelTo(20); // jump backward
  kernel.resume(); // re-dispatch remaining recorded events
  const traveled = kernel.travelTo(kernel.eventCount);
  assert.equal(traveled.hash, finalHash);
});

test('recording is deterministic across independent runs', () => {
  const a = buildScenario();
  const b = buildScenario();
  assert.equal(a.snapshots.length, b.snapshots.length);
  for (let i = 0; i < a.snapshots.length; i += 1) {
    assert.equal(a.snapshots[i].hash, b.snapshots[i].hash);
  }
});

test('the event log is the source of truth: tampering changes replayed state', () => {
  const kernel = buildScenario();
  const original = kernel.verifyAt(35);
  assert.ok(original.match);

  const tampered = new Kernel(KERNEL_SEED);
  for (const e of kernel.eventLog) {
    const copy = structuredClone(e);
    if (copy.kind === 'packet' && copy.payload.id === 30) {
      copy.payload.size += 1; // flip one bit of history
    }
    tampered.inject(copy);
  }
  tampered.run();
  assert.notEqual(tampered.snapshots[34].hash, kernel.snapshots[34].hash);
});
