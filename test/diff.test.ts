import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  PMap,
  VersionStore,
  VersionReleasedError,
  openDiff,
  diffPage,
  referenceDiff,
  type CancelSignal,
  type DiffCursor,
  type DiffItem,
  type DiffOptions,
  type HashTrust,
} from '../src/index.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Signal that flips to aborted after `n` reads of `.aborted`. */
function abortAfter(n: number): CancelSignal {
  let reads = 0;
  return {
    get aborted() {
      reads += 1;
      return reads > n;
    },
  };
}

interface CollectResult<K, V> {
  items: DiffItem<K, V>[];
  pages: number;
  scannedKeys: number;
  skippedKeys: number;
}

function collectAll<K, V>(
  store: VersionStore<K, V>,
  cursor: DiffCursor<K>,
  options: DiffOptions<K, V> = {},
): CollectResult<K, V> {
  const items: DiffItem<K, V>[] = [];
  let pages = 0;
  let scannedKeys = 0;
  let skippedKeys = 0;
  let current = cursor;
  for (;;) {
    const page = diffPage(store, current, options);
    assert.equal(page.cancelled, false, 'unexpected cancellation');
    items.push(...page.items);
    scannedKeys += page.scannedKeys;
    skippedKeys += page.skippedKeys;
    pages += 1;
    current = page.cursor;
    if (page.done) break;
    assert.ok(pages < 100000, 'pagination did not terminate');
  }
  return { items, pages, scannedKeys, skippedKeys };
}

/** Build two versions sharing most of their structure. */
function randomPair(seed: number): { v1: PMap<number, number>; v2: PMap<number, number> } {
  const rng = mulberry32(seed);
  let v1 = PMap.empty<number, number>();
  const n = 1 + Math.floor(rng() * 200);
  for (let i = 0; i < n; i++) v1 = v1.set(Math.floor(rng() * 1000), Math.floor(rng() * 1e6));
  let v2 = v1;
  const mutations = Math.floor(rng() * 80);
  for (let i = 0; i < mutations; i++) {
    const key = Math.floor(rng() * 1000);
    const roll = rng();
    if (roll < 0.45) v2 = v2.set(key, Math.floor(rng() * 1e6));
    else if (roll < 0.75) v2 = v2.delete(key);
    else v2 = v2.set(key, Math.floor(rng() * 1e6));
  }
  return { v1, v2 };
}

test('basic diff: added, removed, changed', () => {
  const store = new VersionStore<number, string>();
  const v1 = PMap.from<number, string>([[1, 'a'], [2, 'b'], [3, 'c'], [5, 'e']]);
  const v2 = PMap.from<number, string>([[2, 'b'], [3, 'C'], [4, 'd'], [5, 'e']]);
  const left = store.commit(v1);
  const right = store.commit(v2);
  const { items } = collectAll(store, openDiff(left, right));
  assert.deepEqual(items, [
    { type: 'removed', key: 1, value: 'a' },
    { type: 'changed', key: 3, oldValue: 'c', newValue: 'C' },
    { type: 'added', key: 4, value: 'd' },
  ]);
});

test('paginated diff matches full-merge reference (randomized)', () => {
  const trusts: HashTrust[] = ['verified', 'trusted'];
  for (let seed = 1; seed <= 60; seed++) {
    const { v1, v2 } = randomPair(seed);
    const expected = referenceDiff(v1, v2);
    for (const pageSize of [1, 2, 3, 7, 1000]) {
      for (const hashTrust of trusts) {
        const store = new VersionStore<number, number>();
        const left = store.commit(v1);
        const right = store.commit(v2);
        const { items } = collectAll(store, openDiff(left, right), {
          pageSize,
          hashTrust,
        });
        assert.deepEqual(
          items,
          expected,
          `seed=${seed} pageSize=${pageSize} trust=${hashTrust}`,
        );
      }
    }
  }
});

test('page size limits output items, not scanned keys', () => {
  const store = new VersionStore<number, number>();
  let v1 = PMap.empty<number, number>();
  for (let i = 0; i < 5000; i++) v1 = v1.set(i, i);
  // Only one real difference buried in 5000 identical keys.
  const v2 = v1.set(4999, -1);
  const left = store.commit(v1);
  const right = store.commit(v2);

  const page = diffPage(store, openDiff(left, right), { pageSize: 1 });
  assert.equal(page.items.length, 1);
  assert.deepEqual(page.items[0], {
    type: 'changed',
    key: 4999,
    oldValue: 4999,
    newValue: -1,
  });
  // The single output item required scanning/skipping the whole map.
  assert.ok(
    page.scannedKeys + page.skippedKeys >= 5000,
    `scanned=${page.scannedKeys} skipped=${page.skippedKeys}`,
  );
  // The page filled exactly, so completion is only known after one more
  // (empty) page.
  assert.equal(page.done, false);
  const tail = diffPage(store, page.cursor, { pageSize: 1 });
  assert.deepEqual(tail.items, []);
  assert.equal(tail.done, true);
});

test('long identical prefix: shared subtrees are skipped wholesale', () => {
  const store = new VersionStore<number, number>();
  let v1 = PMap.empty<number, number>();
  for (let i = 0; i < 20000; i++) v1 = v1.set(i, i);
  const v2 = v1.set(19998, -1).set(20000, 1); // tail divergence only
  const left = store.commit(v1);
  const right = store.commit(v2);

  const result = collectAll(store, openDiff(left, right), { pageSize: 10 });
  assert.deepEqual(result.items, [
    { type: 'changed', key: 19998, oldValue: 19998, newValue: -1 },
    { type: 'added', key: 20000, value: 1 },
  ]);
  // Most of the 2*20000 keys were skipped as identical subtrees...
  assert.ok(
    result.skippedKeys > 30000,
    `skipped=${result.skippedKeys}`,
  );
  // ...and only a small frontier was actually walked node by node.
  assert.ok(
    result.scannedKeys < 1000,
    `scanned=${result.scannedKeys}`,
  );
});

test('identical maps produce an empty first page with done=true', () => {
  const store = new VersionStore<number, number>();
  let v1 = PMap.empty<number, number>();
  for (let i = 0; i < 1000; i++) v1 = v1.set(i, i);
  const left = store.commit(v1);
  const right = store.commit(v1); // same version
  const page = diffPage(store, openDiff(left, right), { pageSize: 5 });
  assert.deepEqual(page.items, []);
  assert.equal(page.done, true);
  assert.equal(page.cancelled, false);
});

test('equal content rebuilt from scratch: trust model decides skipping', () => {
  const entries: [number, number][] = [];
  for (let i = 0; i < 2000; i++) entries.push([i, i * 7]);
  const v1 = PMap.from(entries);
  const v2 = PMap.from(entries); // same content, zero shared nodes
  assert.notEqual(v1.root, v2.root);

  const store = new VersionStore<number, number>();
  const left = store.commit(v1);
  const right = store.commit(v2);

  // 'verified': hashes match but nodes differ -> full element-wise check.
  const verified = collectAll(store, openDiff(left, right), {
    hashTrust: 'verified',
  });
  assert.deepEqual(verified.items, []);
  assert.equal(verified.scannedKeys, 4000);
  assert.equal(verified.skippedKeys, 0);

  // 'trusted': equal hashes are accepted as proof -> whole tree skipped.
  const trusted = collectAll(store, openDiff(left, right), {
    hashTrust: 'trusted',
  });
  assert.deepEqual(trusted.items, []);
  assert.equal(trusted.scannedKeys, 0);
  assert.equal(trusted.skippedKeys, 4000);
});

test('hash collisions: verified stays correct, trusted may mask diffs', () => {
  // Pathological hasher: every key/value hashes the same, so node hashes
  // depend only on tree shape. Same-shape trees collide everywhere.
  const constHash = () => 0xdeadbeef;
  const options = { hashKey: constHash, hashValue: constHash };
  let v1 = PMap.empty<number, number>(options);
  for (let i = 0; i < 100; i++) v1 = v1.set(i, i);
  let v2 = v1;
  for (let i = 0; i < 100; i += 2) v2 = v2.set(i, -i); // values only, shape kept
  assert.equal(v1.root!.hash, v2.root!.hash, 'expected a full-tree collision');

  const expected = referenceDiff(v1, v2);
  assert.equal(expected.length, 50);

  const store = new VersionStore<number, number>();
  const left = store.commit(v1);
  const right = store.commit(v2);

  // 'verified' descends despite equal hashes and finds every change.
  const verified = collectAll(store, openDiff(left, right), {
    hashTrust: 'verified',
  });
  assert.deepEqual(verified.items, expected);

  // 'trusted' accepts the colliding root hash and reports nothing:
  // the documented hazard the caller opts into.
  const trusted = collectAll(store, openDiff(left, right), {
    hashTrust: 'trusted',
  });
  assert.deepEqual(trusted.items, []);
});

test('empty maps and one-sided diffs', () => {
  const store = new VersionStore<number, number>();
  const empty = PMap.empty<number, number>();
  const full = PMap.from<number, number>([[1, 1], [2, 2], [3, 3]]);
  const e = store.commit(empty);
  const f = store.commit(full);

  assert.deepEqual(collectAll(store, openDiff(e, e)).items, []);
  assert.deepEqual(collectAll(store, openDiff(e, f)).items, [
    { type: 'added', key: 1, value: 1 },
    { type: 'added', key: 2, value: 2 },
    { type: 'added', key: 3, value: 3 },
  ]);
  assert.deepEqual(collectAll(store, openDiff(f, e)).items, [
    { type: 'removed', key: 1, value: 1 },
    { type: 'removed', key: 2, value: 2 },
    { type: 'removed', key: 3, value: 3 },
  ]);
});

test('empty page: pre-aborted signal yields nothing but stays resumable', () => {
  const store = new VersionStore<number, number>();
  const { v1, v2 } = randomPair(777);
  const left = store.commit(v1);
  const right = store.commit(v2);

  const page = diffPage(store, openDiff(left, right), {
    pageSize: 10,
    signal: abortAfter(0), // aborted before any work
  });
  assert.deepEqual(page.items, []);
  assert.equal(page.cancelled, true);
  assert.equal(page.done, false);
  assert.equal(page.cursor.lastKey, null);

  // Resuming from the empty page's cursor yields the complete diff.
  const rest = collectAll(store, page.cursor, { pageSize: 3 });
  assert.deepEqual(rest.items, referenceDiff(v1, v2));
});

test('cancellation: every page can be resumed exactly', () => {
  const store = new VersionStore<number, number>();
  const { v1, v2 } = randomPair(1234);
  const expected = referenceDiff(v1, v2);
  assert.ok(expected.length > 0, 'scenario should contain differences');
  const left = store.commit(v1);
  const right = store.commit(v2);

  // Abort each page after a single output item, then resume.
  const items: DiffItem<number, number>[] = [];
  let cursor: DiffCursor<number> = openDiff(left, right);
  let cancelledPages = 0;
  for (;;) {
    const page = diffPage(store, cursor, {
      pageSize: 1000,
      signal: abortAfter(2), // ~1 item per page
    });
    items.push(...page.items);
    cursor = page.cursor;
    if (page.cancelled) cancelledPages += 1;
    if (page.done) break;
    assert.ok(cancelledPages < 100000, 'cancellation loop did not terminate');
  }
  assert.ok(cancelledPages > 0);
  assert.deepEqual(items, expected);
});

test('released versions fail loudly, including mid-diff', () => {
  const store = new VersionStore<number, number>();
  const { v1, v2 } = randomPair(99);
  const left = store.commit(v1);
  const right = store.commit(v2);

  // Released before the diff even starts.
  store.release(right);
  assert.throws(
    () => diffPage(store, openDiff(left, right)),
    (err: unknown) => err instanceof VersionReleasedError,
  );

  // Released between pages: the resume fails before emitting anything.
  const store2 = new VersionStore<number, number>();
  const left2 = store2.commit(v1);
  const right2 = store2.commit(v2);
  const first = diffPage(store2, openDiff(left2, right2), { pageSize: 1 });
  store2.release(left2);
  assert.throws(
    () => diffPage(store2, first.cursor),
    (err: unknown) =>
      err instanceof VersionReleasedError &&
      (err as VersionReleasedError).version === left2,
  );
});

test('cursor stays bound to its two versions across pages', () => {
  const store = new VersionStore<number, number>();
  const { v1, v2 } = randomPair(5);
  const left = store.commit(v1);
  const right = store.commit(v2);
  let cursor: DiffCursor<number> = openDiff(left, right);
  for (;;) {
    const page = diffPage(store, cursor, { pageSize: 2 });
    assert.equal(page.cursor.left, left);
    assert.equal(page.cursor.right, right);
    cursor = page.cursor;
    if (page.done) break;
  }
});

test('items arrive in strictly ascending key order across pages', () => {
  const store = new VersionStore<number, number>();
  const { v1, v2 } = randomPair(31337);
  const left = store.commit(v1);
  const right = store.commit(v2);
  let previous = -Infinity;
  let cursor: DiffCursor<number> = openDiff(left, right);
  for (;;) {
    const page = diffPage(store, cursor, { pageSize: 3 });
    for (const item of page.items) {
      assert.ok(item.key > previous, 'keys must ascend across pages');
      previous = item.key;
    }
    cursor = page.cursor;
    if (page.done) break;
  }
});
