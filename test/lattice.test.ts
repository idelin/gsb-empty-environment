import { test } from 'node:test';
import assert from 'node:assert/strict';
import { booleanLattice, setLattice } from '../src/lattice.ts';

test('布尔格：序与并', () => {
  assert.equal(booleanLattice.bottom, false);
  assert.ok(booleanLattice.leq(false, false));
  assert.ok(booleanLattice.leq(false, true));
  assert.ok(booleanLattice.leq(true, true));
  assert.ok(!booleanLattice.leq(true, false));
  assert.equal(booleanLattice.join(false, true), true);
  assert.equal(booleanLattice.join(true, false), true);
  assert.equal(booleanLattice.join(false, false), false);
});

test('幂集格：子集序与并集', () => {
  const lat = setLattice<string>();
  const ab = new Set(['a', 'b']);
  const a = new Set(['a']);
  const c = new Set(['c']);
  assert.ok(lat.leq(lat.bottom, a));
  assert.ok(lat.leq(a, ab));
  assert.ok(!lat.leq(ab, a));
  assert.ok(!lat.leq(a, c));
  assert.deepEqual(lat.join(a, c), new Set(['a', 'c']));
  assert.ok(lat.eq(ab, new Set(['b', 'a'])));
  assert.ok(!lat.eq(a, ab));
});
