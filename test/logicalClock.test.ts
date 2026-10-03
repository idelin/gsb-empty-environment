import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LamportClock } from '../src/logicalClock.ts';

test('tick is strictly monotonic', () => {
  const c = new LamportClock();
  assert.equal(c.tick(), 1);
  assert.equal(c.tick(), 2);
  assert.equal(c.now(), 2);
});

test('merge takes max(local, remote) + 1', () => {
  const c = new LamportClock();
  c.tick();
  c.tick();
  assert.equal(c.merge(5), 6);
  assert.equal(c.merge(2), 7);
});
