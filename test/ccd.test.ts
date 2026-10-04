import { Fraction, F } from '../src/fraction.js';
import { pairContactInterval, broadPhase, sweptAabb, defaultCellSize } from '../src/ccd.js';
import { test, assert } from './harness.js';

function body (id: string, x: any, y: any, w: any, h: any, vx: any, vy: any) {
  return { id, x: F(x), y: F(y), w: F(w), h: F(h), vx: F(vx), vy: F(vy) };
}

function intervalStr (a: any, b: any, T: any) {
  const r = pairContactInterval(a, b, F(T));
  if (r === null) return null;
  return [r[0].toString(), r[1].toString()];
}

test('ccd: same motion, overlapping -> whole [0, T]', () => {
  const a = body('a', 0, 0, 2, 2, '1/3', '-1/2');
  const b = body('b', 1, 1, 2, 2, '1/3', '-1/2');
  assert.deepStrictEqual(intervalStr(a, b, 10), ['0', '10']);
});

test('ccd: same motion, disjoint -> never', () => {
  const a = body('a', 0, 0, 1, 1, '1/3', '-1/2');
  const b = body('b', 5, 5, 1, 1, '1/3', '-1/2');
  assert.strictEqual(intervalStr(a, b, 10), null);
});

test('ccd: zero relative velocity, touching counts as collision', () => {
  const a = body('a', 0, 0, 1, 1, 0, 0);
  const b = body('b', 1, 0, 1, 1, 0, 0);
  assert.deepStrictEqual(intervalStr(a, b, 4), ['0', '4']);
});

test('ccd: initial overlap starts at 0', () => {
  const a = body('a', 0, 0, 2, 2, 0, 0);
  const b = body('b', 1, 1, 2, 2, '1/2', 0);
  assert.deepStrictEqual(intervalStr(a, b, 10), ['0', '2']);
});

test('ccd: contact exactly at t = T', () => {
  const a = body('a', 0, 0, 1, 1, 0, 0);
  const b = body('b', 2, 0, 1, 1, -1, 0);
  assert.deepStrictEqual(intervalStr(a, b, 1), ['1', '1']);
});

test('ccd: contact just after T is not reported', () => {
  const a = body('a', 0, 0, 1, 1, 0, 0);
  const b = body('b', '2/1', 0, 1, 1, '-1/2', 0);
  // contact at t = 2, but T = 3/2
  assert.strictEqual(intervalStr(a, b, '3/2'), null);
  assert.deepStrictEqual(intervalStr(a, b, 2), ['2', '2']);
});

test('ccd: extremely narrow intersection interval (exact rationals)', () => {
  // x-window [0, 2], y-window [2 - 1e-9, 4 - 1e-9] -> intersection width 1e-9
  const a = body('a', 0, 0, 1, 1, 0, 0);
  const b = body('b', -1, '2999999999/1000000000', 1, 1, 1, -1) ;
  assert.deepStrictEqual(intervalStr(a, b, 3), ['1999999999/1000000000', '2']);
});

test('ccd: negative coordinates', () => {
  const a = body('a', -5, -5, 2, 2, '1/2', '1/3');
  const b = body('b', 3, -7, 1, 1, '-1/2', '2/3');
  assert.deepStrictEqual(intervalStr(a, b, 8), ['6', '8']);
});

test('ccd: zero-size point rect passes through rect', () => {
  const a = body('a', 0, 0, 1, 1, 0, 0);
  const b = body('b', 3, '1/2', 0, 0, -1, 0); // point enters at t=2, leaves at t=3
  assert.deepStrictEqual(intervalStr(a, b, 5), ['2', '3']);
});

test('ccd: crossing many grid cells', () => {
  const a = body('a', 50, 0, 1, 1, 0, 0);
  const b = body('b', 0, 0, 1, 1, 25, 0); // crosses 50+ cells of size 1
  const broad = broadPhase([a, b], 3, F(1));
  assert.strictEqual(broad.pairs.length, 1);
  assert.deepStrictEqual(intervalStr(a, b, 3), ['49/25', '51/25']);
});

test('ccd: grid boundary duplicate enumeration -> pair reported once', () => {
  // A spans cells (0..2, 0..2), B spans (1..3, 1..3): they share 4 cells
  const a = body('a', 0, 0, 2, 2, 0, 0);
  const b = body('b', 1, 1, 2, 2, 0, 0);
  const broad = broadPhase([a, b], 1, F(1));
  assert.strictEqual(broad.pairs.length, 1);
  assert.strictEqual(broad.cellCount, 14);
});

test('ccd: swept AABB spans movement in both directions', () => {
  const a = body('a', 0, 0, 1, 1, '-3/2', '1/2');
  const box = sweptAabb(a, 4);
  assert.strictEqual(box.minX.toString(), '-6');
  assert.strictEqual(box.maxX.toString(), '1');
  assert.strictEqual(box.minY.toString(), '0');
  assert.strictEqual(box.maxY.toString(), '3');
});

test('ccd: default cell size heuristic is positive', () => {
  const bodies = [body('a', 0, 0, 2, 3, 0, 0), body('b', 9, 9, 1, 1, 0, 0)];
  const cs = defaultCellSize(bodies, 2);
  assert.strictEqual(cs.toString(), '3');
});

test('ccd: pairs separated by swept AABB are pruned', () => {
  const a = body('a', 0, 0, 1, 1, 0, 0);
  const b = body('b', 100, 100, 1, 1, 0, 0);
  const broad = broadPhase([a, b], 5, F('1/2'));
  assert.strictEqual(broad.pairs.length, 0);
});
