import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runSimulation } from '../src/simulation.ts';
import type { SimConfig } from '../src/simulation.ts';

const base: SimConfig = {
  seed: 42,
  steps: 20_000,
  maxActiveTxns: 8,
  numPages: 32,
  checkpointEvery: 500,
  crashAt: [3_000, 9_500, 15_000],
};

test('simulation is deterministic: same seed, same hash', () => {
  const a = runSimulation(base);
  const b = runSimulation(base);
  assert.equal(a.hash, b.hash);
  assert.deepEqual(a.stats, b.stats);
});

test('different seeds diverge', () => {
  const a = runSimulation(base);
  const b = runSimulation({ ...base, seed: 43 });
  assert.notEqual(a.hash, b.hash);
});

test('invariant: committed data durable, uncommitted rolled back after crashes', () => {
  for (const seed of [1, 7, 42, 1337, 9999]) {
    const r = runSimulation({ ...base, seed });
    assert.deepEqual(r.mismatches, [], `seed ${seed}: disk must equal committed model`);
    assert.ok(r.stats.crashes > 0);
    assert.ok(r.stats.recoveries > 0);
  }
});

test('no leaks: no open txns, no unflushed log, no dirty frames at end', () => {
  const r = runSimulation(base);
  assert.equal(r.openTxns, 0);
  assert.equal(r.unflushedLog, 0);
  assert.equal(r.dirtyFrames, 0);
});

test('million-step deterministic regression', () => {
  const r = runSimulation({
    seed: 2024,
    steps: 1_000_000,
    maxActiveTxns: 16,
    numPages: 128,
    checkpointEvery: 10_000,
    crashAt: [250_000, 500_000, 750_000],
  });
  assert.deepEqual(r.mismatches, []);
  assert.equal(r.openTxns, 0);
  assert.equal(r.unflushedLog, 0);
  assert.equal(r.dirtyFrames, 0);
  assert.equal(r.hash, GOLDEN_HASH, 'regression: hash drifted from golden value');
});

const GOLDEN_HASH = '0e7bf860';
