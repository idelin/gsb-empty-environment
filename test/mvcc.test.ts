import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  MVCCStore,
  TransactionAbortedError,
  runTransaction,
} from '../src/mvcc.ts';

describe('basic commit / read', () => {
  test('a commit becomes visible to later transactions', async () => {
    const store = new MVCCStore<string>();
    const w = store.begin();
    w.set('a', '1');
    await w.commit();
    assert.equal(store.currentTs, 1);

    const r = store.beginReadOnly();
    assert.equal(r.get('a'), '1');
    await r.commit();
  });

  test('atomic multi-key commit', async () => {
    const store = new MVCCStore<number>();
    const w = store.begin();
    w.set('x', 1);
    w.set('y', 2);
    w.set('z', 3);
    await w.commit();

    const r = store.beginReadOnly();
    assert.deepEqual([r.get('x'), r.get('y'), r.get('z')], [1, 2, 3]);
    await r.commit();
  });

  test('delete creates a tombstone', async () => {
    const store = new MVCCStore<string>();
    await runTransaction(store, (t) => t.set('k', 'v'));
    await runTransaction(store, (t) => {
      assert.equal(t.has('k'), true);
      t.delete('k');
    });
    const r = store.beginReadOnly();
    assert.equal(r.get('k'), undefined);
    assert.equal(r.has('k'), false);
    await r.commit();
  });
});

describe('snapshot isolation', () => {
  test('old reader never sees newer committed versions', async () => {
    const store = new MVCCStore<number>();
    await runTransaction(store, (t) => t.set('k', 1));

    const oldReader = store.beginReadOnly(); // snapshot ts=1
    assert.equal(oldReader.get('k'), 1);

    await runTransaction(store, (t) => {
      t.set('k', (t.get('k') ?? 0) + 1);
    });
    await runTransaction(store, (t) => {
      t.set('k', (t.get('k') ?? 0) + 1);
    });
    assert.equal(store.currentTs, 3);

    // Snapshot is fixed: the long-lived reader is unaffected.
    assert.equal(oldReader.get('k'), 1);
    await oldReader.commit();

    const fresh = store.beginReadOnly();
    assert.equal(fresh.get('k'), 3);
    await fresh.commit();
  });

  test('reader cannot see intermediate uncommitted state', async () => {
    const store = new MVCCStore<number>();
    await runTransaction(store, (t) => t.set('k', 0));

    const writer = store.begin();
    writer.set('k', 999); // buffered, not committed
    assert.equal(writer.get('k'), 999); // visible to itself

    const reader = store.beginReadOnly();
    assert.equal(reader.get('k'), 0); // no dirty read
    await reader.commit();

    await writer.rollback();
    const after = store.beginReadOnly();
    assert.equal(after.get('k'), 0); // rollback leaves no trace
    await after.commit();
  });

  test('read-only snapshot sees a coherent multi-key point in time', async () => {
    const store = new MVCCStore<number>();
    await runTransaction(store, (t) => {
      t.set('a', 1);
      t.set('b', 1);
    });
    const snap = store.beginReadOnly();
    await runTransaction(store, (t) => {
      t.set('a', 2);
      t.set('b', 2);
    });
    assert.deepEqual([snap.get('a'), snap.get('b')], [1, 1]);
    await snap.commit();
  });
});

describe('optimistic conflict detection', () => {
  test('two txns reading the same value cannot silently lose an update', async () => {
    const store = new MVCCStore<number>();
    await runTransaction(store, (t) => t.set('balance', 100));

    const t1 = store.begin();
    const t2 = store.begin();
    t1.set('balance', (t1.get('balance') ?? 0) - 30); // 70
    t2.set('balance', (t2.get('balance') ?? 0) - 50); // 50
    await t1.commit();

    await assert.rejects(
      () => t2.commit(),
      (err: unknown) =>
        err instanceof TransactionAbortedError &&
        /read-write conflict/.test(err.reason),
    );
    assert.equal(t2.state, 'aborted');

    const r = store.beginReadOnly();
    assert.equal(r.get('balance'), 70); // not 50, not 20, not overwritten
    await r.commit();
  });

  test('different keys do not conflict and both commit', async () => {
    const store = new MVCCStore<number>();
    const t1 = store.begin();
    const t2 = store.begin();
    t1.set('a', 1);
    t2.set('b', 2);
    await Promise.all([t1.commit(), t2.commit()]);
    assert.equal(store.currentTs, 2);

    const r = store.beginReadOnly();
    assert.equal(r.get('a'), 1);
    assert.equal(r.get('b'), 2);
    await r.commit();
  });

  test('blind writes on a key changed since snapshot also conflict', async () => {
    const store = new MVCCStore<number>();
    await runTransaction(store, (t) => t.set('k', 1));
    const t1 = store.begin();
    const t2 = store.begin();
    t1.set('k', 10); // blind write
    t2.set('k', 20); // blind write
    await t1.commit();
    await assert.rejects(() => t2.commit(), /write-write conflict/);
  });

  test('committing after rollback is rejected; buffered writes are gone', async () => {
    const store = new MVCCStore<string>();
    const t = store.begin();
    t.set('k', 'v');
    t.rollback();
    await assert.rejects(() => t.commit(), TransactionAbortedError);
    assert.equal(store.versionCount(), 0);
  });

  test('read-only transaction rejects writes', async () => {
    const store = new MVCCStore<string>();
    const t = store.beginReadOnly();
    assert.throws(() => t.set('k', 'v'), /read-only/);
    t.rollback();
  });
});

describe('retry helper', () => {
  test('runTransaction retries conflicts and both increments survive', async () => {
    const store = new MVCCStore<number>();
    await runTransaction(store, (t) => t.set('n', 0));

    const attempts: number[] = [];
    const inc = () =>
      runTransaction(
        store,
        (t) => {
          attempts.push(t.id);
          t.set('n', (t.get('n') ?? 0) + 1);
        },
        { retries: 5 },
      );

    await Promise.all([inc(), inc(), inc(), inc(), inc()]);

    const r = store.beginReadOnly();
    assert.equal(r.get('n'), 5);
    await r.commit();
    assert.ok(attempts.length > 5, 'at least one txn retried after a conflict');
  });
});

describe('garbage collection', () => {
  test('retains only the version visible to the oldest active reader', async () => {
    const store = new MVCCStore<number>();
    await runTransaction(store, (t) => t.set('k', 1));
    await runTransaction(store, (t) => t.set('k', 2));

    const oldReader = store.beginReadOnly(); // readTs=2
    await runTransaction(store, (t) => t.set('k', 3)); // ts=3
    await runTransaction(store, (t) => t.set('k', 4)); // ts=4
    assert.equal(store.versionCount('k'), 4);

    const stats = store.garbageCollect();
    assert.equal(stats.floorReadTs, 2);
    assert.equal(stats.versionsRemoved, 1); // v1 dropped; v2 visible to reader
    assert.equal(oldReader.get('k'), 2); // snapshot still readable

    await oldReader.commit();
    const stats2 = store.garbageCollect();
    assert.equal(stats2.floorReadTs, 4);
    assert.equal(stats2.versionsRemoved, 2); // v2 and v3 dropped now
    assert.equal(store.versionCount('k'), 1); // only v4 remains
    const r = store.beginReadOnly();
    assert.equal(r.get('k'), 4);
    await r.commit();
  });

  test('GC with no active readers keeps only the latest version', async () => {
    const store = new MVCCStore<number>();
    for (let i = 1; i <= 5; i++) {
      await runTransaction(store, (t) => t.set('k', i));
    }
    assert.equal(store.versionCount('k'), 5);
    const stats = store.garbageCollect();
    assert.equal(stats.versionsRemoved, 4);
    assert.equal(store.versionCount('k'), 1);
  });

  test('GC removes keys whose latest retained version is a tombstone', async () => {
    const store = new MVCCStore<string>();
    await runTransaction(store, (t) => t.set('gone', 'v'));
    await runTransaction(store, (t) => t.delete('gone'));
    store.garbageCollect();
    assert.equal(store.versionCount('gone'), 0);
    assert.equal(store.logLength, 2); // log (replay source) is untouched
  });
});

describe('operation log replay', () => {
  test('replaying the log reconstructs identical versioned state', async () => {
    const store = new MVCCStore<number>();
    await runTransaction(store, (t) => {
      t.set('a', 1);
      t.set('b', 10);
    });
    await runTransaction(store, (t) => {
      t.set('a', 2);
      t.delete('b');
    });
    await runTransaction(store, (t) => t.set('c', 30));

    const copy = MVCCStore.replay<number>(store.getLog());
    assert.equal(copy.currentTs, store.currentTs);
    assert.equal(copy.versionCount(), store.versionCount());

    const a = copy.beginReadOnly();
    assert.equal(a.get('a'), 2);
    assert.equal(a.has('b'), false);
    assert.equal(a.get('c'), 30);
    await a.commit();
  });

  test('replay is order independent (records sorted by commitTs)', () => {
    const log = [
      { commitTs: 3, txnId: 3, writes: [{ key: 'k', value: 3, deleted: false }] },
      { commitTs: 1, txnId: 1, writes: [{ key: 'k', value: 1, deleted: false }] },
      {
        commitTs: 2,
        txnId: 2,
        writes: [{ key: 'k', value: undefined, deleted: true }],
      },
    ];
    const copy = MVCCStore.replay<number>(log);
    assert.equal(copy.currentTs, 3);
    // Historical versions are all preserved after replay.
    assert.deepEqual(
      copy.versionCount('k'),
      3,
    );
  });
});
