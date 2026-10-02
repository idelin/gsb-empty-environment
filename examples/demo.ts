import { Store } from '../src/index.ts';

const show = (label: string, value: unknown) =>
  console.log(`\n=== ${label} ===\n`, JSON.stringify(value, null, 2));

const store = new Store();

// 1. Normalize a nested response into entity tables; references are shared.
store.ingest('Post', [
  { __typename: 'Post', id: 1, title: 'Hello', author: { __typename: 'User', id: 1, name: 'Ada' } },
  { __typename: 'Post', id: 2, title: 'World', author: { __typename: 'User', id: 1, name: 'Ada' } },
]);
show('1. entity tables + reverse index after ingest', store.snapshot().toJSON());

// 2. Two parents share User:1 — one update is visible through both.
store.update('User:1', { name: 'Grace' });
show('2. both posts see the author rename', {
  post1: store.denormalize('Post:1'),
  post2: store.denormalize('Post:2'),
});

// 3. Partial update: missing fields survive, explicit null is stored.
store.update('Post:1', { subtitle: null });
show('3. partial update keeps title, stores explicit null', store.get('Post:1'));

// 4. Paginated relation: two pages appended, duplicate ref deduped.
store.ingest('Feed', { id: 'home', label: 'Home' });
store.appendConnection('Feed:home', 'posts',
  [{ __typename: 'Post', id: 1 }, { __typename: 'Post', id: 2 }],
  { endCursor: 'c1', hasNextPage: true });
store.appendConnection('Feed:home', 'posts',
  [{ __typename: 'Post', id: 3, title: 'Third' }, { __typename: 'Post', id: 1 }],
  { endCursor: 'c2', hasNextPage: false });
show('4. feed connection after two pages', store.get('Feed:home'));

// 5. Optimistic overlay + stale server ack.
store.ingest('Todo', { id: 1, title: 'buy milk' }, { version: 10 });
const editA = store.beginOptimistic();
store.writeOptimistic(editA.layerId, 'Todo:1', { title: 'buy oat milk' });
const editB = store.beginOptimistic();
store.writeOptimistic(editB.layerId, 'Todo:1', { title: 'buy oat milk + coffee' });
console.log('\n=== 5. optimistic stack, readers see the newest edit ===');
console.log(' ', store.get('Todo:1')!.title);
store.commitLayer(editB.layerId, { type: 'Todo', data: { id: 1, title: 'buy oat milk + coffee' } });
store.commitLayer(editA.layerId, { type: 'Todo', data: { id: 1, title: 'buy oat milk' } }); // stale ack
console.log('   after stale ack for the older edit arrives:');
console.log(' ', store.get('Todo:1')!.title, ' <- newer user version wins');

// 6. Rollback only undoes the layer's own changes.
const l1 = store.beginOptimistic();
store.writeOptimistic(l1.layerId, 'Todo:1', { done: true });
const l2 = store.beginOptimistic();
store.writeOptimistic(l2.layerId, 'Todo:1', { priority: 'high' });
store.rollbackLayer(l1.layerId);
show('6. rollback of the done-edit keeps the priority-edit', store.get('Todo:1'));
store.rollbackLayer(l2.layerId);

// 7. Optimistic create with temp id, then identity merge with the server id.
store.ingest('Board', { id: 1, todos: [] });
const create = store.beginOptimistic();
store.writeOptimistic(create.layerId, 'Todo:tmp-1', { __typename: 'Todo', id: 'tmp-1', title: 'new task' });
store.writeOptimistic(create.layerId, 'Board:1', { todos: [{ $ref: 'Todo:tmp-1' }] });
store.mergeIdentities('Todo:tmp-1', 'Todo:42');
store.commitLayer(create.layerId, { type: 'Todo', data: { id: 42, title: 'new task', done: false } });
show('7. identity merge retargeted the board, alias resolves', {
  board: store.get('Board:1'),
  viaAlias: store.get('Todo:tmp-1'),
});

// 8. Delete with a reference cycle + live query lease.
store.ingest('Node', { id: 'x', friend: { __typename: 'Node', id: 'y' } });
store.ingest('Node', { id: 'y', friend: { __typename: 'Node', id: 'x' } });
console.log('\n=== 8. delete with reference cycle + live query lease ===');
const feed = store.query((read) => read.keysOfType('Node'));
feed.subscribe((r) => console.log('   live query (lease on Node:*) now sees:', r));
store.tx((t) => {
  t.delete('Node:x');
  t.delete('Node:y');
});
console.log('   Node:x / Node:y entries after cyclic delete:', (store.snapshot().toJSON() as any).reverse['Node:x'], (store.snapshot().toJSON() as any).reverse['Node:y']);
feed.dispose();

// 9. Snapshot isolation: readers never see a transaction's intermediate state.
const before = store.snapshot();
store.tx((t) => {
  t.update('Todo:1', { title: 'intermediate' });
  console.log('\n=== 9. isolation ===');
  console.log('   committed read inside tx:', store.get('Todo:1')!.title);
  console.log('   tx read-your-writes:     ', t.get('Todo:1')!.title);
  t.update('Todo:1', { title: 'final' });
});
console.log('   after commit:            ', store.get('Todo:1')!.title);
console.log('   pre-tx snapshot still:   ', before.get('Todo:1')!.title);

// 10. Replay the op journal into a fresh store -> identical state.
const replayed = Store.replay(store.getJournal());
const a = JSON.stringify(store.snapshot().toJSON());
const b = JSON.stringify(replayed.snapshot().toJSON());
console.log('\n=== 10. journal replay ===');
console.log(`   ${store.getJournal().length} ops replayed, states identical: ${a === b}`);
