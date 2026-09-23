import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store } from '../src/store.ts';
import type { Notification, Patch } from '../src/store.ts';
import { referenceDispatch } from '../src/reference.ts';
import { parsePattern } from '../src/pattern.ts';
import type { Pattern } from '../src/pattern.ts';

test('parent subscription receives child changes', () => {
  const store = new Store({ users: {} });
  const seen: Notification[] = [];
  store.subscribe('/users', (n) => seen.push(n));
  store.transact((tx) => tx.set('/users/u1/name', 'ada'));
  assert.equal(seen.length, 1);
  assert.deepEqual(seen[0].patches.map((p) => p.path), [['users', 'u1', 'name']]);
});

test('adjacent string prefix is not an ancestor', () => {
  const store = new Store({ users: {} });
  const seen: Notification[] = [];
  store.subscribe('/users/ab', (n) => seen.push(n));
  store.transact((tx) => tx.set('/users/abc/x', 1));
  assert.equal(seen.length, 0);
  store.transact((tx) => tx.set('/users/ab/x', 1));
  assert.equal(seen.length, 1);
});

test('wildcard subscription across one segment', () => {
  const store = new Store({ users: {} });
  const seen: Notification[] = [];
  store.subscribe('/users/*/email', (n) => seen.push(n));
  store.transact((tx) => {
    tx.set('/users/u1/email', 'a@x');
    tx.set('/users/u2/name', 'bob');
  });
  assert.equal(seen.length, 1);
  assert.deepEqual(seen[0].patches[0].path, ['users', 'u1', 'email']);
});

test('move notifies both old and new paths with one op identity', () => {
  const store = new Store({ a: { x: 1 }, b: {} });
  const oldSeen: Notification[] = [];
  const newSeen: Notification[] = [];
  store.subscribe('/a', (n) => oldSeen.push(n));
  store.subscribe('/b', (n) => newSeen.push(n));
  store.transact((tx) => tx.move('/a/x', '/b/y'));

  assert.equal(oldSeen.length, 1);
  assert.equal(newSeen.length, 1);
  const oldPatch = oldSeen[0].patches[0];
  const newPatch = newSeen[0].patches[0];
  assert.equal(oldPatch.kind, 'delete');
  assert.deepEqual(oldPatch.movedTo, ['b', 'y']);
  assert.equal(newPatch.kind, 'set');
  assert.deepEqual(newPatch.movedFrom, ['a', 'x']);
  assert.equal(oldPatch.opId, newPatch.opId); // same operation identity
  assert.deepEqual(store.state, { a: {}, b: { y: 1 } });
});

test('duplicate matches are deduplicated into one notification, op order preserved', () => {
  const store = new Store({ a: {} });
  const seen: Notification[] = [];
  store.subscribe('/a', (n) => seen.push(n));
  store.subscribe('/a/*', (n) => seen.push(n));
  store.transact((tx) => {
    tx.set('/a/x', 1);
    tx.set('/a/y', 2);
    tx.move('/a/x', '/a/z');
  });
  assert.equal(seen.length, 2); // one notification per subscription
  for (const n of seen) {
    assert.deepEqual(n.opIds, [1, 2, 3]); // operation order + identity preserved
    assert.deepEqual(
      n.patches.map((p) => p.kind),
      ['set', 'set', 'delete', 'set'],
    );
  }
});

test('array element delete notifies the shifted index range', () => {
  const store = new Store({ arr: ['a', 'b', 'c', 'd', 'e'] });
  const seen = new Map<string, Notification[]>();
  for (const p of ['/arr/0', '/arr/1', '/arr/2', '/arr/3', '/arr/4', '/arr']) {
    seen.set(p, []);
    store.subscribe(p, (n) => seen.get(p)!.push(n));
  }
  let range: Patch['range'];
  store.transact((tx) => tx.delete('/arr/1'));
  const result = store.ops.at(-1)!;

  // delete on an array element is journaled as a splice
  assert.equal(result.kind, 'splice');
  assert.deepEqual(store.state, { arr: ['a', 'c', 'd', 'e'] });

  // range covers [deletedIndex, oldLength-1]: indices 1..4 shifted
  const n1 = seen.get('/arr/1')![0];
  range = n1.patches[0].range;
  assert.deepEqual(range, [1, 4]);
  assert.equal(seen.get('/arr/0')!.length, 0); // untouched index stays silent
  for (const p of ['/arr/1', '/arr/2', '/arr/3', '/arr/4', '/arr']) {
    assert.equal(seen.get(p)!.length, 1, `${p} should be notified`);
    assert.deepEqual(seen.get(p)![0].patches[0].range, [1, 4]);
  }
});

test('array insert shifts are included in the notified range', () => {
  const store = new Store({ arr: [1, 2, 3] });
  const seen: Notification[] = [];
  store.subscribe('/arr/*', (n) => seen.push(n));
  store.transact((tx) => tx.splice('/arr', 1, 0, 9));
  assert.deepEqual(store.state, { arr: [1, 9, 2, 3] });
  assert.equal(seen.length, 1);
  assert.deepEqual(seen[0].patches[0].range, [1, 3]); // insert at 1 shifts 1..2, new length 4
});

test('subscription add/remove take effect at the transaction boundary', () => {
  const store = new Store({ a: {} });
  const added: Notification[] = [];
  const removed: Notification[] = [];
  const removedId = store.subscribe('/a', (n) => removed.push(n));

  store.transact((tx) => {
    tx.set('/a/x', 1);
    store.subscribe('/a', (n) => added.push(n)); // staged
    store.unsubscribe(removedId); // staged
  });
  // staged add did NOT see this commit; staged remove STILL saw it
  assert.equal(added.length, 0);
  assert.equal(removed.length, 1);

  store.transact((tx) => tx.set('/a/y', 2));
  assert.equal(added.length, 1); // now active
  assert.equal(removed.length, 1); // now gone
});

test('reentrant writes from callbacks apply at the transaction boundary', () => {
  const store = new Store({ a: 0, b: 0 });
  const order: string[] = [];
  store.subscribe('/a', (_n, s) => {
    order.push(`a-notified b=${(s.state as { b: number }).b}`);
    s.transact((tx) => tx.set('/b', 1)); // reentrant: queued to boundary
    order.push(`after-reentrant-call b=${(s.state as { b: number }).b}`);
  });
  store.subscribe('/b', () => order.push('b-notified'));

  store.transact((tx) => tx.set('/a', 1));
  assert.deepEqual(order, [
    'a-notified b=0', // intermediate state of the queued tx not visible
    'after-reentrant-call b=0',
    'b-notified', // queued transaction committed at the boundary
  ]);
  assert.deepEqual(store.state, { a: 1, b: 1 });
});

test('readers never see intermediate transaction state', () => {
  const store = new Store({ a: { x: 1 } });
  const before = store.state;
  const during: unknown[] = [];
  store.transact((tx) => {
    tx.set('/a/x', 2);
    tx.set('/a/y', 3);
    during.push(store.state); // mid-transaction read
  });
  assert.equal(during[0], before); // same committed snapshot
  assert.deepEqual(during[0], { a: { x: 1 } });
  assert.deepEqual(store.state, { a: { x: 2, y: 3 } });
  // committed snapshots are frozen
  assert.throws(() => {
    (before as { a: { x: number } }).a.x = 99;
  }, TypeError);
});

test('journal replays into an identical state', () => {
  const store = new Store({ users: {}, list: [1, 2, 3] });
  store.transact((tx) => {
    tx.set('/users/u1', { name: 'ada' });
    tx.move('/users/u1', '/users/u2');
    tx.delete('/list/0');
    tx.splice('/list', 1, 0, 9, 8);
  });
  const replica = new Store({ users: {}, list: [1, 2, 3] });
  replica.replay(store.ops);
  assert.deepEqual(replica.state, store.state);
});

test('automaton dispatch matches the independent per-subscription reference', () => {
  const store = new Store({ a: { b: {} }, arr: [0, 1, 2, 3] });
  const patterns = new Map<number, Pattern>();
  const sources = ['/a', '/a/b', '/a/*', '/a/b/c', '/arr', '/arr/2', '/arr/*', '/'];
  for (const source of sources) {
    const id = store.subscribe(source, () => {});
    patterns.set(id, parsePattern(source));
  }
  const result = store.transact((tx) => {
    tx.set('/a/b/c', 1);
    tx.move('/a/b/c', '/a/b/d');
    tx.delete('/arr/1');
    tx.set('/arr/0', 99);
  })!;

  const reference = referenceDispatch(patterns, result.patches);
  const actual = new Map(result.notifications.map((n) => [n.subId, n.patches]));
  assert.deepEqual(
    [...actual.keys()].sort((x, y) => x - y),
    [...reference.bySub.keys()].sort((x, y) => x - y),
  );
  for (const [id, patches] of actual) {
    assert.deepEqual(patches, reference.bySub.get(id));
  }
  console.log(
    `    [stats] patches=${result.patches.length} automatonVisited=${result.visited} ` +
      `referenceComparisons=${reference.comparisons}`,
  );
});
