import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runWorkload, diffTraces, TraceRecorder, Simulator } from '../src/index.ts';

test('recorded trace replays identically (deterministic regression)', () => {
  const a = runWorkload({ events: 20_000, seed: 2024 });
  const b = runWorkload({ events: 20_000, seed: 2024 });
  assert.equal(a.checksum, b.checksum);
  assert.equal(diffTraces(a.trace, b.trace), null);
});

test('different seeds diverge', () => {
  const a = runWorkload({ events: 5_000, seed: 1 });
  const b = runWorkload({ events: 5_000, seed: 2 });
  assert.notEqual(a.checksum, b.checksum);
});

test('fault reproduction pinpoints first divergent event', () => {
  const good = runWorkload({ events: 50_000, seed: 42 });
  const buggy = runWorkload({ events: 50_000, seed: 42, buggy: true });
  const div = diffTraces(good.trace, buggy.trace);
  assert.ok(div, 'expected a divergence');
  assert.equal(div.reason, 'entry mismatch');
  assert.equal(div.index, 1);
  assert.equal(div.expected?.time, 2);
  assert.equal(div.actual?.time, 1);
  // prefix before divergence is identical
  assert.equal(diffTraces(good.trace.slice(0, div.index), buggy.trace.slice(0, div.index)), null);
});

test('diffTraces detects truncation and extra events', () => {
  const good = runWorkload({ events: 1_000, seed: 9 });
  const shorter = good.trace.slice(0, good.trace.length - 5);
  const div = diffTraces(good.trace, shorter);
  assert.equal(div?.reason, 'actual trace truncated');
  assert.equal(div?.index, good.trace.length - 5);
  const div2 = diffTraces(shorter, good.trace);
  assert.equal(div2?.reason, 'actual trace has extra events');
});

test('trace recorder without storage still checksums', () => {
  const sim = new Simulator(11);
  const rec = new TraceRecorder({ store: false });
  sim.schedule(0, 'a', () => {});
  sim.schedule(1, 'b', () => {});
  sim.run({ observer: rec.observer() });
  assert.equal(rec.entries.length, 0);
  assert.match(rec.checksum(), /^[0-9a-f]{64}$/);
  sim.dispose();
});
