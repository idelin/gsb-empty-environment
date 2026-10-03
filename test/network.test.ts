import { test } from 'node:test';
import assert from 'node:assert/strict';
import { bit, maskOf, UNIVERSAL } from '../src/allen.ts';
import { TemporalNetwork } from '../src/network.ts';

test('transitive closure: A before B, B overlaps C  =>  A before C', () => {
  const net = new TemporalNetwork(3);
  net.intersect(0, 1, bit('b'));
  net.intersect(1, 2, bit('o'));
  assert.equal(net.propagate(), true);
  assert.equal(net.mask(0, 2), bit('b'));
  assert.equal(net.mask(2, 0), bit('bi'));
});

test('transitive closure: {b,m} chains tighten to {b}', () => {
  // b∘b = b∘m = m∘b = m∘m = {b}: a gapless precedence chain still forces
  // a strict "before" between the endpoints of the chain.
  const net = new TemporalNetwork(3);
  net.intersect(0, 1, maskOf('b', 'm'));
  net.intersect(1, 2, maskOf('b', 'm'));
  assert.equal(net.propagate(), true);
  assert.equal(net.mask(0, 2), bit('b'));
});

test('unrelated pairs remain universal after propagation', () => {
  const net = new TemporalNetwork(3);
  net.intersect(0, 1, bit('b'));
  assert.equal(net.propagate(), true);
  assert.equal(net.mask(0, 2), UNIVERSAL);
  assert.equal(net.mask(1, 2), UNIVERSAL);
});

test('cyclic precedence is detected as inconsistent', () => {
  const net = new TemporalNetwork(3);
  net.intersect(0, 1, bit('b'));
  net.intersect(1, 2, bit('b'));
  net.intersect(2, 0, bit('b'));
  assert.equal(net.propagate(), false);
  assert.equal(net.consistent, false);
});

test('directly contradictory assertion is inconsistent', () => {
  const net = new TemporalNetwork(2);
  net.intersect(0, 1, bit('b'));
  net.intersect(0, 1, bit('bi'));
  assert.equal(net.consistent, false);
});

test('propagation tightens through disjunctive paths', () => {
  // A {b,m} B, B {o} C:  b∘o = {b}, m∘o = {b}  =>  A {b} C
  const net = new TemporalNetwork(3);
  net.intersect(0, 1, maskOf('b', 'm'));
  net.intersect(1, 2, bit('o'));
  assert.equal(net.propagate(), true);
  assert.equal(net.mask(0, 2), bit('b'));
});

test('longer chains propagate across several hops', () => {
  // A b B, B m C, C o D  =>  A must end before D starts: A {b} D
  const net = new TemporalNetwork(4);
  net.intersect(0, 1, bit('b'));
  net.intersect(1, 2, bit('m'));
  net.intersect(2, 3, bit('o'));
  assert.equal(net.propagate(), true);
  assert.equal(net.mask(0, 3), bit('b'));
  assert.equal(net.mask(0, 2), bit('b'));
});
