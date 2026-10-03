import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rng } from '../src/rng.ts';
import { VirtualSwitch } from '../src/network.ts';

test('loss and jitter replay identically from the same seed', () => {
  const profile = { lossRate: 0.25, baseLatency: 2, jitter: 5 };
  const swA = new VirtualSwitch(new Rng(1234), profile);
  const swB = new VirtualSwitch(new Rng(1234), profile);
  const routesA = Array.from({ length: 5000 }, () => swA.route('n1', 'n2'));
  const routesB = Array.from({ length: 5000 }, () => swB.route('n1', 'n2'));
  assert.deepEqual(routesA, routesB);
});

test('loss rate and latency bounds are respected', () => {
  const profile = { lossRate: 0.5, baseLatency: 3, jitter: 4 };
  const sw = new VirtualSwitch(new Rng(99), profile);
  let dropped = 0;
  const total = 20000;
  for (let i = 0; i < total; i++) {
    const delay = sw.route('a', 'b');
    if (delay === null) {
      dropped++;
    } else {
      assert.ok(delay >= 3 && delay <= 7);
    }
  }
  const ratio = dropped / total;
  assert.ok(ratio > 0.45 && ratio < 0.55, `loss ratio ${ratio}`);
});

test('partition drops everything until healed', () => {
  const sw = new VirtualSwitch(new Rng(1));
  sw.isolate('A', 'B');
  for (let i = 0; i < 100; i++) {
    assert.equal(sw.route('A', 'B'), null);
    assert.equal(sw.route('B', 'A'), null);
  }
  sw.heal('A', 'B');
  assert.equal(sw.route('A', 'B'), 1);
});

test('isolateFrom cuts a node off from a set of peers', () => {
  const sw = new VirtualSwitch(new Rng(1));
  sw.isolateFrom('A', ['B', 'C']);
  assert.equal(sw.route('A', 'B'), null);
  assert.equal(sw.route('C', 'A'), null);
  assert.equal(sw.route('B', 'C'), 1);
  sw.healFrom('A', ['B', 'C']);
  assert.equal(sw.route('A', 'C'), 1);
});
