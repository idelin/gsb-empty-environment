import { test } from 'node:test';
import assert from 'node:assert/strict';
import { gate } from '../src/bdd.ts';
import { newBdd } from './helpers.ts';

test('ROBDD is canonical: equivalent formulas share one node', () => {
  const bdd = newBdd();
  const x = bdd.literal('a');
  const y = bdd.literal('b');
  const f1 = bdd.and(x, y);
  const f2 = bdd.and(bdd.or(x, bdd.FALSE), bdd.or(y, bdd.FALSE));
  assert.equal(f1, f2);
});

test('symbolic reachability never enumerates switch assignments', () => {
  const bdd = newBdd();
  const a = bdd.literal('a');
  const b = bdd.literal('b');
  const c = bdd.literal('c');
  // Reachability as a fixed point of symbolic conditions, over a 3-switch
  // graph: condition = a AND (b OR c).
  let reachable = bdd.and(a, bdd.or(b, c));
  for (let iteration = 0; iteration < 5; iteration += 1) {
    const next = bdd.or(reachable, bdd.and(a, bdd.or(b, c)));
    assert.equal(next, reachable);
    reachable = next;
  }
  assert.ok(bdd.isSat(reachable));
  assert.equal(bdd.and(reachable, bdd.not(a)), bdd.FALSE);
  const witness = bdd.witness(reachable);
  assert.ok(witness);
  assert.equal(witness!.a, true);
  assert.ok(witness!.b === true || witness!.c === true);
});

test('entailment and contradiction are decided without enumeration', () => {
  const bdd = newBdd();
  const a = bdd.literal('a');
  const condition = bdd.compile(gate.and(gate.varOf('a'), gate.not(gate.varOf('a'))));
  assert.equal(condition, bdd.FALSE);
  assert.equal(bdd.witness(condition), undefined);
  assert.ok(bdd.entails(bdd.and(a, bdd.literal('b')), a));
  assert.ok(!bdd.entails(a, bdd.literal('b')));
});
