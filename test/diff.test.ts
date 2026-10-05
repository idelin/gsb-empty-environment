import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MapStore, VersionError, mergeDiff } from '../src/index.ts';
import { collectAll, mulberry32 } from './util.ts';

function numberStore() {
  return new MapStore<number, number>();
}

test('classifies added, deleted and changed entries', () => {
  const store = new MapStore<string, number>();
  let v1 = store.empty();
  v1 = store.set(v1, 'a', 1);
  v1 = store.set(v1, 'b', 2);
  v1 = store.set(v1, 'c', 3);
  let v2 = store.set(v1, 'c', 30);
  v2 = store.remove(v2, 'a');
  v2 = store.set(v2, 'd', 4);
  const differ = store.differ();
  const { items, pages } = collectAll(differ, differ.beginDiff(v1, v2), 10);
  assert.deepEqual(items, [
    { type: 'deleted', key: 'a', oldValue: 1 },
    { type: 'changed', key: 'c', oldValue: 3, newValue: 30 },
    { type: 'added', key: 'd', newValue: 4 },
  ]);
  assert.equal(pages.length, 1);
  assert.equal(pages[0]!.done, true);
  assert.equal(pages[0]!.reason, 'complete');
});

test('empty maps complete immediately with no items', () => {
  const store = numberStore();
  const differ = store.differ();
  const page = differ.nextPage(differ.beginDiff(store.empty(), store.empty()), 5);
  assert.equal(page.done, true);
  assert.deepEqual(page.items, []);
  assert.equal(page.cursor.lastKey, null);
});

test('identical root reference is skipped in one visit', () => {
  const store = numberStore();
  let root = store.empty();
  for (let i = 0; i < 1000; i++) root = store.set(root, i, i);
  const differ = store.differ();
  const page = differ.nextPage(differ.beginDiff(root, root), 5);
  assert.equal(page.done, true);
  assert.deepEqual(page.items, []);
  assert.equal(page.stats.scannedNodes, 1);
});

for (const trust of ['verified', 'trusted'] as const) {
  test(`randomized paged diff matches full merge (trust=${trust})`, () => {
    for (let seed = 1; seed <= 40; seed++) {
      const rand = mulberry32(seed);
      const store = numberStore();
      let v1 = store.empty();
      const n0 = Math.floor(rand() * 120);
      for (let i = 0; i < n0; i++) {
        v1 = store.set(v1, Math.floor(rand() * 200), Math.floor(rand() * 1000));
      }
      let v2 = v1;
      const ops = Math.floor(rand() * 80);
      for (let i = 0; i < ops; i++) {
        const k = Math.floor(rand() * 200);
        if (rand() < 0.6) v2 = store.set(v2, k, Math.floor(rand() * 1000));
        else v2 = store.remove(v2, k);
      }
      const reference = mergeDiff(store.entries(v1), store.entries(v2), store.config.compare);
      const differ = store.differ({ trust });
      const pageSize = 1 + Math.floor(rand() * 5);
      const cancelRand = mulberry32(seed * 7919 + 13);
      const { items, pages } = collectAll(
        differ,
        differ.beginDiff(v1, v2),
        pageSize,
        () => (cancelRand() < 0.25 ? { scanBudget: Math.floor(cancelRand() * 30) } : undefined),
      );
      assert.deepEqual(items, reference, `seed=${seed}`);
      for (const p of pages) {
        assert.ok(p.items.length <= pageSize, `page emitted ${p.items.length} > ${pageSize}`);
      }
    }
  });
}

test('immediate cancellation yields an empty page that resumes cleanly', () => {
  const store = numberStore();
  let v1 = store.empty();
  for (let i = 0; i < 500; i++) v1 = store.set(v1, i, i);
  let v2 = v1;
  for (let i = 0; i < 500; i += 2) v2 = store.remove(v2, i);
  const differ = store.differ();
  const start = differ.beginDiff(v1, v2);
  const cancelled = differ.nextPage(start, 10, { shouldCancel: () => true });
  assert.equal(cancelled.done, false);
  assert.equal(cancelled.reason, 'cancelled');
  assert.deepEqual(cancelled.items, []);
  assert.equal(cancelled.cursor.lastKey, null);
  // The empty page did not lose anything: resuming produces the full diff.
  const { items } = collectAll(differ, cancelled.cursor, 10);
  const reference = mergeDiff(store.entries(v1), store.entries(v2), store.config.compare);
  assert.deepEqual(items, reference);
});

test('page size bounds emitted items, not scanned keys', () => {
  const store = numberStore();
  let v1 = store.empty();
  for (let i = 0; i < 5000; i++) v1 = store.set(v1, i, i);
  let v2 = v1;
  v2 = store.set(v2, 100, -1);
  v2 = store.set(v2, 2500, -1);
  v2 = store.set(v2, 4999, -1);
  const differ = store.differ();
  const { items, pages } = collectAll(differ, differ.beginDiff(v1, v2), 1);
  const reference = mergeDiff(store.entries(v1), store.entries(v2), store.config.compare);
  assert.deepEqual(items, reference);
  assert.equal(items.length, 3);
  assert.ok(pages.every((p) => p.items.length <= 1));
  // A page may scan arbitrarily many shared keys to fill a single item slot.
  assert.ok(
    pages.some((p) => p.stats.scannedNodes > 10 * Math.max(1, p.items.length)),
    `expected a page scanning many keys: ${pages.map((p) => p.stats.scannedNodes).join(',')}`,
  );
});

test('long common prefix is skipped wholesale', () => {
  const store = numberStore();
  let v1 = store.empty();
  for (let i = 0; i < 10000; i++) v1 = store.set(v1, i, i * 2);
  let v2 = v1;
  for (let i = 10000; i < 10010; i++) v2 = store.set(v2, i, i);
  v2 = store.set(v2, 5000, -1);
  const differ = store.differ();
  const { items, pages } = collectAll(differ, differ.beginDiff(v1, v2), 4);
  const reference = mergeDiff(store.entries(v1), store.entries(v2), store.config.compare);
  assert.deepEqual(items, reference);
  assert.equal(items.length, 11);
  const scanned = pages.reduce((n, p) => n + p.stats.scannedNodes, 0);
  assert.ok(
    scanned < 500,
    `scanned ${scanned} nodes for 2 changed paths in a 10k map; structural sharing should skip the rest`,
  );
});

test('empty-to-large and large-to-empty diffs page correctly', () => {
  const store = numberStore();
  let big = store.empty();
  for (let i = 0; i < 1000; i++) big = store.set(big, i, i);
  const differ = store.differ();
  const added = collectAll(differ, differ.beginDiff(store.empty(), big), 33);
  assert.equal(added.items.length, 1000);
  assert.ok(added.items.every((it) => it.type === 'added'));
  const deleted = collectAll(differ, differ.beginDiff(big, store.empty()), 33);
  assert.equal(deleted.items.length, 1000);
  assert.ok(deleted.items.every((it) => it.type === 'deleted'));
  const reference = mergeDiff(store.entries(big), store.entries(store.empty()), store.config.compare);
  assert.deepEqual(deleted.items, reference);
});

test('cursor retains roots after both versions are released', () => {
  const store = numberStore();
  let v1 = store.empty();
  for (let i = 0; i < 300; i++) v1 = store.set(v1, i, i);
  let v2 = v1;
  for (let i = 0; i < 300; i += 3) v2 = store.set(v2, i, -i);
  for (let i = 1; i < 300; i += 3) v2 = store.remove(v2, i);
  const id1 = store.commit(v1);
  const id2 = store.commit(v2);
  const reference = mergeDiff(
    store.entries(store.root(id1)),
    store.entries(store.root(id2)),
    store.config.compare,
  );
  const differ = store.differ();
  const cursor = differ.beginDiff(store.root(id1), store.root(id2));
  assert.equal(store.release(id1), true);
  assert.equal(store.release(id2), true);
  assert.throws(() => store.root(id1), VersionError);
  assert.throws(() => store.root(id2), VersionError);
  // The diff still completes: the cursor pinned both roots itself.
  const { items } = collectAll(differ, cursor, 7);
  assert.deepEqual(items, reference);
  assert.equal(store.release(id1), false);
});

test('hash collisions are resolved by the explicit trust model', () => {
  const colliding = new MapStore<string, number>({ hashNode: () => 42 });
  let v1 = colliding.empty();
  v1 = colliding.set(v1, 'a', 1);
  v1 = colliding.set(v1, 'b', 2);
  let v2 = colliding.set(v1, 'b', 3);
  v2 = colliding.set(v2, 'c', 4);
  const reference = mergeDiff(colliding.entries(v1), colliding.entries(v2), colliding.config.compare);
  assert.equal(reference.length, 2);

  // 'trusted' accepts equal digests at face value: collisions hide changes.
  const trusted = colliding.differ({ trust: 'trusted' });
  const trustedResult = collectAll(trusted, trusted.beginDiff(v1, v2), 10);
  assert.deepEqual(trustedResult.items, []);

  // 'verified' (the default) descends despite equal digests and stays correct.
  const verified = colliding.differ({ trust: 'verified' });
  const verifiedResult = collectAll(verified, verified.beginDiff(v1, v2), 1);
  assert.deepEqual(verifiedResult.items, reference);
});

test('trusted mode skips digest-equal subtrees even without shared references', () => {
  const store = numberStore();
  const build = () => {
    let root = store.empty();
    for (let i = 0; i < 200; i++) root = store.set(root, i, i * 5);
    return root;
  };
  const a = build();
  const b = build();
  assert.notEqual(a, b);
  const trusted = store.differ({ trust: 'trusted' });
  const fast = trusted.nextPage(trusted.beginDiff(a, b), 10);
  assert.deepEqual(fast.items, []);
  assert.equal(fast.stats.scannedNodes, 1);
  const verified = store.differ({ trust: 'verified' });
  const thorough = verified.nextPage(verified.beginDiff(a, b), 10);
  assert.deepEqual(thorough.items, []);
  assert.ok(thorough.stats.scannedNodes > 100, 'verified mode must descend to confirm');
});

test('custom value equality controls what counts as changed', () => {
  const store = new MapStore<number, { n: number }>();
  let v1 = store.empty();
  v1 = store.set(v1, 1, { n: 5 });
  const v2 = store.set(v1, 1, { n: 5 });
  const strict = store.differ();
  const strictItems = collectAll(strict, strict.beginDiff(v1, v2), 10).items;
  assert.equal(strictItems.length, 1);
  assert.equal(strictItems[0]!.type, 'changed');
  const byField = store.differ({ valuesEqual: (x, y) => x.n === y.n });
  const byFieldItems = collectAll(byField, byField.beginDiff(v1, v2), 10).items;
  assert.deepEqual(byFieldItems, []);
});
