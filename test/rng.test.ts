import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rng, fnv1a } from '../src/rng.ts';

test('same seed produces identical sequences', () => {
  const a = new Rng(42);
  const b = new Rng(42);
  for (let i = 0; i < 1000; i++) {
    assert.equal(a.next(), b.next());
  }
});

test('different seeds diverge', () => {
  const a = new Rng(1);
  const b = new Rng(2);
  const seqA = Array.from({ length: 100 }, () => a.next());
  const seqB = Array.from({ length: 100 }, () => b.next());
  assert.notDeepEqual(seqA, seqB);
});

test('int() stays in range and is deterministic', () => {
  const a = new Rng(7);
  const b = new Rng(7);
  for (let i = 0; i < 1000; i++) {
    const v = a.int(6);
    assert.ok(v >= 0 && v < 6);
    assert.equal(v, b.int(6));
  }
});

test('fnv1a is stable', () => {
  assert.equal(fnv1a('swim'), fnv1a('swim'));
  assert.notEqual(fnv1a('swim'), fnv1a('swim!'));
  assert.match(fnv1a(''), /^[0-9a-f]{8}$/);
});
