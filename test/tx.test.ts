import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store } from '../src/index.ts';

test('readers never see a transaction’s intermediate state', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, title: 'before' });
  const snap = s.snapshot();
  const insideReads: unknown[] = [];
  s.tx((t) => {
    t.update('Todo:1', { title: 'mid' });
    insideReads.push(s.get('Todo:1')!.title); // outside view inside the tx
    insideReads.push(t.get('Todo:1')!.title); // read-your-writes view
    t.update('Todo:1', { title: 'after' });
  });
  assert.deepEqual(insideReads, ['before', 'mid']);
  assert.equal(s.get('Todo:1')!.title, 'after');
  assert.equal(snap.get('Todo:1')!.title, 'before', 'old snapshot is frozen');
});

test('a throwing transaction commits nothing', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, title: 'safe' });
  const journalLen = s.getJournal().length;
  assert.throws(() =>
    s.tx((t) => {
      t.update('Todo:1', { title: 'corrupt' });
      t.delete('Todo:1');
      throw new Error('boom');
    }),
  );
  assert.equal(s.get('Todo:1')!.title, 'safe');
  assert.equal(s.getJournal().length, journalLen, 'no ops were journaled');
});

test('queries are notified once per commit, not per op', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, a: 1, b: 1 });
  const q = s.query((read) => read.denormalize('Todo:1'));
  let calls = 0;
  q.subscribe(() => calls++);
  s.tx((t) => {
    t.update('Todo:1', { a: 2 });
    t.update('Todo:1', { b: 2 });
  });
  assert.equal(calls, 1);
  q.dispose();
});

test('the op journal replays into an identical store', () => {
  const s = new Store();
  s.ingest('Board', { id: 1, name: 'board' });
  s.appendConnection('Board:1', 'todos', [
    { __typename: 'Todo', id: 1, title: 'a' },
    { __typename: 'Todo', id: 2, title: 'b' },
  ]);
  const l = s.beginOptimistic();
  s.writeOptimistic(l.layerId, 'Todo:1', { title: 'a-opt' });
  const l2 = s.beginOptimistic();
  s.writeOptimistic(l2.layerId, 'Todo:2', { title: 'b-opt' });
  s.rollbackLayer(l2.layerId);
  s.commitLayer(l.layerId, { type: 'Todo', data: { id: 1, title: 'a-confirmed' } });
  s.ingest('Todo', { id: 3, title: 'c' });
  s.delete('Todo:2');
  s.mergeIdentities('Todo:3', 'Todo:33');
  s.ingest('Todo', { id: 1, title: 'stale' }, { version: 1 });

  const replayed = Store.replay(s.getJournal());
  assert.deepEqual(replayed.snapshot().toJSON(), s.snapshot().toJSON());
});

test('replayed store keeps working: leases, overlays and deletes', () => {
  const s = new Store();
  s.ingest('Post', { id: 1, author: { __typename: 'User', id: 1, name: 'A' } });
  s.delete('User:1');
  const r = Store.replay(s.getJournal());
  assert.equal(r.get('Post:1')!.author, null);
  assert.equal(r.get('User:1'), undefined);
  const q = r.query((read) => read.keysOfType('Post'));
  assert.deepEqual(q.result, ['Post:1']);
  q.dispose();
});
