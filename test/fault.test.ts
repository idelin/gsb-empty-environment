import { test } from 'node:test';
import assert from 'node:assert/strict';
import { recordTrace, reproduce, runInterleaved } from '../src/fault.ts';
import type { SimConfig } from '../src/simulation.ts';

const config: SimConfig = {
  seed: 777,
  steps: 50_000,
  maxActiveTxns: 10,
  numPages: 64,
  checkpointEvery: 1_000,
  crashAt: [12_345, 33_333],
};

test('fault reproduction: recorded trace replays bit-identically', () => {
  const trace = recordTrace(config);
  const repro = reproduce(trace);
  assert.ok(repro.ok, `expected ${repro.expectedHash}, got ${repro.actualHash}`);
  assert.deepEqual(repro.result.mismatches, []);
});

test('tampered trace is detected (fault not reproducible => mismatch flagged)', () => {
  const trace = recordTrace(config);
  const tampered = reproduce({ config: { ...trace.config, seed: 778 }, hash: trace.hash });
  assert.equal(tampered.ok, false);
});

test('lost-update bug reproduced without latches, absent with latches', () => {
  const buggy = runInterleaved(5, 8, 200, false);
  assert.ok(buggy.bugReproduced, 'unlatched read-modify-write must lose updates');
  assert.ok(buggy.finalValue < buggy.expectedValue);

  const safe = runInterleaved(5, 8, 200, true);
  assert.equal(safe.bugReproduced, false);
  assert.equal(safe.finalValue, safe.expectedValue);
  assert.equal(safe.latchLeaks, 0, 'no latch leaks');
});

test('fault injection inside critical section leaves no latch leak', () => {
  const r = runInterleaved(9, 6, 100, true, { worker: 3, iter: 50 });
  assert.equal(r.latchLeaks, 0, 'try/finally must release latch even on injected fault');
});
