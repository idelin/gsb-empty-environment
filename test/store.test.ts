import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store, type JsonValue, type Operation } from '../src/store.ts';

function replay(ops: readonly Operation[]): Store {
  const store = new Store('observer');
  for (const op of ops) store.apply(op);
  return store;
}

function permutations<T>(items: readonly T[]): T[][] {
  if (items.length <= 1) return [[...items]];
  const result: T[][] = [];
  for (let i = 0; i < items.length; i += 1) {
    const rest = [...items.slice(0, i), ...items.slice(i + 1)];
    for (const tail of permutations(rest)) {
      result.push([items[i], ...tail]);
    }
  }
  return result;
}

test('concurrent sets on independent fields merge', () => {
  const alice = new Store('alice');
  const bob = new Store('bob');
  const opA = alice.set(['user', 'name'], 'alice');
  const opB = bob.set(['user', 'age'], 30);
  alice.apply(opB);
  bob.apply(opA);
  assert.deepEqual(alice.materialize(), { user: { name: 'alice', age: 30 } });
  assert.deepEqual(bob.materialize(), alice.materialize());
});

test('higher version wins regardless of arrival order', () => {
  const older: Operation = { kind: 'set', path: ['k'], value: 'old', version: 1, clientId: 'a' };
  const newer: Operation = { kind: 'set', path: ['k'], value: 'new', version: 2, clientId: 'a' };
  assert.equal(replay([older, newer]).get(['k']), 'new');
  assert.equal(replay([newer, older]).get(['k']), 'new');
});

test('equal versions are decided by client id, order independent', () => {
  const fromA: Operation = { kind: 'set', path: ['k'], value: 'A', version: 1, clientId: 'a' };
  const fromB: Operation = { kind: 'set', path: ['k'], value: 'B', version: 1, clientId: 'b' };
  assert.equal(replay([fromA, fromB]).get(['k']), 'B');
  assert.equal(replay([fromB, fromA]).get(['k']), 'B');
});

test('delete leaves a tombstone: stale values never resurrect', () => {
  const set: Operation = { kind: 'set', path: ['k'], value: 'v1', version: 1, clientId: 'a' };
  const del: Operation = { kind: 'delete', path: ['k'], version: 2, clientId: 'a' };
  assert.equal(replay([set, del]).get(['k']), undefined);
  assert.equal(replay([del, set]).get(['k']), undefined);
  assert.deepEqual(replay([del, set]).materialize(), {});
});

test('a newer set may revive a deleted field', () => {
  const set: Operation = { kind: 'set', path: ['k'], value: 'v1', version: 1, clientId: 'a' };
  const del: Operation = { kind: 'delete', path: ['k'], version: 2, clientId: 'a' };
  const revive: Operation = { kind: 'set', path: ['k'], value: 'v2', version: 3, clientId: 'a' };
  assert.equal(replay([set, del, revive]).get(['k']), 'v2');
  assert.equal(replay([revive, del, set]).get(['k']), 'v2');
});

test('object updates apply per leaf field', () => {
  const store = new Store('a');
  store.set(['doc'], { x: 1, y: 2 });
  store.set(['doc'], { x: 10 });
  assert.deepEqual(store.get(['doc']), { x: 10, y: 2 });
  store.set(['doc', 'y'], 20);
  assert.deepEqual(store.get(['doc']), { x: 10, y: 20 });
});

test('nested object leaves merge across clients', () => {
  const ops: Operation[] = [
    { kind: 'set', path: ['cfg'], value: { db: { host: 'h1', port: 1 } }, version: 1, clientId: 'a' },
    { kind: 'set', path: ['cfg', 'db', 'port'], value: 2, version: 1, clientId: 'b' },
  ];
  assert.deepEqual(replay(ops).get(['cfg']), { db: { host: 'h1', port: 2 } });
  assert.deepEqual(replay([...ops].reverse()).get(['cfg']), { db: { host: 'h1', port: 2 } });
});

test('arrays are replaced as a whole, never merged element-wise', () => {
  const store = new Store('a');
  store.set(['doc'], { list: [1, 2, 3], n: 1 });
  store.set(['doc', 'list'], [9]);
  assert.deepEqual(store.get(['doc']), { list: [9], n: 1 });
});

test('stale array writes do not resurrect removed elements', () => {
  const oldArr: Operation = { kind: 'set', path: ['l'], value: [1, 2, 3], version: 1, clientId: 'a' };
  const newArr: Operation = { kind: 'set', path: ['l'], value: [7], version: 2, clientId: 'a' };
  assert.deepEqual(replay([newArr, oldArr]).get(['l']), [7]);
  assert.deepEqual(replay([oldArr, newArr]).get(['l']), [7]);
});

test('scalar replaces object and object replaces scalar by version', () => {
  const obj: Operation = { kind: 'set', path: ['a'], value: { b: 1 }, version: 1, clientId: 'a' };
  const scalar: Operation = { kind: 'set', path: ['a'], value: 'x', version: 2, clientId: 'a' };
  assert.deepEqual(replay([obj, scalar]).materialize(), { a: 'x' });
  assert.deepEqual(replay([scalar, obj]).materialize(), { a: 'x' });
  const obj2: Operation = { kind: 'set', path: ['a'], value: { b: 2 }, version: 3, clientId: 'a' };
  assert.deepEqual(replay([obj, scalar, obj2]).materialize(), { a: { b: 2 } });
});

test('deleting a subtree shadows old leaves but not newer concurrent ones', () => {
  const oldLeaf: Operation = { kind: 'set', path: ['a', 'b'], value: 1, version: 1, clientId: 'a' };
  const del: Operation = { kind: 'delete', path: ['a'], version: 2, clientId: 'a' };
  const newLeaf: Operation = { kind: 'set', path: ['a', 'c'], value: 2, version: 3, clientId: 'b' };
  assert.deepEqual(replay([oldLeaf, del]).materialize(), {});
  assert.deepEqual(replay([del, oldLeaf]).materialize(), {});
  assert.deepEqual(replay([oldLeaf, del, newLeaf]).materialize(), { a: { c: 2 } });
  assert.deepEqual(replay([newLeaf, del, oldLeaf]).materialize(), { a: { c: 2 } });
});

test('delete of a field inside an object keeps sibling fields', () => {
  const store = new Store('a');
  store.set(['doc'], { x: 1, y: 2 });
  store.delete(['doc', 'x']);
  assert.deepEqual(store.get(['doc']), { y: 2 });
});

test('local clock advances past observed remote versions', () => {
  const alice = new Store('alice');
  const bob = new Store('bob');
  bob.set(['k'], 'v');
  bob.set(['k'], 'v2');
  const remote = bob.set(['k'], 'v3');
  alice.apply(remote);
  const local = alice.set(['j'], 1);
  assert.equal(remote.version, 3);
  assert.equal(local.version, 4);
});

test('restore from snapshot converges with replay', () => {
  const ops: Operation[] = [
    { kind: 'set', path: ['user'], value: { name: 'n', tags: ['a', 'b'] }, version: 1, clientId: 'a' },
    { kind: 'delete', path: ['user', 'name'], version: 2, clientId: 'b' },
    { kind: 'set', path: ['user', 'tags'], value: ['c'], version: 3, clientId: 'a' },
  ];
  const replayed = replay(ops);
  const restored = Store.restore(replayed.snapshot());
  assert.deepEqual(restored.materialize(), replayed.materialize());
  assert.deepEqual(restored.snapshot(), replayed.snapshot());
  assert.equal(restored.version, replayed.version);
});

test('all permutations of concurrent ops converge to one state', () => {
  const ops: Operation[] = [
    { kind: 'set', path: ['user', 'name'], value: 'alice', version: 1, clientId: 'c1' },
    { kind: 'set', path: ['user', 'age'], value: 30, version: 2, clientId: 'c1' },
    { kind: 'set', path: ['user', 'name'], value: 'carol', version: 3, clientId: 'c1' },
    { kind: 'set', path: ['user', 'name'], value: 'bob', version: 1, clientId: 'c2' },
    { kind: 'delete', path: ['user', 'age'], version: 2, clientId: 'c2' },
    { kind: 'set', path: ['user', 'tags'], value: ['a', 'b'], version: 1, clientId: 'c3' },
    { kind: 'set', path: ['user', 'tags'], value: ['c'], version: 2, clientId: 'c3' },
  ];
  const expected: JsonValue = { user: { name: 'carol', tags: ['c'] } };
  const orders = permutations(ops);
  assert.equal(orders.length, 5040);
  for (const order of orders) {
    const store = replay(order);
    assert.deepEqual(store.materialize(), expected);
    assert.deepEqual(Store.restore(store.snapshot()).materialize(), expected);
  }
});

test('all permutations: deleted old values never resurrect, independent fields merge', () => {
  const ops: Operation[] = [
    { kind: 'set', path: ['doc'], value: { title: 't1', body: 'b1' }, version: 1, clientId: 'c1' },
    { kind: 'delete', path: ['doc', 'title'], version: 2, clientId: 'c1' },
    { kind: 'set', path: ['doc', 'body'], value: 'b2', version: 1, clientId: 'c2' },
    { kind: 'set', path: ['doc', 'meta'], value: { likes: 1 }, version: 2, clientId: 'c2' },
    { kind: 'delete', path: ['doc', 'meta'], version: 3, clientId: 'c1' },
  ];
  const expected: JsonValue = { doc: { body: 'b2' } };
  for (const order of permutations(ops)) {
    const store = replay(order);
    assert.deepEqual(store.materialize(), expected);
    assert.deepEqual(Store.restore(store.snapshot()).materialize(), expected);
  }
});
