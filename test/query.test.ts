import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store } from '../src/index.ts';

const seed = () => {
  const s = new Store();
  s.ingest('Post', [
    { __typename: 'Post', id: 1, title: 'p1', author: { __typename: 'User', id: 1, name: 'Ada' } },
    { __typename: 'Post', id: 2, title: 'p2', author: { __typename: 'User', id: 1, name: 'Ada' } },
  ]);
  return s;
};

test('live query holds leases on every entity it read', () => {
  const s = seed();
  const q = s.query((read) => read.denormalize('Post:1'));
  assert.deepEqual(q.leaseKeys, ['Post:1', 'User:1']);
  q.dispose();
});

test('query re-evaluates when a leased entity changes, stays quiet otherwise', () => {
  const s = seed();
  const q = s.query((read) => (read.denormalize('Post:1') as any)?.author?.name);
  const seen: string[] = [];
  q.subscribe((r) => seen.push(r));
  s.update('Post:2', { title: 'unrelated' });
  assert.deepEqual(seen, [], 'unrelated change does not notify');
  s.update('User:1', { name: 'Grace' });
  assert.deepEqual(seen, ['Grace'], 'change through shared reference notifies');
  q.dispose();
});

test('deleting a leased entity notifies live queries', () => {
  const s = seed();
  const q = s.query((read) => read.denormalize('Post:1'));
  const seen: unknown[] = [];
  q.subscribe((r) => seen.push(r));
  s.delete('User:1');
  assert.equal(seen.length, 1);
  assert.equal((seen[0] as any).author, null, 'deleted reference reads as null');
  q.dispose();
});

test('disposed queries stop receiving notifications', () => {
  const s = seed();
  const q = s.query((read) => read.get('User:1')?.name);
  let calls = 0;
  q.subscribe(() => calls++);
  q.dispose();
  s.update('User:1', { name: 'x' });
  assert.equal(calls, 0);
});

test('type-wildcard lease fires when an entity of that type appears', () => {
  const s = new Store();
  const q = s.query((read) => read.keysOfType('Todo'));
  const seen: string[][] = [];
  q.subscribe((r) => seen.push(r));
  s.ingest('Todo', { id: 1, title: 'first' });
  assert.deepEqual(seen, [['Todo:1']]);
  q.dispose();
});

test('no notification when the recomputed result is unchanged', () => {
  const s = seed();
  const q = s.query((read) => read.get('User:1')?.name);
  let calls = 0;
  q.subscribe(() => calls++);
  s.update('User:1', { name: 'Ada' }); // same value
  assert.equal(calls, 0);
  q.dispose();
});
