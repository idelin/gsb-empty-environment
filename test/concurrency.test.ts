import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LatchManager } from '../src/txn.ts';
import { runInterleaved } from '../src/fault.ts';

test('latch manager enforces mutual exclusion', () => {
  const lm = new LatchManager();
  lm.acquire(1, 100);
  assert.throws(() => lm.acquire(1, 200), /latch conflict/);
  assert.equal(lm.tryAcquire(1, 200), false);
  assert.equal(lm.tryAcquire(1, 100), true, 're-entrant for owner');
  lm.release(1, 100);
  assert.equal(lm.outstanding, 0);
  lm.acquire(1, 200);
  lm.release(1, 200);
  lm.assertNoLeaks();
});

test('release by non-owner is a violation', () => {
  const lm = new LatchManager();
  lm.acquire(7, 1);
  assert.throws(() => lm.release(7, 2), /release violation/);
  assert.throws(() => lm.release(99, 1), /release violation/);
});

test('interleaved workers across many seeds: serializable, zero leaks', () => {
  for (let seed = 1; seed <= 20; seed++) {
    const r = runInterleaved(seed, 6, 150, true);
    assert.equal(r.finalValue, r.expectedValue, `seed ${seed}`);
    assert.equal(r.latchLeaks, 0, `seed ${seed} latch leak`);
  }
});
