import assert from 'node:assert/strict';
import { test } from 'node:test';
import { Store } from '../src/index.ts';

test('transaction commits atomically; readers never see intermediate state', () => {
  const store = new Store({ counter: 0 }, 'v1');
  const seenDuring: unknown[] = [];
  store.transaction((tx) => {
    tx.set('counter', 1);
    seenDuring.push(store.get('counter'));
    tx.set('counter', 2);
    seenDuring.push(store.get('counter'));
  });
  assert.deepEqual(seenDuring, [0, 0]);
  assert.equal(store.get('counter'), 2);
  assert.equal(store.journal.length, 1);
});

test('failed transaction leaves state untouched and writes no journal entries', () => {
  const store = new Store({ counter: 0 }, 'v1');
  let notified = 0;
  store.subscribe((s) => s.counter, () => {
    notified += 1;
  });
  assert.throws(() => {
    store.transaction((tx) => {
      tx.set('counter', 99);
      throw new Error('boom');
    });
  }, /boom/);
  assert.equal(store.get('counter'), 0);
  assert.equal(notified, 0);
  assert.equal(store.journal.length, 0);
});

test('paused subscriptions batch into a single notification on resume', () => {
  const store = new Store({ counter: 0 }, 'v1');
  const events: unknown[] = [];
  store.subscribe(
    (s) => s.counter,
    (next) => events.push(next),
  );
  store.pauseSubscriptions();
  store.transaction((tx) => tx.set('counter', 1));
  store.transaction((tx) => tx.set('counter', 2));
  assert.deepEqual(events, []);
  store.resumeSubscriptions();
  assert.deepEqual(events, [2]);
});

test('committed snapshots are frozen for readers', () => {
  const store = new Store({ user: { name: 'ada' } }, 'v1');
  const snapshot = store.getState();
  assert.throws(() => {
    (snapshot.user as Record<string, unknown>).name = 'grace';
  }, TypeError);
  assert.equal(store.get('user.name'), 'ada');
});

test('unsubscribe stops notifications', () => {
  const store = new Store({ counter: 0 }, 'v1');
  let notified = 0;
  const off = store.subscribe(
    (s) => s.counter,
    () => {
      notified += 1;
    },
  );
  store.transaction((tx) => tx.set('counter', 1));
  off();
  store.transaction((tx) => tx.set('counter', 2));
  assert.equal(notified, 1);
});
