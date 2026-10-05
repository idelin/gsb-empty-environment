import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync } from 'node:fs';
import { TransactionalStore } from '../src/store.ts';
import { ReferenceModel } from '../src/reference.ts';
import { generateLog, replay } from '../src/replay.ts';
import type { LogEntry } from '../src/replay.ts';
import type { JsonObject, JsonValue } from '../src/json.ts';

/* Independent ledger audit: weight of every node reachable from `roots`,
 * deduplicated by identity (shared structure counted once). */
function reachableWeight(roots: JsonValue[]): number {
  const seen = new Set<object>();
  let weight = 0;
  const walk = (value: JsonValue): void => {
    if (typeof value !== 'object' || value === null) return;
    if (seen.has(value)) return;
    seen.add(value);
    weight += 1;
    for (const child of Object.values(value)) {
      if (typeof child === 'object' && child !== null) walk(child as JsonValue);
      else weight += 1;
    }
  };
  for (const root of roots) walk(root);
  return weight;
}

function dig(state: JsonValue, ...path: Array<string | number>): any {
  let node: any = state;
  for (const key of path) node = node[key];
  return node;
}

test('interdependent slices commit atomically; subscribers never see a mixed state', () => {
  const initial = {
    cart: { seq: 0, items: [] as JsonValue[] },
    inventory: { seq: 0, stock: { apple: 3 } },
  };
  const store = new TransactionalStore(initial);
  const reference = new ReferenceModel(initial);

  const observed: Array<{ version: number; cartSeq: number; invSeq: number }> = [];
  store.subscribe((state, version) => {
    observed.push({
      version,
      cartSeq: dig(state, 'cart', 'seq'),
      invSeq: dig(state, 'inventory', 'seq'),
    });
  });

  // Each transaction moves one apple cart<-inventory and bumps BOTH slice
  // sequence numbers; a subscriber must never observe cart@n with inventory@m.
  const log: LogEntry[] = [];
  for (let i = 1; i <= 3; i += 1) {
    log.push(
      { op: 'begin', tx: `buy${i}` },
      { op: 'set', tx: `buy${i}`, path: ['cart', 'seq'], value: i },
      { op: 'set', tx: `buy${i}`, path: ['inventory', 'seq'], value: i },
      { op: 'set', tx: `buy${i}`, path: ['cart', 'items', i - 1], value: `apple#${i}` },
      { op: 'set', tx: `buy${i}`, path: ['inventory', 'stock', 'apple'], value: 3 - i },
      { op: 'commit', tx: `buy${i}` },
    );
  }
  const report = replay(store, reference, log);

  assert.deepEqual(report.inconsistencies, []);
  assert.deepEqual(report.notifications, [1, 2, 3]);
  assert.equal(observed.length, 3);
  for (const entry of observed) {
    assert.equal(entry.cartSeq, entry.invSeq, `mixed slices at version ${entry.version}`);
    assert.equal(entry.cartSeq, entry.version);
  }
  assert.equal(dig(store.state, 'inventory', 'stock', 'apple'), 0);
  assert.deepEqual(store.state, reference.history.get(3));
});

test('commits issued inside a notification are queued until the round finishes', () => {
  const store = new TransactionalStore({ n: 0 });
  const events: string[] = [];
  let reentries = 0;

  store.subscribe((state, version) => {
    events.push(`A@${version}:n=${dig(state, 'n')}`);
    if (reentries < 2) {
      reentries += 1;
      store.begin().set(['n'], version + 1).commit(); // applied now, notified later
    }
  });
  store.subscribe((state, version) => {
    events.push(`B@${version}:n=${dig(state, 'n')}`);
  });

  store.begin().set(['n'], 1).commit();

  // FIFO: every listener finishes version N before anyone hears about N+1.
  assert.deepEqual(events, [
    'A@1:n=1', 'B@1:n=1',
    'A@2:n=2', 'B@2:n=2',
    'A@3:n=3', 'B@3:n=3',
  ]);
  assert.equal(store.version, 3);
  assert.equal(dig(store.state, 'n'), 3);
  // At most one notification per listener per version.
  const perVersion = new Map<number, number>();
  for (const event of events) {
    const v = Number(event.split('@')[1].split(':')[0]);
    perVersion.set(v, (perVersion.get(v) ?? 0) + 1);
  }
  for (const [v, count] of perVersion) {
    assert.equal(count, 2, `version ${v} delivered ${count} times`);
  }
});

test('unsubscribing inside a callback takes effect immediately', () => {
  const store = new TransactionalStore({ n: 0 });
  let aCount = 0;
  let bCount = 0;
  let cCount = 0;
  const unsubA = store.subscribe(() => {
    aCount += 1;
    unsubA(); // self-unsubscribe: no further versions
  });
  const unsubB = store.subscribe(() => {
    bCount += 1;
  });
  store.subscribe(() => {
    cCount += 1;
    if (cCount === 1) unsubB(); // remove another listener mid-round
  });
  for (let i = 1; i <= 4; i += 1) store.begin().set(['n'], i).commit();
  assert.equal(aCount, 1);
  assert.equal(bCount, 1); // saw v1 (ran before C), removed before v2
  assert.equal(cCount, 4);

  // A listener removed earlier in the same round is skipped entirely.
  const s2 = new TransactionalStore({ n: 0 });
  let x = 0;
  let y = 0;
  let unsubY: () => void = () => {};
  s2.subscribe(() => {
    x += 1;
    unsubY();
  });
  unsubY = s2.subscribe(() => {
    y += 1;
  });
  s2.begin().set(['n'], 1).commit();
  s2.begin().set(['n'], 2).commit();
  assert.equal(x, 2);
  assert.equal(y, 0);
});

test('a throwing listener never blocks the others', () => {
  const forwarded: unknown[] = [];
  const store = new TransactionalStore(
    { n: 0 },
    { onListenerError: (error) => forwarded.push(error) },
  );
  let good = 0;
  store.subscribe(() => {
    throw new Error('boom');
  });
  store.subscribe(() => {
    good += 1;
  });
  for (let i = 1; i <= 3; i += 1) store.begin().set(['n'], i).commit();
  assert.equal(good, 3);
  assert.equal(store.listenerErrors.length, 3);
  assert.equal(forwarded.length, 3);
  assert.deepEqual(
    store.listenerErrors.map((entry) => entry.version),
    [1, 2, 3],
  );
  assert.match(String((store.listenerErrors[0] as { error: unknown }).error), /boom/);
});

test('checkout an old version, fork a branch; existing history is never overwritten', () => {
  const initial = { doc: { text: 'a' } };
  const store = new TransactionalStore(initial);
  const reference = new ReferenceModel(initial);
  const log: LogEntry[] = [
    { op: 'begin', tx: 't1' },
    { op: 'set', tx: 't1', path: ['doc', 'text'], value: 'ab' },
    { op: 'commit', tx: 't1' },
    { op: 'begin', tx: 't2' },
    { op: 'set', tx: 't2', path: ['doc', 'text'], value: 'abc' },
    { op: 'commit', tx: 't2' },
    // rewind to v1 and fork
    { op: 'begin', tx: 't3', base: 1 },
    { op: 'set', tx: 't3', path: ['doc', 'text'], value: 'aXY' },
    { op: 'commit', tx: 't3' },
    // continue on the new branch (base defaults to head)
    { op: 'begin', tx: 't4' },
    { op: 'set', tx: 't4', path: ['doc', 'text'], value: 'aXY!' },
    { op: 'commit', tx: 't4' },
  ];
  const report = replay(store, reference, log);
  assert.deepEqual(report.inconsistencies, []);

  // History is immutable: v1 and v2 keep their original content.
  assert.equal(dig(store.checkout(1).state, 'doc', 'text'), 'ab');
  assert.equal(dig(store.checkout(2).state, 'doc', 'text'), 'abc');
  // Branch metadata forms a DAG; versions stay global and monotonic.
  assert.equal(store.checkout(2).branch, 0);
  assert.equal(store.checkout(3).branch, 1);
  assert.equal(store.checkout(3).parent, 1);
  assert.equal(store.checkout(4).branch, 1);
  assert.equal(store.checkout(4).parent, 3);
  assert.equal(store.version, 4);
  assert.equal(dig(store.state, 'doc', 'text'), 'aXY!');
  for (let v = 0; v <= 4; v += 1) {
    assert.deepEqual(store.checkout(v).state, reference.history.get(v));
  }
});

test('snapshots pin nodes against reclamation; the ledger stays exact', () => {
  // weight(v_i) = root(1 self + 1 scalar tag) + blob(1 + (i+1) scalars) = i + 4
  const initial = { blob: [0], tag: 'v0' };
  const store = new TransactionalStore(initial, { capacity: 20 });
  const reference = new ReferenceModel(initial);

  let snap: ReturnType<TransactionalStore['snapshot']> | undefined;
  for (let i = 1; i <= 8; i += 1) {
    const tx = store.begin();
    tx.set(['blob'], Array.from({ length: i + 1 }, (_, k) => k));
    tx.set(['tag'], `v${i}`);
    const version = tx.commit();
    reference.commit(i - 1, [
      { kind: 'set', path: ['blob'], value: Array.from({ length: i + 1 }, (_, k) => k) },
      { kind: 'set', path: ['tag'], value: `v${i}` },
    ]);
    assert.equal(version, i);
    if (i === 2) snap = store.snapshot(2);
    const ledger = store.ledger();
    assert.equal(ledger.retained, ledger.allocatedTotal - ledger.freedTotal);
    assert.equal(ledger.retained, reachableWeight(store.retainedRoots()));
    assert.ok(ledger.retained <= 20, `retained ${ledger.retained} exceeds capacity`);
  }

  // Head (v8, weight 12) + pinned snapshot (v2, weight 6) survive; the rest
  // of history (v0,v1,v3..v7) was reclaimed oldest-first.
  const ledger = store.ledger();
  assert.equal(ledger.retained, 18);
  assert.equal(ledger.liveCommits, 2);
  assert.equal(ledger.evictedCommits, 7);
  assert.deepEqual(ledger.pinnedVersions, [2]);

  // The pinned version is still fully readable and matches the reference.
  assert.deepEqual(store.checkout(2).state, reference.history.get(2));
  assert.deepEqual(snap!.state, reference.history.get(2));
  // Reclaimed versions are gone; unknown versions are rejected too.
  for (const v of [0, 1, 3, 4, 5, 6, 7]) {
    assert.throws(() => store.checkout(v), RangeError);
  }
  assert.throws(() => store.checkout(999), RangeError);
  assert.throws(() => store.snapshot(5), RangeError);

  // Releasing the snapshot unpins v2. Without capacity pressure nothing is
  // reclaimed eagerly: history is kept while it fits.
  snap!.release();
  assert.equal(store.ledger().retained, 18);
  assert.deepEqual(store.checkout(2).state, reference.history.get(2));
  assert.throws(() => snap!.state, Error);
  snap!.release(); // idempotent
  assert.equal(store.ledger().retained, 18);

  // The next commit creates pressure (18 + 13 > 20); gc now reclaims the
  // unpinned v2 (and v8, oldest-first), leaving only head v9 (weight 13).
  const tx9 = store.begin();
  tx9.set(['blob'], Array.from({ length: 10 }, (_, k) => k));
  tx9.set(['tag'], 'v9');
  tx9.commit();
  reference.commit(8, [
    { kind: 'set', path: ['blob'], value: Array.from({ length: 10 }, (_, k) => k) },
    { kind: 'set', path: ['tag'], value: 'v9' },
  ]);
  const after = store.ledger();
  assert.equal(after.retained, 13);
  assert.equal(after.liveCommits, 1);
  assert.equal(after.evictedCommits, 9);
  assert.equal(after.retained, reachableWeight(store.retainedRoots()));
  assert.throws(() => store.checkout(2), RangeError);
  assert.throws(() => store.checkout(8), RangeError);
  assert.deepEqual(store.state, reference.history.get(9));
});

test('values are validated as JSON and stored as frozen, decoupled copies', () => {
  const store = new TransactionalStore({ a: { b: 1 }, list: [1, 2] });
  const tx = store.begin();
  const cyclic: JsonValue[] = [];
  cyclic.push(cyclic);
  const badValues: unknown[] = [
    undefined, NaN, Infinity, -Infinity, Symbol('s'), 10n,
    () => 1, new Date(), new Map(), cyclic,
  ];
  for (const bad of badValues) {
    assert.throws(() => tx.set(['x'], bad as JsonValue), TypeError);
  }
  // Failed sets did not poison the transaction.
  tx.set(['a', 'b'], 2);
  assert.equal(tx.commit(), 1);
  assert.equal(dig(store.state, 'a', 'b'), 2);

  // The stored tree is deeply frozen.
  assert.ok(Object.isFrozen(store.state));
  assert.ok(Object.isFrozen(store.state.a));
  assert.ok(Object.isFrozen(store.state.list));
  assert.throws(() => {
    (store.state as JsonObject).a = 5 as unknown as JsonValue;
  }, TypeError);

  // Mutating the caller's object after set() cannot leak into the store.
  const input: JsonObject = { x: [1] };
  const tx2 = store.begin();
  tx2.set(['k'], input);
  (input.x as JsonValue[]).push(99);
  tx2.commit();
  assert.deepEqual(dig(store.state, 'k'), { x: [1] });

  // delete(): object keys are removed, array indices are spliced.
  const tx3 = store.begin();
  tx3.delete(['list', 0]);
  tx3.delete(['a', 'b']);
  tx3.commit();
  assert.deepEqual(store.state.list, [2]);
  assert.deepEqual(store.state.a, {});
  assert.throws(() => store.begin().delete(['missing']).commit(), RangeError);
  assert.throws(() => store.begin().set(['list', 0, 'x'], 1).commit(), TypeError);

  // A transaction is single-use; an empty commit still produces a version.
  const single = store.begin();
  single.commit();
  assert.throws(() => single.commit(), Error);
  const before = store.version;
  store.begin().commit();
  assert.equal(store.version, before + 1);
});

test('deterministic op-log replay against the deep-copy reference (seeded fuzz)', () => {
  mkdirSync('logs', { recursive: true });
  for (const seed of [1, 7, 42, 1337]) {
    const initial: JsonObject = { cart: { items: [] }, inventory: { stock: {} } };
    const log = generateLog({ steps: 120, seed, initial });
    const store = new TransactionalStore(initial, { capacity: 150 });
    const reference = new ReferenceModel(initial);
    const report = replay(store, reference, log);

    assert.deepEqual(report.inconsistencies, [], `seed ${seed}: ${report.inconsistencies[0]}`);
    assert.deepEqual(
      report.notifications,
      report.commits,
      `seed ${seed}: every version notified exactly once, in order`,
    );
    const ledger = store.ledger();
    assert.equal(ledger.retained, ledger.allocatedTotal - ledger.freedTotal);
    assert.equal(ledger.retained, reachableWeight(store.retainedRoots()));
    assert.ok(ledger.evictedCommits > 0, `seed ${seed}: capacity should force eviction`);
    assert.ok(ledger.retained <= 150, `seed ${seed}: retained ${ledger.retained} over capacity`);

    const summary = {
      seed,
      ops: log.length,
      commits: report.commits.length,
      notifications: report.notifications.length,
      crossChecks: report.checks,
      ledger,
    };
    writeFileSync(
      `logs/fuzz-seed${seed}.json`,
      JSON.stringify({ summary, log }, null, 2),
    );
    console.log(
      `seed ${seed}: ${log.length} ops, ${report.commits.length} commits, ` +
        `${report.checks} cross-checks, retained=${ledger.retained}/${ledger.capacity}, ` +
        `evicted=${ledger.evictedCommits}, live=${ledger.liveCommits}`,
    );
  }
});
