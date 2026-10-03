import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  RELATIONS,
  UNIVERSAL,
  bit,
  maskOf,
  relationsOf,
  compose,
  composeMasks,
  converseMask,
  converseRelation,
  endpointConstraints,
  parseRelations,
} from '../src/allen.ts';

test('13 basic relations with distinct bits', () => {
  assert.equal(RELATIONS.length, 13);
  const all = maskOf(...RELATIONS);
  assert.equal(all, UNIVERSAL);
  assert.equal(all.toString(2).length, 13);
});

test('converse is an involution and swaps pairs correctly', () => {
  assert.equal(converseRelation('b'), 'bi');
  assert.equal(converseRelation('bi'), 'b');
  assert.equal(converseRelation('m'), 'mi');
  assert.equal(converseRelation('o'), 'oi');
  assert.equal(converseRelation('s'), 'si');
  assert.equal(converseRelation('d'), 'di');
  assert.equal(converseRelation('f'), 'fi');
  assert.equal(converseRelation('eq'), 'eq');
  for (const r of RELATIONS) {
    assert.equal(converseRelation(converseRelation(r)), r);
  }
  assert.equal(converseMask(maskOf('b', 'o')), maskOf('bi', 'oi'));
});

test('composition: every entry is non-empty', () => {
  for (const r1 of RELATIONS) {
    for (const r2 of RELATIONS) {
      assert.notEqual(compose(r1, r2), 0, `compose(${r1}, ${r2}) is empty`);
    }
  }
});

test('composition: known table entries', () => {
  // A before B, B before C  =>  A before C (only).
  assert.equal(compose('b', 'b'), bit('b'));
  // A meets B, B meets C  =>  A ends strictly before C starts.
  assert.equal(compose('m', 'm'), bit('b'));
  // A overlaps B, B overlaps C  =>  A may be before, meet or overlap C.
  assert.equal(compose('o', 'o'), maskOf('b', 'm', 'o'));
  // A before B, B after C  =>  no restriction between A and C.
  assert.equal(compose('b', 'bi'), UNIVERSAL);
  // A during B, B during C  =>  A during C.
  assert.equal(compose('d', 'd'), bit('d'));
  // Identity element.
  assert.equal(compose('b', 'eq'), bit('b'));
  assert.equal(compose('eq', 'o'), bit('o'));
});

test('composition: converse symmetry of the generated table', () => {
  for (const r1 of RELATIONS) {
    for (const r2 of RELATIONS) {
      const forward = compose(r1, r2);
      const backward = compose(converseRelation(r2), converseRelation(r1));
      assert.equal(
        forward,
        converseMask(backward),
        `table not converse-symmetric at (${r1}, ${r2})`,
      );
    }
  }
});

test('composeMasks distributes over disjunctions', () => {
  // {b,m} composed with {b,m}: b∘b=b, b∘m=b, m∘b=b, m∘m=b  =>  {b}
  assert.equal(composeMasks(maskOf('b', 'm'), maskOf('b', 'm')), bit('b'));
  assert.equal(composeMasks(UNIVERSAL, bit('eq')), UNIVERSAL);
});

test('parseRelations accepts symbols, names and set notation', () => {
  assert.equal(parseRelations('b'), bit('b'));
  assert.equal(parseRelations('before'), bit('b'));
  assert.equal(parseRelations('{b,m}'), maskOf('b', 'm'));
  assert.equal(parseRelations(['b', 'meets']), maskOf('b', 'm'));
  assert.equal(parseRelations(maskOf('o')), bit('o'));
  assert.throws(() => parseRelations('nonsense'));
});

test('endpointConstraints: strict before becomes a negative lag', () => {
  const cs = endpointConstraints(bit('b'));
  const keys = new Set(cs.map((c) => `${c.from}->${c.to}:${c.maxLagUnits}`));
  // A.end strictly before B.start:  time[a.end] - time[b.start] <= -1 (in eps units)
  assert.ok(keys.has('2->1:-1'));
  // The full endpoint order a.s < a.e < b.s < b.e is fixed (6 strict bounds).
  assert.equal(cs.length, 6);
  assert.ok(cs.every((c) => c.maxLagUnits === -1));
});

test('endpointConstraints: {b,m} convex hull is a non-strict precedence', () => {
  const cs = endpointConstraints(maskOf('b', 'm'));
  const keys = new Set(cs.map((c) => `${c.from}->${c.to}:${c.maxLagUnits}`));
  // The strict precedence is relaxed to the common bound A.end <= B.start.
  assert.ok(keys.has('2->1:0'));
  // ...while the reverse direction (b.start - a.end) stays unconstrained.
  assert.ok(![...keys].some((k) => k.startsWith('1->2')));
});

test('endpointConstraints: overlaps fixes the full endpoint order', () => {
  const cs = endpointConstraints(bit('o'));
  const keys = new Set(cs.map((c) => `${c.from}->${c.to}:${c.maxLagUnits}`));
  // a.start < b.start < a.end < b.end
  assert.ok(keys.has('2->0:-1')); // a.start < b.start
  assert.ok(keys.has('1->2:-1')); // b.start < a.end
  assert.ok(keys.has('3->1:-1')); // a.end < b.end
});

test('relationsOf round-trips through masks', () => {
  assert.deepEqual(relationsOf(maskOf('b', 'd', 'fi')), ['b', 'd', 'fi']);
  assert.deepEqual(relationsOf(UNIVERSAL), [...RELATIONS]);
});
