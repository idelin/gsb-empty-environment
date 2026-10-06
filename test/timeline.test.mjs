import { PiecewiseTimeMap } from '../dist/timeline.js';
import { test, eq } from './harness.mjs';

test('single segment maps linearly', () => {
  const m = new PiecewiseTimeMap({ startTime: 0, startLocal: 0, rate: 1 });
  eq(m.map(0), 0);
  eq(m.map(10), 10);
  eq(m.map(10.5), 10.5);
});

test('cut anchors local time and switches rate without a jump', () => {
  const m = new PiecewiseTimeMap({ startTime: 0, startLocal: 0, rate: 1 });
  m.cut(5, 0); // pause at local 5
  eq(m.map(10), 5);
  m.cut(10, 2); // resume at 2x from local 5
  eq(m.map(10), 5);
  eq(m.map(15), 15);
});

test('same-timestamp ops: last op wins, zero-length segment is erased', () => {
  const m = new PiecewiseTimeMap({ startTime: 0, startLocal: 0, rate: 1 });
  m.cut(5, 0); // pause at t=5
  m.cut(5, 1); // resume at the same instant
  eq(m.length, 2, 'segment count');
  eq(m.map(10), 10, 'mapping as if never paused');
});

test('same-timestamp ops observe earlier same-timestamp local time', () => {
  const m = new PiecewiseTimeMap({ startTime: 0, startLocal: 0, rate: 1 });
  m.reset(3, 100, 0); // seek to local 100 at t=3, paused
  m.cut(3, 1); // resume at the same instant: must keep local 100
  eq(m.map(3), 100);
  eq(m.map(8), 105);
});

test('out-of-order op truncates future segments', () => {
  const m = new PiecewiseTimeMap({ startTime: 0, startLocal: 0, rate: 1 });
  m.cut(5, 0);
  m.cut(3, 2); // rewrite history from t=3
  eq(m.length, 2, 'segment count');
  eq(m.map(8), 13); // local 3 at t=3, then 2x for 5ms
});

test('reset anchors an explicit local time (seek)', () => {
  const m = new PiecewiseTimeMap({ startTime: 0, startLocal: 0, rate: 1 });
  m.reset(4, 100, 1);
  eq(m.map(4), 100);
  eq(m.map(6), 102);
});

test('timeAtLocal inverts the mapping on the latest matching segment', () => {
  const m = new PiecewiseTimeMap({ startTime: 0, startLocal: 0, rate: 2 });
  eq(m.timeAtLocal(10), 5);
  m.cut(10, 1); // local 20 at t=10
  eq(m.timeAtLocal(25), 15);
  eq(m.timeAtLocal(20), 10, 'boundary belongs to the later segment');
});

test('timeAtLocal returns NaN for unreachable local times', () => {
  const m = new PiecewiseTimeMap({ startTime: 0, startLocal: 0, rate: 1 });
  m.cut(10, 0); // frozen at local 10 from t=10 on
  eq(Number.isNaN(m.timeAtLocal(50)), true);
});
