import { test } from 'node:test';
import assert from 'node:assert/strict';
import { VirtualClock } from '../src/clock.ts';

test('timers fire in time order, then insertion order', () => {
  const clock = new VirtualClock();
  const fired: string[] = [];
  clock.setTimeout(() => fired.push('b@100'), 100);
  clock.setTimeout(() => fired.push('a@50'), 50);
  clock.setTimeout(() => fired.push('c@100'), 100);
  clock.advanceBy(100);
  assert.deepEqual(fired, ['a@50', 'b@100', 'c@100']);
  assert.equal(clock.now(), 100);
});

test('timers scheduled while advancing fire within the same advance', () => {
  const clock = new VirtualClock();
  const fired: number[] = [];
  clock.setTimeout(() => {
    fired.push(10);
    clock.setTimeout(() => fired.push(20), 10);
  }, 10);
  clock.advanceBy(30);
  assert.deepEqual(fired, [10, 20]);
});

test('clearTimeout prevents firing', () => {
  const clock = new VirtualClock();
  const fired: string[] = [];
  const id = clock.setTimeout(() => fired.push('x'), 10);
  clock.clearTimeout(id);
  clock.advanceBy(50);
  assert.deepEqual(fired, []);
  assert.equal(clock.pendingCount, 0);
});

test('time only moves when advanced and never backwards', () => {
  const clock = new VirtualClock(1000);
  assert.equal(clock.now(), 1000);
  assert.throws(() => clock.advanceTo(500), RangeError);
  assert.equal(clock.now(), 1000);
});
