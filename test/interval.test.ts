import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  parseRational,
  parseValue,
  compareAffine,
  uncertainValue,
  exactValue,
  bounds,
  toString,
} from '../src/index.ts';

test('separated intervals decide lt/gt', () => {
  const a = parseValue('1.0±0.1', 's1');
  const b = parseValue('1.5±0.1', 's2');
  assert.deepEqual(compareAffine(a, b), { kind: 'decided', outcome: 'lt' });
  assert.deepEqual(compareAffine(b, a), { kind: 'decided', outcome: 'gt' });
});

test('overlapping intervals are undetermined, never "approximately equal"', () => {
  const a = parseValue('1.5±0.1', 's1');
  const b = parseValue('1.55±0.1', 's2');
  const r = compareAffine(a, b);
  assert.equal(r.kind, 'undetermined');
  // identical centres, independent sources: still undetermined
  const c = parseValue('1.5±0.1', 's3');
  assert.equal(compareAffine(a, c).kind, 'undetermined');
});

test('shared provenance cancels: same source, same reading => eq', () => {
  const a = parseValue('1.5±0.1', 'sensor-7');
  const b = parseValue('1.5±0.1', 'sensor-7');
  assert.deepEqual(compareAffine(a, b), { kind: 'decided', outcome: 'eq' });
});

test('shared provenance cancels error, exact offset still decides', () => {
  // two quantities measured with the SAME instrument bias e ∈ [-0.1, 0.1]:
  // a = 1.0 + e, b = 2.0 + e  =>  a - b = -1 exactly, regardless of e
  const a = parseValue('1.0±0.1', 'instrument');
  const b = parseValue('2.0±0.1', 'instrument');
  assert.deepEqual(compareAffine(a, b), { kind: 'decided', outcome: 'lt' });
  // independent instruments with the same bounds cannot decide
  const c = parseValue('1.0±0.1', 'instrument-1');
  const d = parseValue('1.05±0.1', 'instrument-2');
  assert.equal(compareAffine(c, d).kind, 'undetermined');
});

test('shared token with unequal radii keeps only the excess as residual', () => {
  // a = 0 ± 0.5@S, b = 0.1 ± 0.1@S: shared 0.1 cancels, excess 0.4 remains,
  // |d| = 0.1 <= 0.4 => undetermined
  const a = uncertainValue(parseRational('0'), 'S', parseRational('0.5'));
  const b = uncertainValue(parseRational('0.1'), 'S', parseRational('0.1'));
  assert.equal(compareAffine(a, b).kind, 'undetermined');
  // widen the gap beyond the excess: |d| = 0.41 > 0.4 => decided
  const c = uncertainValue(parseRational('0.41'), 'S', parseRational('0.1'));
  assert.deepEqual(compareAffine(a, c), { kind: 'decided', outcome: 'lt' });
});

test('exact point strictly inside an interval is undetermined, not eq', () => {
  const point = exactValue(parseRational('1.5'));
  const interval = parseValue('1.5±0.25', 'gauge');
  assert.equal(compareAffine(point, interval).kind, 'undetermined');
});

test('exact point outside an interval decides', () => {
  const point = exactValue(parseRational('2'));
  const interval = parseValue('1.5±0.25', 'gauge');
  assert.deepEqual(compareAffine(point, interval), { kind: 'decided', outcome: 'gt' });
});

test('bounds are exact rationals', () => {
  const v = parseValue('1/3±0.01', 'g');
  const { lo, hi } = bounds(v);
  assert.equal(toString(lo), '97/300');
  assert.equal(toString(hi), '103/300');
});
