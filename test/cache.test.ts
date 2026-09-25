import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile, StructureCache } from '../src/index.ts';

test('results are cached per structure version', () => {
  const doc = { items: [{ id: 1 }, { id: 2 }] };
  const cache = new StructureCache();
  const plan = compile('$.items[?(@.id == 1)]');

  const first = cache.query(plan, doc);
  assert.equal(first.fromCache, false);
  assert.equal(first.result.version, 0);

  const second = cache.query(plan, doc);
  assert.equal(second.fromCache, true);
  assert.equal(second.result, first.result, 'same cached object is served');
});

test('local changes revise match results and path evidence', () => {
  const doc: { items: { id: number }[] } = { items: [{ id: 1 }, { id: 2 }] };
  const cache = new StructureCache();
  const plan = compile('$.items[?(@.id == 1)]');

  const before = cache.query(plan, doc);
  assert.deepEqual(before.result.evidence, ['$.items[0]']);

  doc.items.push({ id: 1 });          // local change ...
  cache.invalidate(doc);              // ... announced to the cache

  const after = cache.query(plan, doc);
  assert.equal(after.fromCache, false);
  assert.equal(after.result.version, 1);
  assert.equal(after.result.matches.length, 2);
  assert.deepEqual(after.result.evidence, ['$.items[0]', '$.items[2]']);

  const again = cache.query(plan, doc);
  assert.equal(again.fromCache, true, 'revised result is cached at the new version');
});

test('evidence uses user-facing path formatting', () => {
  const doc = { 'a b': [{ '': 7 }], normal: 1 };
  const cache = new StructureCache();
  const r1 = cache.query(compile("$.'a b'[0].''"), doc);
  assert.deepEqual(r1.result.evidence, ['$["a b"][0][""]']);
  const r2 = cache.query(compile('$.normal'), doc);
  assert.deepEqual(r2.result.evidence, ['$.normal']);
});

test('versions are tracked per root object', () => {
  const a = { v: 1 };
  const b = { v: 1 };
  const cache = new StructureCache();
  const plan = compile('$.v');
  cache.query(plan, a);
  cache.invalidate(a);
  const ra = cache.query(plan, a);
  const rb = cache.query(plan, b);
  assert.equal(ra.fromCache, false, 'a was invalidated');
  assert.equal(rb.fromCache, false, 'b is a different root with its own version');
  assert.equal(cache.versionOf(a), 1);
  assert.equal(cache.versionOf(b), 0);
});
