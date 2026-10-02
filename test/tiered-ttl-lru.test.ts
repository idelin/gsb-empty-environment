import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setTimeout as sleep } from 'node:timers/promises';
import { TieredTtlLruCache } from '../src/tiered-ttl-lru.ts';

test('basic set/get/delete round-trip', () => {
  const cache = new TieredTtlLruCache<string, number>();
  cache.set('a', 1);
  cache.set('b', 2);
  assert.equal(cache.get('a'), 1);
  assert.equal(cache.get('b'), 2);
  assert.equal(cache.get('missing'), undefined);
  assert.equal(cache.delete('a'), true);
  assert.equal(cache.get('a'), undefined);
  assert.equal(cache.delete('a'), false);
  assert.ok(cache.verifyIntegrity());
  cache.dispose();
});

test('absolute TTL: read after deadline returns undefined and frees the entry', async () => {
  const cache = new TieredTtlLruCache<string, number>();
  cache.set('short', 1, { ttlMs: 20 });
  cache.set('long', 2, { ttlMs: 5000 });
  assert.equal(cache.get('short'), 1);
  await sleep(40);
  assert.equal(cache.get('short'), undefined, 'timed-out read must return empty');
  assert.equal(cache.size, 1, 'dead object must be unlinked from every index');
  assert.equal(cache.get('long'), 2);
  assert.ok(cache.verifyIntegrity());
  cache.dispose();
});

test('sliding TTD: hot access renews the deadline, idleness lets it die', async () => {
  const cache = new TieredTtlLruCache<string, number>();
  cache.set('hot', 7, { slidingMs: 120 });
  // Keep touching it well past the original 120ms window.
  for (let i = 0; i < 5; i++) {
    await sleep(50);
    assert.equal(cache.get('hot'), 7, `hit #${i + 1} should still be alive`);
  }
  // ~250ms elapsed on a 120ms window — only sliding renewal kept it alive.
  assert.equal(cache.get('hot'), 7);
  // Now leave it idle past the window.
  await sleep(180);
  assert.equal(cache.get('hot'), undefined, 'idle entry must expire after the sliding window');
  assert.equal(cache.size, 0);
  assert.ok(cache.verifyIntegrity());
  cache.dispose();
});

test('LRU hotness: O(1) promotion reorders the list, capacity evicts coldest', () => {
  const cache = new TieredTtlLruCache<string, number>({ maxSize: 3 });
  cache.set('a', 1);
  cache.set('b', 2);
  cache.set('c', 3);
  assert.deepEqual(cache.keys(), ['c', 'b', 'a']);
  cache.get('a'); // promote a to hottest
  assert.deepEqual(cache.keys(), ['a', 'c', 'b']);
  cache.set('d', 4); // overflows capacity -> evicts coldest ('b')
  assert.deepEqual(cache.keys(), ['d', 'a', 'c']);
  assert.equal(cache.get('b'), undefined);
  assert.equal(cache.stats.lruEvicted, 1);
  assert.ok(cache.verifyIntegrity());
  cache.dispose();
});

test('acceptance: 100 objects, 10..100ms TTL, hot reads, batch reap, zero leak', async () => {
  const cache = new TieredTtlLruCache<string, { id: number }>();
  const TOTAL = 100;

  // Insert 100 objects with staggered absolute TTLs from 10ms to 100ms.
  for (let i = 1; i <= TOTAL; i++) {
    cache.set(`obj-${i}`, { id: i }, { ttlMs: 10 + (i - 1) * (90 / (TOTAL - 1)) });
  }
  assert.equal(cache.size, TOTAL);
  assert.ok(cache.verifyIntegrity());

  // High-frequency access on the soon-to-expire slice (sliding renewal keeps
  // the hottest few alive; the rest of the slice still dies on schedule).
  const survivors = new Set<string>();
  for (let round = 0; round < 4; round++) {
    for (let i = 1; i <= 5; i++) {
      const key = `obj-${i}`;
      if (cache.get(key) !== undefined) {
        cache.set(key, { id: i }, { slidingMs: 120 }); // rescue via sliding TTD
        survivors.add(key);
      }
    }
    await sleep(15);
  }

  // Deterministic expiry probes: obj-50 (~54ms) must be dead by t=80ms.
  await sleep(50); // ~80ms total
  assert.equal(cache.get('obj-50'), undefined, 'expired object must read as empty');
  assert.equal(cache.get('obj-30'), undefined);

  // The rescued hot objects are still served.
  for (const key of survivors) {
    assert.notEqual(cache.get(key), undefined, `${key} rescued by sliding TTD`);
  }

  // Wait out every remaining deadline, then trigger ONE operation: the lazy
  // batch reaper must drain all dead objects in a single pass.
  await sleep(150);
  const before = cache.stats;
  const reaped = cache.sweep();
  assert.equal(cache.size, 0, 'batch sweep leaves no dead objects behind');
  assert.ok(reaped > 0);
  assert.equal(cache.stats.expiredReaped - before.expiredReaped, reaped);

  // Zero-leak teardown: every index is empty and structurally consistent.
  assert.ok(cache.verifyIntegrity());
  cache.dispose();
  assert.equal(cache.size, 0);
  assert.deepEqual(cache.keys(), []);
  assert.equal(cache.nextExpiryIn(), Infinity);
});

test('batch reaper drains a large expired prefix in one call', async () => {
  const cache = new TieredTtlLruCache<number, number>();
  for (let i = 0; i < 500; i++) cache.set(i, i, { ttlMs: 15 });
  cache.set(9999, 9999, { ttlMs: 60_000 });
  await sleep(30);
  const reaped = cache.sweep();
  assert.equal(reaped, 500, 'all 500 dead objects reaped in one batch pass');
  assert.equal(cache.size, 1);
  assert.ok(cache.verifyIntegrity());
  cache.dispose();
});

test('set on an existing key refreshes value, deadline and hotness', async () => {
  const cache = new TieredTtlLruCache<string, number>();
  cache.set('k', 1, { ttlMs: 25 });
  cache.set('other', 9);
  await sleep(15);
  cache.set('k', 2, { ttlMs: 5000 }); // re-arm deadline
  assert.deepEqual(cache.keys()[0], 'k', 'rewrite promotes to hottest');
  await sleep(25);
  assert.equal(cache.get('k'), 2, 'refreshed deadline must be honored');
  assert.ok(cache.verifyIntegrity());
  cache.dispose();
});

test('background janitor reaps during idle periods and unrefs its timer', async () => {
  const cache = new TieredTtlLruCache<string, number>();
  cache.set('x', 1, { ttlMs: 20 });
  cache.startJanitor(10);
  await sleep(60);
  assert.equal(cache.stats.size, 0, 'janitor collected the dead object while idle');
  cache.stopJanitor();
  cache.dispose();
});
