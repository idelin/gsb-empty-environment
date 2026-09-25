import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JsonDagError, normalize } from '../src/json.ts';
import { digestOfTree } from '../src/digest.ts';
import type { HashFn } from '../src/digest.ts';
import { diff } from '../src/diff.ts';

test('normalization is idempotent and digest-stable', () => {
  const messy = {
    z: [{ b: 2, a: 1 }, { d: { y: [3, { q: 1, p: 2 }], x: 2 } }],
    a: { m: 1, b: { k: 2, j: 1 } },
  };
  const once = normalize(messy);
  const twice = normalize(once);
  assert.deepEqual(twice, once);
  assert.equal(digestOfTree(messy), digestOfTree(once));
});

test('object key order does not affect digests', () => {
  assert.equal(
    digestOfTree({ a: 1, b: { c: 2, d: 3 } }),
    digestOfTree({ b: { d: 3, c: 2 }, a: 1 }),
  );
});

test('forced digest collision is still caught by collision review', () => {
  const constantHash: HashFn = () => '0';
  const base = { a: 1, b: { c: 2 } };
  const target = { a: 1, b: { c: 3 } };
  // Digests collide everywhere by construction.
  assert.equal(digestOfTree(base, { hash: constantHash }), digestOfTree(target, { hash: constantHash }));
  // Without review, digest pruning trusts the (colliding) digest.
  assert.equal(diff(base, target, { hash: constantHash }).changes.length, 0);
  // With review, content comparison finds the difference anyway.
  const checked = diff(base, target, { hash: constantHash, verifyCollisions: true });
  assert.equal(checked.changes.length, 1);
  assert.equal(checked.changes[0].collision, true);
  assert.deepEqual(checked.changes[0].path, []);
  assert.deepEqual(checked.changes[0].after, target);
});

test('duplicate array ids are rejected with a locating path', () => {
  const opts = { arrayMode: () => 'id' as const };
  let err: JsonDagError | undefined;
  try {
    digestOfTree({ list: [{ id: 'a' }, { id: 'b' }, { id: 'a' }] }, opts);
  } catch (e) {
    err = e as JsonDagError;
  }
  assert.ok(err instanceof JsonDagError);
  assert.deepEqual(err.path, ['list', 2]);
  assert.match(err.message, /duplicate array element id/);
  assert.match(err.message, /\$\.list\[2\]/);
});
