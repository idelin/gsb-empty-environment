import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store } from '../src/index.ts';

test('partial update preserves unreturned fields', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, title: 'a', body: 'long', done: false });
  s.ingest('Todo', { id: 1, done: true }); // partial response
  const rec = s.get('Todo:1')!;
  assert.equal(rec.title, 'a');
  assert.equal(rec.body, 'long');
  assert.equal(rec.done, true);
});

test('stale server response cannot overwrite a newer field version', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, title: 'new' }, { version: 5 });
  s.ingest('Todo', { id: 1, title: 'old', body: 'from-old-response' }, { version: 3 });
  const rec = s.get('Todo:1')!;
  assert.equal(rec.title, 'new', 'older versioned field is rejected');
  assert.equal(rec.body, 'from-old-response', 'fields not seen before still apply');
});

test('explicit delete cleans the reverse index and nulls referrers', () => {
  const s = new Store();
  s.ingest('Post', [
    { __typename: 'Post', id: 1, author: { __typename: 'User', id: 1, name: 'A' } },
    { __typename: 'Post', id: 2, author: { __typename: 'User', id: 1, name: 'A' } },
  ]);
  s.delete('User:1');
  assert.equal(s.get('User:1'), undefined);
  assert.equal(s.get('Post:1')!.author, null);
  assert.equal(s.get('Post:2')!.author, null);
  const json = s.snapshot().toJSON() as any;
  assert.equal(json.reverse['User:1'], undefined, 'reverse index entry removed');
});

test('delete handles reference cycles without looping', () => {
  const s = new Store();
  s.ingest('Node', { id: 'a', friend: { __typename: 'Node', id: 'b' } });
  s.ingest('Node', { id: 'b', friend: { __typename: 'Node', id: 'a' } });
  // delete only one side: the other's pointer is cleaned
  s.delete('Node:a');
  assert.equal(s.get('Node:b')!.friend, null);
  // delete both sides of a cycle in one transaction
  s.ingest('Node', { id: 'x', friend: { __typename: 'Node', id: 'y' } });
  s.ingest('Node', { id: 'y', friend: { __typename: 'Node', id: 'x' } });
  s.tx((t) => {
    t.delete('Node:x');
    t.delete('Node:y');
  });
  assert.equal(s.get('Node:x'), undefined);
  assert.equal(s.get('Node:y'), undefined);
  const json = s.snapshot().toJSON() as any;
  assert.deepEqual(Object.keys(json.reverse), []);
});

test('delete handles self-references and array references', () => {
  const s = new Store();
  s.ingest('Node', { id: 1, self: { __typename: 'Node', id: 1 } });
  s.ingest('List', {
    id: 1,
    items: [{ __typename: 'Item', id: 1 }, { __typename: 'Item', id: 2 }],
  });
  s.delete('Node:1');
  assert.equal(s.get('Node:1'), undefined);
  s.delete('Item:1');
  assert.deepEqual(s.get('List:1')!.items, [{ $ref: 'Item:2' }]);
});

test('tombstone blocks stale resurrection but allows newer data', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, title: 'x' }, { version: 1 });
  s.delete('Todo:1', { version: 6 });
  s.ingest('Todo', { id: 1, title: 'zombie' }, { version: 5 });
  assert.equal(s.get('Todo:1'), undefined, 'stale write cannot resurrect');
  s.ingest('Todo', { id: 1, title: 'back' }, { version: 7 });
  assert.equal(s.get('Todo:1')!.title, 'back', 'newer write may resurrect');
});

test('identity merge retargets referrers and merges fields atomically', () => {
  const s = new Store();
  // optimistic create under a temp id, referenced from a list
  s.ingest('Todo', { id: 'tmp-1', title: 'draft', optimistic: true }, { version: 2 });
  s.ingest('Board', {
    id: 1,
    todos: [{ __typename: 'Todo', id: 1 }, { __typename: 'Todo', id: 'tmp-1' }],
  });
  s.ingest('Todo', { id: 42, title: 'draft', createdAt: 'today' }, { version: 3 });
  s.mergeIdentities('Todo:tmp-1', 'Todo:42');

  const board = s.get('Board:1')!;
  assert.deepEqual(board.todos, [{ $ref: 'Todo:1' }, { $ref: 'Todo:42' }]);
  const merged = s.get('Todo:42')!;
  assert.equal(merged.title, 'draft');
  assert.equal(merged.optimistic, true, 'field only known on temp identity survives');
  assert.equal(merged.createdAt, 'today');
  // old key resolves through the alias
  assert.equal(s.get('Todo:tmp-1')!.createdAt, 'today');
  const json = s.snapshot().toJSON() as any;
  assert.deepEqual(json.reverse['Todo:42'], ['Board:1 x1']);
  assert.equal(json.reverse['Todo:tmp-1'], undefined);
  assert.deepEqual(json.aliases, [['Todo:tmp-1', 'Todo:42']]);
});

test('paginated relations append pages, dedupe refs and keep sibling fields', () => {
  const s = new Store();
  s.ingest('Feed', { id: 1, label: 'home' });
  s.appendConnection(
    'Feed:1',
    'posts',
    [
      { __typename: 'Post', id: 1, title: 'a' },
      { __typename: 'Post', id: 2, title: 'b' },
    ],
    { endCursor: 'c1', hasNextPage: true },
  );
  s.appendConnection(
    'Feed:1',
    'posts',
    [
      { __typename: 'Post', id: 3, title: 'c' },
      { __typename: 'Post', id: 1, title: 'a-v2' }, // duplicate ref, fresher data
    ],
    { endCursor: 'c2', hasNextPage: false },
  );
  const feed = s.get('Feed:1')!;
  assert.equal(feed.label, 'home', 'unrelated field preserved');
  const conn = feed.posts as any;
  assert.deepEqual(conn.items, [{ $ref: 'Post:1' }, { $ref: 'Post:2' }, { $ref: 'Post:3' }]);
  assert.deepEqual(conn.pageInfo, { endCursor: 'c2', hasNextPage: false });
  assert.equal(s.get('Post:1')!.title, 'a-v2', 'page item data still normalized');
});
