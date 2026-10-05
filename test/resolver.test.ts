import test from 'node:test';
import assert from 'node:assert/strict';
import {
  AliasResolver,
  AliasStore,
  DEFAULT_MAX_HOPS,
  referenceResolve,
} from '../src/index.ts';
import type { AliasOp, Resolution, Snapshot, Target } from '../src/index.ts';

const obj = (name: string, version: number): Target => ({ kind: 'object', name, version });
const ali = (name: string): Target => ({ kind: 'alias', name });

function setup() {
  const store = new AliasStore();
  const resolver = new AliasResolver(store);
  return { store, resolver };
}

test('resolves an alias to a concrete object version with the full path', () => {
  const { store, resolver } = setup();
  store.putObject('doc', 3, 'v3-data');
  store.commitAliasBatch([{ type: 'set', alias: 'latest', target: obj('doc', 3) }]);

  const res = resolver.resolve('latest');
  assert.equal(res.status, 'ok');
  assert.deepEqual(res.object, { name: 'doc', version: 3 });
  assert.deepEqual(res.path, [{ alias: 'latest', target: obj('doc', 3) }]);
});

test('follows alias chains and returns every hop in order', () => {
  const { store, resolver } = setup();
  store.putObject('o', 1, null);
  store.commitAliasBatch([
    { type: 'set', alias: 'a', target: ali('b') },
    { type: 'set', alias: 'b', target: ali('c') },
    { type: 'set', alias: 'c', target: obj('o', 1) },
  ]);

  const res = resolver.resolve('a');
  assert.equal(res.status, 'ok');
  assert.deepEqual(
    res.path.map((hop) => hop.alias),
    ['a', 'b', 'c'],
  );
  assert.deepEqual(res.path[1].target, ali('c'));
});

test('enforces the max hop limit and reports the partial path', () => {
  const { store, resolver } = setup();
  store.putObject('o', 1, null);
  store.commitAliasBatch([
    { type: 'set', alias: 'c0', target: ali('c1') },
    { type: 'set', alias: 'c1', target: ali('c2') },
    { type: 'set', alias: 'c2', target: ali('c3') },
    { type: 'set', alias: 'c3', target: obj('o', 1) },
  ]);

  const limited = resolver.resolve('c0', { maxHops: 2 });
  assert.equal(limited.status, 'max-hops-exceeded');
  assert.equal(limited.path.length, 2);

  const enough = resolver.resolve('c0', { maxHops: 4 });
  assert.equal(enough.status, 'ok');

  const byDefault = resolver.resolve('c0');
  assert.equal(byDefault.status, 'ok');
  assert.ok(DEFAULT_MAX_HOPS >= 4);
});

test('reports cycles and dangling targets as distinct outcomes', () => {
  const { store, resolver } = setup();
  // Cycle created atomically in a single batch.
  store.commitAliasBatch([
    { type: 'set', alias: 'x', target: ali('y') },
    { type: 'set', alias: 'y', target: ali('x') },
    { type: 'set', alias: 'loop', target: ali('loop') },
    { type: 'set', alias: 'lost', target: ali('ghost') },
    { type: 'set', alias: 'broken', target: obj('o', 9) },
  ]);

  const cyc = resolver.resolve('x');
  assert.equal(cyc.status, 'cycle');
  assert.deepEqual(cyc.cycle, ['x', 'y', 'x']);
  assert.equal(cyc.path.length, 2);

  const selfLoop = resolver.resolve('loop');
  assert.equal(selfLoop.status, 'cycle');
  assert.deepEqual(selfLoop.cycle, ['loop', 'loop']);

  const danglingAlias = resolver.resolve('lost');
  assert.equal(danglingAlias.status, 'dangling');
  assert.deepEqual(danglingAlias.missing, ali('ghost'));

  const danglingObject = resolver.resolve('broken');
  assert.equal(danglingObject.status, 'dangling');
  assert.deepEqual(danglingObject.missing, obj('o', 9));

  assert.equal(resolver.resolve('ghost').status, 'missing-alias');
});

test('alias batch updates are atomic: a failing batch changes nothing', () => {
  const { store, resolver } = setup();
  store.putObject('o', 1, null);
  store.commitAliasBatch([{ type: 'set', alias: 'keep', target: obj('o', 1) }]);
  const epochBefore = store.epoch;

  assert.throws(
    () =>
      store.commitAliasBatch([
        { type: 'set', alias: 'dup', target: obj('o', 1) },
        { type: 'set', alias: 'dup', target: ali('keep') },
      ]),
    /duplicate alias/,
  );
  assert.throws(
    () =>
      store.commitAliasBatch([
        { type: 'set', alias: 'bad', target: obj('o', 0) },
        { type: 'set', alias: 'good', target: obj('o', 1) },
      ]),
    /invalid object target version/,
  );

  assert.equal(store.epoch, epochBefore);
  assert.equal(resolver.resolve('dup').status, 'missing-alias');
  assert.equal(resolver.resolve('bad').status, 'missing-alias');
  assert.equal(resolver.resolve('good').status, 'missing-alias');
  const kept = resolver.resolve('keep');
  assert.equal(kept.status, 'ok');
  assert.deepEqual(kept.object, { name: 'o', version: 1 });
});

test('multiple aliases may share one target and all invalidate together', () => {
  const { store, resolver } = setup();
  store.putObject('shared', 1, null);
  store.commitAliasBatch([
    { type: 'set', alias: 's1', target: obj('shared', 1) },
    { type: 'set', alias: 's2', target: obj('shared', 1) },
    { type: 'set', alias: 's3', target: ali('s1') },
  ]);

  for (const name of ['s1', 's2', 's3']) {
    const res = resolver.resolve(name);
    assert.equal(res.status, 'ok');
    assert.deepEqual(res.object, { name: 'shared', version: 1 });
  }

  store.deleteObject('shared', 1);
  for (const name of ['s1', 's2', 's3']) {
    const res = resolver.resolve(name);
    assert.equal(res.status, 'dangling');
    assert.deepEqual(res.missing, obj('shared', 1));
  }
});

test('negative dependency: filling a missing alias target invalidates the cache', () => {
  const { store, resolver } = setup();
  store.commitAliasBatch([{ type: 'set', alias: 'p', target: ali('gap') }]);

  const before = resolver.resolve('p');
  assert.equal(before.status, 'dangling');
  assert.deepEqual(before.missing, ali('gap'));
  const hitsBefore = resolver.stats.hits;

  // Sanity: without any commit the dangling result is served from cache.
  resolver.resolve('p');
  assert.equal(resolver.stats.hits, hitsBefore + 1);

  store.putObject('o', 1, null);
  store.commitAliasBatch([{ type: 'set', alias: 'gap', target: obj('o', 1) }]);

  const after = resolver.resolve('p');
  assert.equal(after.status, 'ok');
  assert.deepEqual(after.object, { name: 'o', version: 1 });
  assert.deepEqual(
    after.path.map((hop) => hop.alias),
    ['p', 'gap'],
  );
});

test('negative dependency: filling a missing object version invalidates the cache', () => {
  const { store, resolver } = setup();
  store.commitAliasBatch([{ type: 'set', alias: 'r', target: obj('o', 9) }]);

  const before = resolver.resolve('r');
  assert.equal(before.status, 'dangling');
  assert.deepEqual(before.missing, obj('o', 9));

  store.putObject('o', 9, 'now-exists');
  const after = resolver.resolve('r');
  assert.equal(after.status, 'ok');
  assert.deepEqual(after.object, { name: 'o', version: 9 });
});

test('fine-grained invalidation keeps entries whose deps were untouched', () => {
  const { store, resolver } = setup();
  store.putObject('o', 1, null);
  store.commitAliasBatch([
    { type: 'set', alias: 'stable', target: obj('o', 1) },
    { type: 'set', alias: 'hole', target: ali('gap') },
  ]);
  resolver.resolve('stable');
  resolver.resolve('hole');
  const hitsBefore = resolver.stats.hits;

  // Touches only alias:gap; the cached entry for "stable" must survive.
  store.commitAliasBatch([{ type: 'set', alias: 'gap', target: obj('o', 1) }]);

  resolver.resolve('stable');
  assert.equal(resolver.stats.hits, hitsBefore + 1);
  assert.equal(resolver.resolve('hole').status, 'ok');
});

test('delete then recreate the same alias name with a different target', () => {
  const { store, resolver } = setup();
  store.putObject('o', 1, 'first');
  store.putObject('o', 2, 'second');
  store.commitAliasBatch([{ type: 'set', alias: 'tmp', target: obj('o', 1) }]);

  const v1 = resolver.resolve('tmp');
  assert.equal(v1.status, 'ok');
  assert.deepEqual(v1.object, { name: 'o', version: 1 });

  store.commitAliasBatch([{ type: 'delete', alias: 'tmp' }]);
  assert.equal(resolver.resolve('tmp').status, 'missing-alias');

  store.commitAliasBatch([{ type: 'set', alias: 'tmp', target: obj('o', 2) }]);
  const v2 = resolver.resolve('tmp');
  assert.equal(v2.status, 'ok');
  assert.deepEqual(v2.object, { name: 'o', version: 2 });
});

test('old snapshots resolve against their own epoch, not the latest state', () => {
  const { store, resolver } = setup();
  store.putObject('o', 1, 'old');
  store.commitAliasBatch([{ type: 'set', alias: 'cur', target: obj('o', 1) }]);
  const snap1 = store.snapshot();

  store.putObject('o', 2, 'new');
  store.commitAliasBatch([
    { type: 'set', alias: 'cur', target: ali('next') },
    { type: 'set', alias: 'next', target: obj('o', 2) },
  ]);

  // Populate the cache at the latest epoch first, then ask for the old one:
  // the newer cached entry must not leak into the older snapshot.
  const latest = resolver.resolve('cur');
  assert.equal(latest.status, 'ok');
  assert.deepEqual(latest.object, { name: 'o', version: 2 });

  const old = resolver.resolve('cur', { snapshot: snap1 });
  assert.equal(old.status, 'ok');
  assert.deepEqual(old.object, { name: 'o', version: 1 });
  assert.deepEqual(old.path, [{ alias: 'cur', target: obj('o', 1) }]);

  // And resolving the latest epoch again must not reuse the old-epoch entry.
  const latestAgain = resolver.resolve('cur');
  assert.equal(latestAgain.status, 'ok');
  assert.deepEqual(latestAgain.object, { name: 'o', version: 2 });
});

function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('differential: cached resolver matches the cache-free reference over random histories', () => {
  const rand = mulberry32(0xc0ffee);
  const store = new AliasStore();
  const resolver = new AliasResolver(store);
  const aliasNames = Array.from({ length: 8 }, (_, i) => `a${i}`);
  const objectNames = Array.from({ length: 4 }, (_, i) => `o${i}`);
  const snapshots: Snapshot[] = [store.snapshot()];

  const pick = <T>(items: T[]): T => items[Math.floor(rand() * items.length)];
  const randomTarget = (): Target =>
    rand() < 0.5
      ? ali(pick(aliasNames))
      : obj(pick(objectNames), 1 + Math.floor(rand() * 3));

  const checkedStatuses = new Set<string>();

  for (let step = 0; step < 400; step++) {
    const roll = rand();
    if (roll < 0.55) {
      const batchSize = 1 + Math.floor(rand() * 4);
      const ops: AliasOp[] = [];
      const used = new Set<string>();
      for (let i = 0; i < batchSize; i++) {
        const name = pick(aliasNames);
        if (used.has(name)) continue;
        used.add(name);
        ops.push(
          rand() < 0.75
            ? { type: 'set', alias: name, target: randomTarget() }
            : { type: 'delete', alias: name },
        );
      }
      store.commitAliasBatch(ops);
    } else if (roll < 0.8) {
      store.putObject(pick(objectNames), 1 + Math.floor(rand() * 3), { step });
    } else {
      store.deleteObject(pick(objectNames), 1 + Math.floor(rand() * 3));
    }

    if (rand() < 0.2) snapshots.push(store.snapshot());
    if (snapshots.length > 12) snapshots.splice(0, snapshots.length - 12);

    for (let k = 0; k < 4; k++) {
      const name = pick(aliasNames);
      const maxHops = 1 + Math.floor(rand() * 6);
      const snap = rand() < 0.7 ? store.snapshot() : pick(snapshots);
      const actual = resolver.resolve(name, { maxHops, snapshot: snap });
      const expected = referenceResolve(snap, name, maxHops);
      checkedStatuses.add(expected.status);
      assert.deepEqual(
        actual,
        expected,
        `step=${step} name=${name} maxHops=${maxHops} epoch=${snap.epoch}`,
      );
    }
  }

  // The random history exercised every outcome class...
  for (const status of ['ok', 'dangling', 'cycle', 'max-hops-exceeded', 'missing-alias']) {
    assert.ok(checkedStatuses.has(status), `expected to exercise status ${status}`);
  }
  // ...and the cache actually engaged.
  assert.ok(resolver.stats.hits > 0, 'expected at least one cache hit');
});

test('resolution results are not mutated by later resolves (cache safety)', () => {
  const { store, resolver } = setup();
  store.putObject('o', 1, null);
  store.commitAliasBatch([
    { type: 'set', alias: 'p0', target: ali('p1') },
    { type: 'set', alias: 'p1', target: obj('o', 1) },
  ]);
  const first: Resolution = resolver.resolve('p0');
  const frozen = JSON.parse(JSON.stringify(first));
  resolver.resolve('p1');
  resolver.resolve('p0');
  assert.deepEqual(first, frozen);
});
