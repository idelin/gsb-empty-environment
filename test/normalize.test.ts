import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store } from '../src/index.ts';

const post = (id: number, title: string, author: object) => ({
  __typename: 'Post',
  id,
  title,
  author,
});

test('nested response is split into entity tables keyed by type:id', () => {
  const s = new Store();
  s.ingest('Post', [
    post(1, 'Hello', { __typename: 'User', id: 1, name: 'Ada' }),
    post(2, 'World', { __typename: 'User', id: 1, name: 'Ada' }),
  ]);
  assert.deepEqual(s.snapshot().keysOfType('Post'), ['Post:1', 'Post:2']);
  assert.deepEqual(s.snapshot().keysOfType('User'), ['User:1']);
  // parents hold shared references, not copies
  assert.deepEqual(s.get('Post:1')!.author, { $ref: 'User:1' });
  assert.deepEqual(s.get('Post:2')!.author, { $ref: 'User:1' });
});

test('two parents referencing the same entity both see the update', () => {
  const s = new Store();
  s.ingest('Post', [
    post(1, 'Hello', { __typename: 'User', id: 1, name: 'Ada' }),
    post(2, 'World', { __typename: 'User', id: 1, name: 'Ada' }),
  ]);
  s.update('User:1', { name: 'Grace' });
  const p1 = s.denormalize('Post:1') as any;
  const p2 = s.denormalize('Post:2') as any;
  assert.equal(p1.author.name, 'Grace');
  assert.equal(p2.author.name, 'Grace');
});

test('reverse index tracks every referrer of a shared entity', () => {
  const s = new Store();
  s.ingest('Post', [
    post(1, 'Hello', { __typename: 'User', id: 1, name: 'Ada' }),
    post(2, 'World', { __typename: 'User', id: 1, name: 'Ada' }),
  ]);
  const json = s.snapshot().toJSON() as any;
  assert.deepEqual(json.reverse['User:1'], ['Post:1 x1', 'Post:2 x1']);
});

test('missing fields keep old values; explicit null is stored as null', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, title: 'a', note: 'b', done: false });
  s.update('Todo:1', { note: null }); // title/done absent -> preserved
  const rec = s.get('Todo:1')!;
  assert.equal(rec.title, 'a');
  assert.equal(rec.done, false);
  assert.equal(rec.note, null);
  assert.ok('note' in rec, 'explicit null is present, not missing');
  assert.ok(!('extra' in rec), 'never-sent field stays missing');
});

test('undefined field values are treated as missing, not as null', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, title: 'a' });
  s.update('Todo:1', { title: undefined, note: null });
  const rec = s.get('Todo:1')!;
  assert.equal(rec.title, 'a');
  assert.equal(rec.note, null);
});

test('id-less nested objects are embedded and rebuilt wholesale, never merged', () => {
  const s = new Store();
  s.ingest('Post', [
    { ...post(1, 'p1', { __typename: 'User', id: 1, name: 'A' }), meta: { views: 10, likes: 1 } },
    { ...post(2, 'p2', { __typename: 'User', id: 1, name: 'A' }), meta: { views: 20, likes: 2 } },
  ]);
  // identical shape, but the two inline objects must stay independent
  s.update('Post:1', { meta: { views: 99 } });
  const p1 = s.get('Post:1')!.meta as any;
  const p2 = s.get('Post:2')!.meta as any;
  assert.deepEqual(p1, { views: 99 }, 'inline object replaced wholesale (likes dropped)');
  assert.deepEqual(p2, { views: 20, likes: 2 }, 'other parent unaffected');
});

test('entities nested inside id-less objects still alias the shared table', () => {
  const s = new Store();
  s.ingest('Post', {
    ...post(1, 'p1', { __typename: 'User', id: 1, name: 'A' }),
    meta: { editor: { __typename: 'User', id: 9, name: 'Ed' } },
  });
  s.update('User:9', { name: 'Edith' });
  const p = s.denormalize('Post:1') as any;
  assert.equal(p.meta.editor.name, 'Edith');
  const json = s.snapshot().toJSON() as any;
  assert.deepEqual(json.reverse['User:9'], ['Post:1 x1']);
});
