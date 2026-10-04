import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Dec } from '../src/decimal.ts';
import * as Iv from '../src/interval.ts';

const d = (s: string) => Dec.fromString(s);
const P = 10;

test('outward rounding contains the true quotient', () => {
  const r = Iv.div(Iv.point(d('1')), Iv.point(d('3')), 3);
  assert.equal(Iv.toString(r), '[0.333, 0.334]');
  assert.equal(Iv.containsValue(r, d('0.3333333333')), true);
});

test('mul handles mixed signs', () => {
  const r = Iv.mul(Iv.range(d('-2'), d('3')), Iv.range(d('4'), d('5')), P);
  assert.equal(Iv.toString(r), '[-10, 15]');
});

test('negative constant scale flips bounds', () => {
  const r = Iv.scale(Iv.range(d('2'), d('3')), d('-1'), P);
  assert.equal(Iv.toString(r), '[-3, -2]');
});

test('empty and unknown are distinct kinds', () => {
  assert.notEqual(Iv.EMPTY.kind, Iv.UNKNOWN.kind);
  assert.equal(Iv.containsValue(Iv.EMPTY, d('0')), false);
  assert.equal(Iv.containsValue(Iv.UNKNOWN, d('0')), true);
  assert.equal(Iv.intersect(Iv.range(d('0'), d('1')), Iv.range(d('2'), d('3'))).kind, 'empty');
  assert.equal(Iv.containsInterval(Iv.UNKNOWN, Iv.EMPTY), true);
  assert.equal(Iv.containsInterval(Iv.EMPTY, Iv.UNKNOWN), false);
});

test('division straddling zero is rejected', () => {
  assert.throws(() => Iv.div(Iv.point(d('1')), Iv.range(d('-1'), d('2')), P), Iv.ZeroDivisionError);
  assert.throws(() => Iv.div(Iv.point(d('1')), Iv.UNKNOWN, P), Iv.ZeroDivisionError);
});

test('zero endpoint of denominator yields a ray', () => {
  assert.equal(Iv.toString(Iv.div(Iv.point(d('1')), Iv.range(d('0'), d('2')), P)), '[0.5, ∞]');
  assert.equal(Iv.toString(Iv.div(Iv.point(d('1')), Iv.range(d('-2'), d('0')), P)), '[-∞, -0.5]');
});

test('hull and containment', () => {
  const h = Iv.hull(Iv.range(d('0'), d('1')), Iv.range(d('3'), d('4')));
  assert.equal(Iv.toString(h), '[0, 4]');
  assert.equal(Iv.containsInterval(h, Iv.range(d('0'), d('1'))), true);
  assert.equal(Iv.containsInterval(Iv.range(d('0'), d('1')), h), false);
});
