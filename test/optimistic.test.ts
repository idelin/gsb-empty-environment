import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store } from '../src/index.ts';

test('optimistic overlay shadows committed state for readers', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, title: 'committed' });
  const { layerId } = s.beginOptimistic();
  s.writeOptimistic(layerId, 'Todo:1', { title: 'optimistic' });
  assert.equal(s.get('Todo:1')!.title, 'optimistic');
  // the committed snapshot taken before still shows the old value
  s.rollbackLayer(layerId);
  assert.equal(s.get('Todo:1')!.title, 'committed');
});

test('stale server ack cannot overwrite a newer user-submitted field version', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, title: 'base' });
  // user submits edit A, then a newer edit B for the same field
  const a = s.beginOptimistic();
  s.writeOptimistic(a.layerId, 'Todo:1', { title: 'A' });
  const b = s.beginOptimistic();
  s.writeOptimistic(b.layerId, 'Todo:1', { title: 'B' });
  // server confirms the NEWER edit first
  s.commitLayer(b.layerId, { type: 'Todo', data: { id: 1, title: 'B' } });
  // then the STALE ack for edit A arrives
  s.commitLayer(a.layerId, { type: 'Todo', data: { id: 1, title: 'A' } });
  assert.equal(s.get('Todo:1')!.title, 'B', 'older ack must not clobber newer version');
});

test('in-flight optimistic edit survives an older server response', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, title: 'v1' }, { version: 2 });
  const { layerId } = s.beginOptimistic(); // version 3
  s.writeOptimistic(layerId, 'Todo:1', { title: 'user-edit' });
  // an in-flight query response rendered before the user's edit lands
  s.ingest('Todo', { id: 1, title: 'v1-again', done: false }, { version: 1 });
  assert.equal(s.get('Todo:1')!.title, 'user-edit', 'overlay still shadows stale base write');
  assert.equal(s.get('Todo:1')!.done, false, 'untouched fields from response apply');
  s.rollbackLayer(layerId);
  assert.equal(s.get('Todo:1')!.title, 'v1', 'base kept the newest confirmed version');
});

test('rollback undoes only the layer’s own changes', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, title: 'base-title', body: 'base-body' });
  const l1 = s.beginOptimistic();
  s.writeOptimistic(l1.layerId, 'Todo:1', { title: 'l1-title' });
  const l2 = s.beginOptimistic();
  s.writeOptimistic(l2.layerId, 'Todo:1', { body: 'l2-body' });
  s.rollbackLayer(l1.layerId);
  const rec = s.get('Todo:1')!;
  assert.equal(rec.title, 'base-title', 'rolled-back layer reverted');
  assert.equal(rec.body, 'l2-body', 'other layer untouched');
  s.rollbackLayer(l2.layerId);
  assert.equal(s.get('Todo:1')!.body, 'base-body');
});

test('commitLayer confirms server data at the layer version and drops the layer', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, title: 'draft' });
  const l = s.beginOptimistic();
  s.writeOptimistic(l.layerId, 'Todo:1', { title: 'renamed' });
  s.commitLayer(l.layerId, { type: 'Todo', data: { id: 1, title: 'renamed', done: false } });
  const rec = s.get('Todo:1')!;
  assert.equal(rec.title, 'renamed');
  assert.equal(rec.done, false);
  const json = s.snapshot().toJSON() as any;
  assert.deepEqual(json.layers, [], 'no layers left');
});

test('optimistic delete hides the entity; rollback restores it', () => {
  const s = new Store();
  s.ingest('Todo', { id: 1, title: 'x' });
  const l = s.beginOptimistic();
  s.deleteOptimistic(l.layerId, 'Todo:1');
  assert.equal(s.get('Todo:1'), undefined);
  s.rollbackLayer(l.layerId);
  assert.equal(s.get('Todo:1')!.title, 'x');
});

test('optimistic create under temp id, then identity merge with server id', () => {
  const s = new Store();
  s.ingest('Board', { id: 1, todos: [] });
  const l = s.beginOptimistic();
  s.writeOptimistic(l.layerId, 'Todo:tmp-1', { __typename: 'Todo', id: 'tmp-1', title: 'new' });
  s.writeOptimistic(l.layerId, 'Board:1', { todos: [{ $ref: 'Todo:tmp-1' }] });
  assert.equal((s.get('Board:1')!.todos as any)[0].$ref, 'Todo:tmp-1');
  // server assigns the real id
  s.mergeIdentities('Todo:tmp-1', 'Todo:7');
  s.commitLayer(l.layerId, { type: 'Todo', data: { id: 7, title: 'new', done: false } });
  const board = s.get('Board:1')!;
  assert.deepEqual(board.todos, [{ $ref: 'Todo:7' }], 'pending ref retargeted');
  const todo = s.get('Todo:7')!;
  assert.equal(todo.title, 'new');
  assert.equal(todo.done, false);
  assert.equal(s.get('Todo:tmp-1')!.title, 'new', 'alias resolves');
});
