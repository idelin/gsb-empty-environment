import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store, deepEqual } from '../src/index.ts';
import type { NotifyMeta } from '../src/index.ts';

interface AppState {
  count: number;
  notes: string[];
  flag: boolean;
}

function makeStore(options: { roundBudget?: number } = {}) {
  return new Store<AppState>({ count: 0, notes: [], flag: false }, options);
}

test('commit notifies subscribers in deterministic subscription-id order', () => {
  const store = makeStore();
  const calls: string[] = [];
  store.subscribe((s) => s.count, () => calls.push('first'));
  store.subscribe((s) => s.count, () => calls.push('second'));
  store.subscribe((s) => s.count, () => calls.push('third'));
  store.transact('bump', (tx) => tx.set(['count'], 1));
  assert.deepEqual(calls, ['first', 'second', 'third']);
});

test('readers never see intermediate transaction state, only committed state', () => {
  const store = makeStore();
  const seenInside: number[] = [];
  store.transact('two-step', (tx) => {
    tx.set(['count'], 1);
    seenInside.push(store.getState().count); // external reader still sees committed
    tx.set(['count'], 2);
    seenInside.push(store.getState().count);
    assert.equal(tx.get(['count']), 2); // draft visible only inside tx
  });
  assert.deepEqual(seenInside, [0, 0]);
  assert.equal(store.getState().count, 2);
});

test('failed transaction commits nothing and notifies nobody', () => {
  const store = makeStore();
  let notified = 0;
  store.subscribe((s) => s.count, () => notified++);
  assert.throws(() =>
    store.transact('boom', (tx) => {
      tx.set(['count'], 99);
      throw new Error('abort');
    }),
  );
  assert.equal(store.getVersion(), 0);
  assert.equal(store.getState().count, 0);
  assert.equal(notified, 0);
});

test('empty transaction does not bump version or notify', () => {
  const store = makeStore();
  let notified = 0;
  store.subscribe((s) => s.count, () => notified++);
  const result = store.transact('noop', () => {});
  assert.equal(result.committed, false);
  assert.equal(store.getVersion(), 0);
  assert.equal(notified, 0);
});

test('reentrant writes inside notifications are processed in the next round', () => {
  const store = makeStore();
  const events: string[] = [];
  store.subscribe(
    (s) => s.count,
    (value, meta) => {
      events.push(`count=${value} round=${meta.round} v${meta.fromVersion}->v${meta.toVersion}`);
      if (value === 1) {
        store.transact('reentrant', (tx) => tx.set(['flag'], true));
      }
    },
  );
  store.subscribe(
    (s) => s.flag,
    (value, meta) => events.push(`flag=${value} round=${meta.round} v${meta.fromVersion}->v${meta.toVersion}`),
  );
  store.transact('bump', (tx) => tx.set(['count'], 1));
  assert.deepEqual(events, [
    'count=1 round=1 v0->v1',
    'flag=true round=2 v1->v2',
  ]);
  assert.equal(store.getVersion(), 2);
});

test('unsubscribing another subscriber does not affect the current snapshot iteration', () => {
  const store = makeStore();
  const calls: string[] = [];
  const second = store.subscribe((s) => s.count, () => calls.push('second'));
  store.subscribe((s) => s.count, () => {
    calls.push('first-unsub-second');
    second.unsubscribe();
  });
  // subscription order: second(id1) is notified before the unsubscribing one(id2)
  store.transact('bump', (tx) => tx.set(['count'], 1));
  assert.deepEqual(calls, ['second', 'first-unsub-second']);
  store.transact('bump2', (tx) => tx.set(['count'], 2));
  assert.deepEqual(calls, ['second', 'first-unsub-second', 'first-unsub-second']);
});

test('self-unsubscribe only affects later rounds', () => {
  const store = makeStore();
  const calls: number[] = [];
  const handle = store.subscribe(
    (s) => s.count,
    (value) => {
      calls.push(value);
      handle.unsubscribe();
    },
  );
  store.transact('bump', (tx) => tx.set(['count'], 1));
  store.transact('bump2', (tx) => tx.set(['count'], 2));
  assert.deepEqual(calls, [1]); // current round delivered, later rounds skipped
});

test('new subscriptions do not affect the current snapshot iteration', () => {
  const store = makeStore();
  const calls: string[] = [];
  store.subscribe(
    (s) => s.count,
    () => {
      calls.push('first');
      store.subscribe((s) => s.count, () => calls.push('late'));
    },
  );
  store.transact('bump', (tx) => tx.set(['count'], 1));
  assert.deepEqual(calls, ['first']); // late subscriber not in current snapshot
  store.transact('bump2', (tx) => tx.set(['count'], 2));
  assert.deepEqual(calls, ['first', 'first', 'late']); // joined from next round on
});

test('subscriber errors are aggregated and do not block other notifications', () => {
  const store = makeStore();
  const calls: string[] = [];
  store.subscribe((s) => s.count, () => calls.push('before'));
  store.subscribe((s) => s.count, () => {
    throw new Error('listener exploded');
  });
  store.subscribe((s) => s.count, () => calls.push('after'));
  store.transact('bump', (tx) => tx.set(['count'], 1));
  assert.deepEqual(calls, ['before', 'after']);
  const errors = store.getSubscriberErrors();
  assert.equal(errors.length, 1);
  assert.equal(errors[0].kind, 'listener');
  assert.equal(errors[0].subscriberId, 2);
  assert.equal(errors[0].round, 1);
  assert.equal(errors[0].version, 1);
  assert.match(String(errors[0].error), /listener exploded/);
});

test('selector and equality strategy can be swapped dynamically', () => {
  const store = makeStore();
  const seen: unknown[] = [];
  const handle = store.subscribe(
    (s) => s.count,
    (v) => seen.push(v),
  );
  store.transact('bump', (tx) => tx.set(['count'], 1));
  assert.deepEqual(seen, [1]);

  // swap equality to parity-based: 1 -> 3 keeps parity, must not notify
  handle.setEquality((a, b) => (a as number) % 2 === (b as number) % 2);
  store.transact('odd-jump', (tx) => tx.set(['count'], 3));
  assert.deepEqual(seen, [1]);
  store.transact('even-jump', (tx) => tx.set(['count'], 4));
  assert.deepEqual(seen, [1, 4]);

  // swap dependency from count to flag, with strict equality
  handle.setSelector((s: unknown) => (s as AppState).flag, (a, b) => a === b);
  store.transact('flip', (tx) => tx.set(['flag'], true));
  assert.deepEqual(seen, [1, 4, true]);
});

test('backpressure merge produces explicit version-range evidence', () => {
  const store = makeStore();
  const metas: NotifyMeta[] = [];
  store.subscribe(
    (s) => s.count,
    (value, meta) => {
      metas.push(meta);
      if (value === 1) {
        // two reentrant commits queue up before the next round runs
        store.transact('r1', (tx) => tx.set(['count'], 2));
        store.transact('r2', (tx) => tx.set(['count'], 3));
      }
    },
  );
  store.transact('seed', (tx) => tx.set(['count'], 1));
  assert.equal(store.getVersion(), 3);
  assert.equal(store.getState().count, 3); // final state not lost

  const evidence = store.getMergeEvidence();
  assert.equal(evidence.length, 1);
  assert.deepEqual(evidence[0], { round: 2, fromVersion: 1, toVersion: 3, mergedCommits: [2, 3] });

  // round 2 notified once for the merged range, not once per commit
  assert.equal(metas.length, 2);
  assert.deepEqual(metas[1].mergedCommits, [2, 3]);
  assert.equal(metas[1].fromVersion, 1);
  assert.equal(metas[1].toVersion, 3);
});

test('recursive updates are stopped by the cycle budget', () => {
  const store = makeStore({ roundBudget: 8 });
  store.subscribe(
    (s) => s.count,
    (value) => {
      store.transact('again', (tx) => tx.set(['count'], value + 1));
    },
  );
  store.transact('seed', (tx) => tx.set(['count'], 1));
  assert.equal(store.getRound(), 8);
  const errors = store.getSubscriberErrors();
  assert.equal(errors.length, 1);
  assert.equal(errors[0].kind, 'cycle-budget');
  assert.match(String(errors[0].error), /cycle budget exceeded/);
});

test('replay reproduces state with unique versions and no lost final state', () => {
  const store = makeStore();
  store.subscribe(
    (s) => s.count,
    (value) => {
      if (value === 1) {
        store.transact('r1', (tx) => tx.set(['count'], 2));
        store.transact('r2', (tx) => tx.set(['count'], 3));
      }
    },
  );
  store.transact('seed', (tx) => tx.set(['count'], 1));
  store.transact('note', (tx) => tx.set(['notes'], ['a', 'b']));
  store.transact('flip', (tx) => tx.set(['flag'], true));

  const { state, versions } = store.replay();
  assert.deepEqual(state, store.getState()); // final state preserved
  assert.equal(new Set(versions).size, versions.length); // no duplicate versions
  for (let i = 1; i < versions.length; i++) {
    assert.ok(versions[i] > versions[i - 1], 'versions strictly increase');
  }
  assert.equal(versions[versions.length - 1], store.getVersion());
});

test('unsubscribed subscriber is never revived by queued rounds', () => {
  const store = makeStore();
  const calls: number[] = [];
  const doomed = store.subscribe((s) => s.count, (v) => calls.push(v));
  store.subscribe(
    (s) => s.count,
    (value) => {
      if (value === 1) {
        // queue several commits; doomed is already gone and must not revive
        store.transact('q1', (tx) => tx.set(['count'], 2));
        store.transact('q2', (tx) => tx.set(['count'], 3));
      }
    },
  );
  doomed.unsubscribe(); // cancelled before any commit lands
  store.transact('seed', (tx) => tx.set(['count'], 1));
  store.transact('later', (tx) => tx.set(['count'], 4));
  assert.deepEqual(calls, []); // never notified after unsubscribe, despite queued rounds
  assert.equal(store.getState().count, 4);
});

test('notifications only reference committed versions', () => {
  const store = makeStore();
  const metas: NotifyMeta[] = [];
  store.subscribe(
    (s) => s.count,
    (value, meta) => {
      metas.push(meta);
      if (value < 3) store.transact('chain', (tx) => tx.set(['count'], value + 1));
    },
  );
  store.transact('seed', (tx) => tx.set(['count'], 1));
  assert.ok(metas.length >= 3);
  for (const meta of metas) {
    assert.ok(meta.toVersion <= store.getVersion());
    assert.ok(meta.fromVersion < meta.toVersion);
    const committedVersions = store.getCommitLog().map((c) => c.version);
    for (const v of meta.mergedCommits) {
      assert.ok(committedVersions.includes(v), `v${v} is a committed version`);
    }
  }
});

test('object selections work with deepEqual and are not double-notified', () => {
  const store = makeStore();
  const seen: string[][] = [];
  store.subscribe((s) => s.notes, (v) => seen.push([...v]), { equality: deepEqual });
  store.transact('same-content', (tx) => tx.set(['notes'], []));
  assert.deepEqual(seen, []); // deep-equal: no notification
  store.transact('add', (tx) => tx.set(['notes'], ['x']));
  assert.deepEqual(seen, [['x']]);
});
