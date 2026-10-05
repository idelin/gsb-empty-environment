import assert from 'node:assert/strict';
import { test } from './harness.mjs';
import { fracToString } from '../dist/fraction.js';
import {
  normalizeSegment,
  normalizeBox,
  normalizeWindow,
  InvalidSegmentError,
} from '../dist/geometry.js';
import { intersectSegmentBox, positionAt } from '../dist/intersect.js';

function seg(over) {
  const base = {
    objectId: 'obj',
    segmentId: 's',
    t0: 0,
    t1: 10,
    p0: { x: 0, y: 0, z: 0 },
    p1: { x: 0, y: 0, z: 0 },
  };
  return normalizeSegment(Object.assign(base, over));
}

function box(x0, y0, z0, x1, y1, z1) {
  return normalizeBox({ lo: { x: x0, y: y0, z: z0 }, hi: { x: x1, y: y1, z: z1 } });
}

function win(t0, t1) {
  return normalizeWindow({ t0, t1 });
}

function fmt(iv) {
  return fracToString(iv.start) + ' .. ' + fracToString(iv.end) + (iv.endInclusive ? ' (incl)' : ' (excl)');
}

test('stationary segment inside box hits for its whole [t0, t1)', () => {
  const s = seg({ p0: { x: 1, y: 2, z: 3 }, p1: { x: 1, y: 2, z: 3 } });
  const iv = intersectSegmentBox(s, box(0, 0, 0, 5, 5, 5), win(0, 100));
  assert.equal(fmt(iv), '0 .. 10 (excl)');
});

test('stationary segment outside box misses', () => {
  const s = seg({ p0: { x: 9, y: 2, z: 3 }, p1: { x: 9, y: 2, z: 3 } });
  assert.equal(intersectSegmentBox(s, box(0, 0, 0, 5, 5, 5), win(0, 100)), null);
});

test('stationary segment exactly on closed box boundary hits', () => {
  const s = seg({ p0: { x: 0, y: 0, z: 0 }, p1: { x: 0, y: 0, z: 0 } });
  const iv = intersectSegmentBox(s, box(0, 0, 0, 5, 5, 5), win(0, 100));
  assert.equal(fmt(iv), '0 .. 10 (excl)');
});

test('zero-duration segment is illegal', () => {
  assert.throws(
    () => seg({ t0: 5, t1: 5 }),
    (e) => e instanceof InvalidSegmentError && /zero-duration/.test(e.message)
  );
});

test('segment with t0 > t1 is illegal', () => {
  assert.throws(() => seg({ t0: 9, t1: 5 }), InvalidSegmentError);
});

test('non-integer coordinates and ticks are rejected', () => {
  assert.throws(() => seg({ p0: { x: 1.5, y: 0, z: 0 } }));
  assert.throws(() => seg({ t0: 0.5 }));
});

test('crossing box with both endpoints outside gives interior interval', () => {
  const s = seg({ t0: 0, t1: 30, p0: { x: -10, y: 1, z: 1 }, p1: { x: 20, y: 1, z: 1 } });
  const iv = intersectSegmentBox(s, box(0, 0, 0, 10, 5, 5), win(0, 100));
  assert.equal(fmt(iv), '10 .. 20 (incl)');
});

test('fractional enter/exit times are exact rationals', () => {
  const s = seg({ t0: 0, t1: 2, p0: { x: 0, y: 0, z: 0 }, p1: { x: 3, y: 0, z: 0 } });
  const iv = intersectSegmentBox(s, box(1, -5, -5, 2, 5, 5), win(0, 100));
  assert.equal(fmt(iv), '2/3 .. 4/3 (incl)');
});

test('grazing tangential touch yields a point hit', () => {
  const s = seg({ t0: 0, t1: 10, p0: { x: 0, y: 0, z: 0 }, p1: { x: 10, y: 10, z: 0 } });
  const iv = intersectSegmentBox(s, box(0, 5, 0, 5, 10, 1), win(0, 100));
  assert.equal(fmt(iv), '5 .. 5 (incl)');
});

test('near-graze that misses yields no hit', () => {
  const s = seg({ t0: 0, t1: 10, p0: { x: 0, y: 0, z: 0 }, p1: { x: 10, y: 10, z: 0 } });
  assert.equal(intersectSegmentBox(s, box(0, 5, 0, 4, 10, 1), win(0, 100)), null);
});

test('query window touching only the right-open segment end misses', () => {
  const s = seg({ t0: 10, t1: 20, p0: { x: 1, y: 1, z: 1 }, p1: { x: 1, y: 1, z: 1 } });
  const b = box(0, 0, 0, 5, 5, 5);
  assert.equal(intersectSegmentBox(s, b, win(0, 10)), null, 'window ends where segment starts');
  assert.equal(intersectSegmentBox(s, b, win(20, 30)), null, 'window starts where segment ends');
  assert.equal(fmt(intersectSegmentBox(s, b, win(10, 20))), '10 .. 20 (excl)');
  assert.equal(fmt(intersectSegmentBox(s, b, win(15, 25))), '15 .. 20 (excl)');
  assert.equal(fmt(intersectSegmentBox(s, b, win(5, 15))), '10 .. 15 (excl)');
});

test('hit interval is clipped to the query window', () => {
  const s = seg({ t0: 0, t1: 100, p0: { x: 0, y: 0, z: 0 }, p1: { x: 100, y: 0, z: 0 } });
  const b = box(-1000, -1, -1, 1000, 1, 1);
  assert.equal(fmt(intersectSegmentBox(s, b, win(10, 40))), '10 .. 40 (excl)');
});

test('hit ending exactly at window start still counts (closed box, half-open window)', () => {
  // Segment exits the box at t=5 inclusive; window [5, 10) contains t=5.
  const s = seg({ t0: 0, t1: 10, p0: { x: 0, y: 0, z: 0 }, p1: { x: 10, y: 0, z: 0 } });
  const iv = intersectSegmentBox(s, box(-5, -1, -1, 5, 1, 1), win(5, 10));
  assert.equal(fmt(iv), '5 .. 5 (incl)');
});

test('hit ending exactly at window end is excluded (right-open)', () => {
  // Segment exits box at t=5; window [0, 5) must not include t=5.
  const s = seg({ t0: 0, t1: 10, p0: { x: 0, y: 0, z: 0 }, p1: { x: 10, y: 0, z: 0 } });
  const iv = intersectSegmentBox(s, box(-5, -1, -1, 5, 1, 1), win(0, 5));
  assert.equal(fmt(iv), '0 .. 5 (excl)');
});

test('positionAt evaluates the linear trajectory exactly', () => {
  const s = seg({ t0: 0, t1: 2, p0: { x: 0, y: 0, z: 0 }, p1: { x: 3, y: -3, z: 6 } });
  const p = positionAt(s, { n: 2n, d: 3n });
  assert.equal(fracToString(p.x), '1');
  assert.equal(fracToString(p.y), '-1');
  assert.equal(fracToString(p.z), '2');
});

test('negative direction motion solves enter/exit correctly', () => {
  const s = seg({ t0: 0, t1: 10, p0: { x: 20, y: 0, z: 0 }, p1: { x: -10, y: 0, z: 0 } });
  const iv = intersectSegmentBox(s, box(0, -1, -1, 9, 1, 1), win(0, 100));
  // x(t) = 20 - 3t; enters x<=9 at t=11/3, leaves x<0 at t=20/3.
  assert.equal(fmt(iv), '11/3 .. 20/3 (incl)');
});
