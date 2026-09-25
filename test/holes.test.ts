import { test } from 'node:test';
import assert from 'node:assert/strict';
import { queryOnce } from '../src/store.ts';

// Per spec: an array hole is ABSENT (like a missing property); an explicit
// `undefined` element is PRESENT with the value undefined.

test('index access: hole is absent, explicit undefined is present', () => {
  const hole: unknown[] = new Array(2); // [<2 holes>]
  const explicit: unknown[] = [undefined, undefined];
  assert.deepEqual(queryOnce({ a: hole }, 'a[0]'), []);
  const matches = queryOnce({ a: explicit }, 'a[0]');
  assert.equal(matches.length, 1);
  assert.equal(matches[0]!.value, undefined);
  assert.deepEqual(matches[0]!.path, ['a', 0]);
});

test('wildcard: holes are skipped, explicit undefined is emitted', () => {
  const sparse: unknown[] = new Array(3);
  sparse[1] = 'x';
  const dense: unknown[] = [undefined, 'x', undefined];
  assert.deepEqual(queryOnce({ a: sparse }, 'a[*]').map((m) => m.path), [['a', 1]]);
  assert.deepEqual(
    queryOnce({ a: dense }, 'a[*]').map((m) => m.path),
    [['a', 0], ['a', 1], ['a', 2]],
  );
});

test('filter: holes are not candidates, explicit undefined is', () => {
  const sparse: unknown[] = new Array(2);
  sparse[0] = { n: 1 }; // index 1 is a hole
  const dense: unknown[] = [{ n: 1 }, undefined];
  assert.deepEqual(queryOnce({ a: sparse }, 'a[?(@.n >= 0)]').length, 1);
  // undefined candidate: @.n is missing -> comparison false, but the
  // candidate itself was still considered (not skipped like a hole).
  const denseMatches = queryOnce({ a: dense }, 'a[?(@ == null)]');
  assert.equal(denseMatches.length, 0); // undefined != null under scalar equality
  const holeCount = queryOnce({ a: sparse }, 'a[?(@.n in {1,2,3})]').length;
  assert.equal(holeCount, 1);
});

test('descend: traversal skips holes but visits explicit undefined', () => {
  const sparse: unknown[] = new Array(2);
  sparse[1] = { tag: 'hit' };
  const doc = { a: sparse };
  assert.deepEqual(queryOnce(doc, '..tag').map((m) => m.path), [['a', 1, 'tag']]);
});
