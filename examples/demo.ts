/**
 * Runnable walkthrough of the MVCC/OCC store.
 * Run: node examples/demo.ts
 */
import {
  MVCCStore,
  TransactionAbortedError,
  runTransaction,
} from '../src/mvcc.ts';

function heading(title: string): void {
  console.log(`\n=== ${title} ===`);
}

async function main(): Promise<void> {
  const store = new MVCCStore<number>();

  heading('1. Basic read/write transaction');
  await runTransaction(store, (t) => {
    t.set('balance', 100);
    t.set('inventory', 5);
  });
  console.log('committed balance=100 inventory=5, commit ts =', store.currentTs);

  heading('2. Lost update: two txns read 100, both write');
  const alice = store.begin();
  const bob = store.begin();
  alice.set('balance', (alice.get('balance') ?? 0) - 30);
  bob.set('balance', (bob.get('balance') ?? 0) - 50);
  await alice.commit();
  console.log('Alice (-30) committed; balance = 70');
  try {
    await bob.commit();
  } catch (err) {
    if (err instanceof TransactionAbortedError) {
      console.log('Bob (-50) rejected ->', err.message);
    }
  }
  console.log('Final balance is 70 (no silent lost update)');

  heading('3. Retry helper: 5 concurrent increments all survive');
  await runTransaction(store, (t) => t.set('counter', 0));
  const inc = () =>
    runTransaction(
      store,
      (t) => t.set('counter', (t.get('counter') ?? 0) + 1),
      { retries: 10 },
    );
  await Promise.all([inc(), inc(), inc(), inc(), inc()]);
  {
    const r = store.beginReadOnly();
    console.log('counter after 5 concurrent increments =', r.get('counter'));
    await r.commit();
  }

  heading('4. Different keys never conflict');
  await Promise.all([
    runTransaction(store, (t) => t.set('a', 1)),
    runTransaction(store, (t) => t.set('b', 2)),
  ]);
  console.log('both committed, commit ts =', store.currentTs);

  heading('5. Snapshot isolation: an old reader ignores newer commits');
  await runTransaction(store, (t) => t.set('k', 1));
  const snapshot = store.beginReadOnly();
  console.log(`reader opened at ts=${snapshot.readTs}, sees k =`, snapshot.get('k'));
  await runTransaction(store, (t) => t.set('k', 2));
  await runTransaction(store, (t) => t.set('k', 3));
  console.log('after two more commits, old reader still sees k =',
    snapshot.get('k'));
  {
    const r = store.beginReadOnly();
    console.log('a fresh reader sees k =', r.get('k'));
    await r.commit();
  }
  await snapshot.commit();

  heading('6. Uncommitted writes are invisible to other readers');
  const writer = store.begin();
  writer.set('k', 999);
  console.log('writer sees its own buffered k =', writer.get('k'));
  {
    const r = store.beginReadOnly();
    console.log('other reader sees committed k =', r.get('k'));
    await r.commit();
  }
  await writer.rollback();
  {
    const r = store.beginReadOnly();
    console.log('after rollback k =', r.get('k'));
    await r.commit();
  }

  heading('7. Garbage collection is bounded by active readers');
  const g = new MVCCStore<number>();
  for (let i = 1; i <= 4; i++) {
    await runTransaction(g, (t) => t.set('v', i));
  }
  console.log('versions of v before GC:', g.versionCount('v'));
  let stats = g.garbageCollect();
  console.log('GC with no readers removed', stats.versionsRemoved,
    'versions; remaining:', g.versionCount('v'));

  await runTransaction(g, (t) => t.set('v', 5));
  await runTransaction(g, (t) => t.set('v', 6));
  const oldReader = g.beginReadOnly();
  await runTransaction(g, (t) => t.set('v', 7));
  stats = g.garbageCollect();
  const freshReader = g.beginReadOnly();
  console.log('GC with reader at ts=' + oldReader.readTs + ' removed',
    stats.versionsRemoved, 'version(s); old reader still sees v =',
    oldReader.get('v'), '; latest v =', freshReader.get('v'));
  await freshReader.commit();
  await oldReader.commit();

  heading('8. Replaying the operation log rebuilds the store');
  const rebuilt = MVCCStore.replay<number>(store.getLog());
  console.log('rebuilt ts', rebuilt.currentTs, '== original ts', store.currentTs);
  console.log('rebuilt versions', rebuilt.versionCount(),
    '== original versions', store.versionCount());
  const rr = rebuilt.beginReadOnly();
  console.log('rebuilt balance =', rr.get('balance'),
    ', counter =', rr.get('counter'), ', k =', rr.get('k'));
  await rr.commit();
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
