import assert from 'node:assert/strict';
import { test } from 'node:test';
import { HashRing } from '../src/hashRing.ts';

interface Member {
  id: string;
}

function buildRing(count: number): HashRing<Member> {
  const ring = new HashRing<Member>();
  for (let i = 0; i < count; i += 1) {
    ring.add({ id: `node-${i}` });
  }
  return ring;
}

function sampleKeys(count: number): string[] {
  const keys: string[] = [];
  for (let i = 0; i < count; i += 1) {
    keys.push(`device-${i}`);
  }
  return keys;
}

test('lookup is deterministic', () => {
  const ring = buildRing(8);
  const first = ring.lookup('device-ABC');
  assert.ok(first !== null);
  for (let i = 0; i < 100; i += 1) {
    assert.equal(ring.lookup('device-ABC'), first);
  }
});

test('adding one node moves only a small fraction of keys', () => {
  const ring = buildRing(8);
  const keys = sampleKeys(20000);
  const before = new Map(keys.map((k) => [k, ring.lookup(k)]));
  ring.add({ id: 'node-8' });
  let moved = 0;
  for (const key of keys) {
    if (ring.lookup(key) !== before.get(key)) moved += 1;
  }
  const ratio = moved / keys.length;
  assert.ok(ratio > 0, 'some keys should move to the new node');
  assert.ok(ratio < 0.25, `moved ratio ${ratio} should stay near 1/9`);
});

test('removing a node only remaps keys that lived on it', () => {
  const ring = buildRing(8);
  const keys = sampleKeys(20000);
  const before = new Map(keys.map((k) => [k, ring.lookup(k)]));
  ring.remove('node-3');
  for (const key of keys) {
    const prev = before.get(key);
    const next = ring.lookup(key);
    if (prev !== 'node-3') {
      assert.equal(next, prev, `key ${key} should not move`);
    } else {
      assert.notEqual(next, 'node-3');
      assert.ok(next !== null);
    }
  }
});

test('exclusion set fails over to the next node on the ring', () => {
  const ring = buildRing(4);
  const key = 'device-XYZ';
  const primary = ring.lookup(key);
  assert.ok(primary !== null);
  const fallback = ring.lookup(key, new Set([primary]));
  assert.ok(fallback !== null);
  assert.notEqual(fallback, primary);
  const none = ring.lookup(key, new Set(ring.ids()));
  assert.equal(none, null);
});
