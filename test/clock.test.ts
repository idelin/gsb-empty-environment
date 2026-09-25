import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Clock } from '../src/clock.ts';

function fakeClock() {
  let wall = 0;
  const clock = new Clock(() => wall);
  return {
    clock,
    advance(amount: number) {
      wall += amount;
    },
  };
}

test('pause freezes time; wall time during pause never leaks in', () => {
  const { clock, advance } = fakeClock();
  clock.start();
  advance(10);
  assert.equal(clock.now(), 10);
  clock.pause();
  advance(1e9);
  assert.equal(clock.now(), 10);
  clock.resume();
  advance(5);
  assert.equal(clock.now(), 15);
});

test('repeated pause/resume cycles accumulate no drift', () => {
  const { clock, advance } = fakeClock();
  clock.start();
  for (let i = 0; i < 100; i++) {
    advance(1);
    clock.pause();
    advance(1000); // paused wall time must not count
    clock.resume();
  }
  clock.pause();
  assert.equal(clock.now(), 100);
});

test('pause/resume are idempotent', () => {
  const { clock, advance } = fakeClock();
  clock.start();
  advance(3);
  clock.pause();
  clock.pause();
  advance(7);
  clock.resume();
  clock.resume();
  advance(2);
  assert.equal(clock.now(), 5);
});
