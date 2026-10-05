/* eslint-disable no-console */
// No @types/node in this offline environment; declare the few globals used.
declare const require: (id: string) => any;
declare const process: { exitCode: number };

const assert = require('assert');

import {
  AliasOp,
  AliasTarget,
  CachedResolver,
  ResolveResult,
  Store,
  resolveUncached,
} from '../src';

let passed = 0;
let failed = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok   ${name}`);
  } catch (err) {
    failed++;
    console.error(`FAIL ${name}`);
    console.error(err instanceof Error ? err.stack : err);
  }
}

function obj(objectId: string, version: number): AliasTarget {
  return { kind: 'object', objectId, version };
}

function ali(alias: string): AliasTarget {
  return { kind: 'alias', alias };
}

// ---------------------------------------------------------------- targeted

test('shared target: many aliases resolve to the same object version', () => {
  const store = new Store();
  store.putObject('doc', 1, { text: 'v1' });
  const out = store.applyBatch([
    { type: 'set', alias: 'latest', target: obj('doc', 1) },
    { type: 'set', alias: 'stable', target: obj('doc', 1) },
    { type: 'set', alias: 'prod', target: ali('stable') },
  ]);
  assert.strictEqual(out.applied, true);
  for (const name of ['latest', 'stable', 'prod']) {
    const r = resolveUncached(store, name);
    assert.strictEqual(r.ok, true, name);
    if (r.ok) assert.deepStrictEqual(r.object, { kind: 'object', objectId: 'doc', version: 1 });
  }
  const prod = resolveUncached(store, 'prod');
  assert.deepStrictEqual(prod.ok ? prod.path : null, ['prod', 'stable']);
});

test('full path is returned across a multi-hop chain', () => {
  const store = new Store();
  store.putObject('o', 7, null);
  store.applyBatch([
    { type: 'set', alias: 'a', target: ali('b') },
    { type: 'set', alias: 'b', target: ali('c') },
    { type: 'set', alias: 'c', target: obj('o', 7) },
  ]);
  const r = resolveUncached(store, 'a');
  assert.deepStrictEqual(r, {
    ok: true,
    path: ['a', 'b', 'c'],
    object: { kind: 'object', objectId: 'o', version: 7 },
  });
});

test('max hops limits the walk and returns the partial path', () => {
  const store = new Store();
  store.putObject('o', 1, null);
  const ops: AliasOp[] = [{ type: 'set', alias: 'n9', target: obj('o', 1) }];
  for (let i = 8; i >= 0; i--) ops.unshift({ type: 'set', alias: `n${i}`, target: ali(`n${i + 1}`) });
  const out = store.applyBatch(ops);
  assert.strictEqual(out.applied, true);
  const r = resolveUncached(store, 'n0', { maxHops: 4 });
  assert.deepStrictEqual(r, { ok: false, reason: 'max-hops', path: ['n0', 'n1', 'n2', 'n3', 'n4'] });
  const full = resolveUncached(store, 'n0', { maxHops: 32 });
  assert.strictEqual(full.ok, true);
});

test('batch-created cycle is rejected atomically and reported as a cycle', () => {
  const store = new Store();
  store.putObject('o', 1, null);
  const seqBefore = store.currentSeq;
  const out = store.applyBatch([
    { type: 'set', alias: 'a', target: ali('b') },
    { type: 'set', alias: 'b', target: ali('c') },
    { type: 'set', alias: 'c', target: ali('a') },
    { type: 'set', alias: 'fine', target: obj('o', 1) },
  ]);
  assert.strictEqual(out.applied, false);
  if (!out.applied) {
    assert.strictEqual(out.cycles.length, 1, 'one canonical cycle');
    assert.deepStrictEqual(out.cycles[0]!.cycle.slice(0, -1).sort(), ['a', 'b', 'c']);
    assert.strictEqual(out.dangling.length, 0, 'cycles and dangling reported separately');
  }
  // Atomic: nothing was written, not even the valid op; sequence did not move.
  assert.strictEqual(store.currentSeq, seqBefore);
  assert.deepStrictEqual(store.listAliases(), []);
  const r = resolveUncached(store, 'fine');
  assert.strictEqual(r.ok, false);
  if (!r.ok) assert.strictEqual(r.reason, 'dangling');
});

test('cycle through a pre-existing alias is still caught', () => {
  const store = new Store();
  store.applyBatch([{ type: 'set', alias: 'x', target: ali('y') }], { allowDangling: true });
  const out = store.applyBatch([{ type: 'set', alias: 'y', target: ali('x') }]);
  assert.strictEqual(out.applied, false);
  if (!out.applied) assert.strictEqual(out.cycles.length, 1);
  // Rejected: y still dangling, x still points at y.
  const y = resolveUncached(store, 'y');
  assert.strictEqual(y.ok, false);
  if (!y.ok && y.reason === 'dangling') {
    assert.deepStrictEqual(y.missing, { kind: 'alias', alias: 'y' });
  }
});

test('dangling targets are reported separately from cycles', () => {
  const store = new Store();
  store.putObject('o', 1, null);
  const seqBefore = store.currentSeq;
  const out = store.applyBatch([
    { type: 'set', alias: 'to-missing-alias', target: ali('ghost') },
    { type: 'set', alias: 'to-missing-object', target: obj('nope', 3) },
  ]);
  assert.strictEqual(out.applied, false);
  if (!out.applied) {
    assert.strictEqual(out.cycles.length, 0);
    assert.strictEqual(out.dangling.length, 2);
    const missings = out.dangling.map((d) => d.missing);
    assert.deepStrictEqual(missings.find((m) => m.kind === 'alias'), { kind: 'alias', alias: 'ghost' });
    assert.deepStrictEqual(
      missings.find((m) => m.kind === 'object'),
      { kind: 'object', objectId: 'nope', version: 3 },
    );
  }
  assert.strictEqual(store.currentSeq, seqBefore);
});

test('allowDangling applies the batch but still reports the dangling chains', () => {
  const store = new Store();
  const out = store.applyBatch(
    [{ type: 'set', alias: 'a', target: ali('b') }],
    { allowDangling: true },
  );
  assert.strictEqual(out.applied, true);
  if (out.applied) assert.strictEqual(out.dangling.length, 1);
});

test('negative dependency: filling a missing alias invalidates the cache', () => {
  const store = new Store();
  const cached = new CachedResolver(store);
  store.applyBatch([{ type: 'set', alias: 'a', target: ali('b') }], { allowDangling: true });

  const before = cached.resolve('a');
  assert.strictEqual(before.ok, false);
  if (!before.ok && before.reason === 'dangling') {
    assert.deepStrictEqual(before.missing, { kind: 'alias', alias: 'b' });
  }

  const entry = cached.inspect('a');
  assert.ok(entry, 'cached');
  const negDep = entry!.deps.find((d) => d.kind === 'alias' && d.alias === 'b');
  assert.ok(negDep, 'dependency on b recorded');
  assert.strictEqual(negDep!.existed, false, 'negative dependency recorded');

  store.putObject('o', 1, null);
  store.applyBatch([{ type: 'set', alias: 'b', target: obj('o', 1) }]);
  assert.strictEqual(cached.inspect('a'), undefined, 'entry invalidated by negative dep');

  const after = cached.resolve('a');
  assert.deepStrictEqual(after, {
    ok: true,
    path: ['a', 'b'],
    object: { kind: 'object', objectId: 'o', version: 1 },
  });
});

test('negative dependency: filling a missing object version invalidates the cache', () => {
  const store = new Store();
  const cached = new CachedResolver(store);
  store.applyBatch([{ type: 'set', alias: 'a', target: obj('o', 9) }], { allowDangling: true });

  const before = cached.resolve('a');
  assert.strictEqual(before.ok, false);
  const negDep = cached.inspect('a')!.deps.find((d) => d.kind === 'object');
  assert.ok(negDep && !negDep.existed, 'negative object dependency recorded');

  store.putObject('o', 9, 'now it exists');
  assert.strictEqual(cached.inspect('a'), undefined, 'entry invalidated');
  const after = cached.resolve('a');
  assert.strictEqual(after.ok, true);
});

test('delete then recreate the same alias name; old snapshot is preserved', () => {
  const store = new Store();
  const cached = new CachedResolver(store);
  store.putObject('o1', 1, 'first');
  store.putObject('o2', 1, 'second');

  store.applyBatch([{ type: 'set', alias: 'name', target: obj('o1', 1) }]);
  const seqBeforeDelete = store.currentSeq;
  const r1 = cached.resolve('name');
  assert.strictEqual(r1.ok, true);
  if (r1.ok) assert.strictEqual(r1.object.objectId, 'o1');

  store.applyBatch([{ type: 'delete', alias: 'name' }]);
  const r2 = cached.resolve('name');
  assert.deepStrictEqual(r2, {
    ok: false,
    reason: 'dangling',
    path: ['name'],
    missing: { kind: 'alias', alias: 'name' },
  });

  store.applyBatch([{ type: 'set', alias: 'name', target: obj('o2', 1) }]);
  const r3 = cached.resolve('name');
  assert.strictEqual(r3.ok, true);
  if (r3.ok) assert.strictEqual(r3.object.objectId, 'o2', 'recreated name sees new target');

  const historical = resolveUncached(store, 'name', { snapshot: seqBeforeDelete });
  assert.strictEqual(historical.ok, true);
  if (historical.ok) assert.strictEqual(historical.object.objectId, 'o1', 'old snapshot unchanged');
});

test('retargeting a shared target invalidates dependents, not unrelated entries', () => {
  const store = new Store();
  const cached = new CachedResolver(store);
  store.putObject('o', 1, 'v1');
  store.putObject('o', 2, 'v2');
  store.applyBatch([
    { type: 'set', alias: 'base', target: obj('o', 1) },
    { type: 'set', alias: 'dep', target: ali('base') },
    { type: 'set', alias: 'other', target: obj('o', 1) },
  ]);
  cached.resolve('dep');
  cached.resolve('other');

  store.applyBatch([{ type: 'set', alias: 'base', target: obj('o', 2) }]);
  assert.strictEqual(cached.inspect('dep'), undefined, 'dependent invalidated transitively');
  assert.ok(cached.inspect('other'), 'unrelated entry kept');

  const r = cached.resolve('dep');
  assert.strictEqual(r.ok, true);
  if (r.ok) assert.strictEqual(r.object.version, 2);
});

// ------------------------------------------------------------- differential

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function differentialRun(seed: number, steps: number): void {
  const rand = mulberry32(seed);
  const pick = <T>(items: T[]): T => items[Math.floor(rand() * items.length)]!;
  const store = new Store();
  const cached = new CachedResolver(store);
  const aliases = Array.from({ length: 8 }, (_, i) => `a${i}`);
  const objectIds = ['o0', 'o1', 'o2', 'o3'];
  const created = new Set<string>();
  const snapshots: Array<{ seq: number; alias: string; result: ResolveResult }> = [];

  const checkAll = (label: string): void => {
    for (const name of aliases) {
      const fromCache = cached.resolve(name);
      const reference = resolveUncached(store, name);
      assert.deepStrictEqual(fromCache, reference, `${label}: cached vs uncached for ${name}`);
    }
  };

  for (let step = 0; step < steps; step++) {
    const roll = rand();
    if (roll < 0.25) {
      // Create an object version (sometimes already-created ones are skipped).
      const objectId = pick(objectIds);
      const version = 1 + Math.floor(rand() * 3);
      const key = `${objectId}@${version}`;
      if (!created.has(key)) {
        created.add(key);
        store.putObject(objectId, version, { seed, step });
      }
    } else {
      // Atomic batch of 1-4 set/delete ops, sometimes allowing dangling.
      const ops: AliasOp[] = [];
      const count = 1 + Math.floor(rand() * 4);
      for (let i = 0; i < count; i++) {
        const alias = pick(aliases);
        if (rand() < 0.25) {
          ops.push({ type: 'delete', alias });
        } else if (rand() < 0.55) {
          // Alias target: occasionally forms cycles (pool is small on purpose).
          ops.push({ type: 'set', alias, target: ali(pick(aliases)) });
        } else {
          // Object target: sometimes missing, to exercise negative deps.
          const objectId = pick(objectIds);
          const version = 1 + Math.floor(rand() * 4);
          ops.push({ type: 'set', alias, target: obj(objectId, version) });
        }
      }
      store.applyBatch(ops, { allowDangling: rand() < 0.5 });
    }

    checkAll(`seed=${seed} step=${step}`);

    if (rand() < 0.15) {
      const alias = pick(aliases);
      snapshots.push({
        seq: store.currentSeq,
        alias,
        result: resolveUncached(store, alias),
      });
    }
  }

  // Old snapshots must remain reproducible after all later mutations.
  for (const snap of snapshots) {
    const replayed = resolveUncached(store, snap.alias, { snapshot: snap.seq });
    assert.deepStrictEqual(replayed, snap.result, `snapshot seq=${snap.seq} alias=${snap.alias}`);
  }
}

test('differential fuzz: cached resolver always matches uncached traversal', () => {
  for (const seed of [1, 7, 42, 1337, 20261005]) {
    differentialRun(seed, 400);
  }
});

test('differential fuzz: delete/recreate and batch cycles under one seed loop', () => {
  // Dense delete/recreate workload: tiny alias pool forces constant
  // same-name reuse, cycle attempts, and dangling fills.
  for (const seed of [11, 99]) {
    const rand = mulberry32(seed);
    const store = new Store();
    const cached = new CachedResolver(store);
    const aliases = ['x', 'y', 'z'];
    store.putObject('o', 1, null);
    store.putObject('o', 2, null);
    for (let step = 0; step < 300; step++) {
      const ops: AliasOp[] = aliases
        .filter(() => rand() < 0.7)
        .map((alias) =>
          rand() < 0.3
            ? { type: 'delete', alias }
            : {
                type: 'set',
                alias,
                target:
                  rand() < 0.5
                    ? ali(aliases[Math.floor(rand() * aliases.length)]!)
                    : obj('o', 1 + Math.floor(rand() * 2)),
              },
        );
      store.applyBatch(ops, { allowDangling: true });
      for (const name of aliases) {
        assert.deepStrictEqual(
          cached.resolve(name),
          resolveUncached(store, name),
          `seed=${seed} step=${step} alias=${name}`,
        );
      }
    }
  }
});

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
