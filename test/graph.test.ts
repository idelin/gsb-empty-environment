import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { batchOf, del, makeStore, upsert } from './helpers.ts';

test('child may arrive before parent: orphan queue, then automatic attach', () => {
  const store = makeStore();
  store.applyBatch(batchOf(
    upsert({ source: 'crm', entity: 'child', parent: 'root', fields: { name: 'c' } }),
  ));
  assert.deepEqual(store.orphans, ['child']);
  assert.equal(store.nodes.get('child')!.parent, 'root'); // claim kept
  assert.equal(store.nodes.get('root'), undefined);

  store.applyBatch(batchOf(upsert({ source: 'hr', entity: 'root', parent: null })));
  assert.deepEqual(store.orphans, []);
  assert.deepEqual([...store.nodes.get('root')!.children], ['child']);
  assert.equal(store.nodes.get('root')!.subtreeSize, 2);
});

test('parent correction moves subtree; only affected ancestor chains are updated', () => {
  const store = makeStore();
  store.applyBatch(batchOf(
    upsert({ source: 'hr', entity: 'r', parent: null }),
    upsert({ source: 'hr', entity: 'a', parent: 'r' }),
    upsert({ source: 'hr', entity: 'b', parent: 'a' }),
    upsert({ source: 'hr', entity: 'c', parent: 'b' }),
    upsert({ source: 'hr', entity: 'd', parent: 'r' }),
  ));
  assert.equal(store.nodes.get('r')!.subtreeSize, 5);
  store.applyBatch(batchOf(upsert({ source: 'hr', entity: 'b', version: 2, parent: 'd' })));
  assert.equal(store.nodes.get('a')!.subtreeSize, 1);
  assert.equal(store.nodes.get('d')!.subtreeSize, 3); // d, b, c
  assert.equal(store.nodes.get('r')!.subtreeSize, 5); // unchanged in total
  assert.equal(store.nodes.get('b')!.parent, 'd');
  assert.deepEqual([...store.lastTouched].sort(), ['a', 'b', 'd', 'r']);
});

test('moving A under its descendant B is rejected atomically; stats unchanged', () => {
  const store = makeStore();
  store.applyBatch(batchOf(
    upsert({ source: 'hr', entity: 'a', parent: null }),
    upsert({ source: 'hr', entity: 'b', parent: 'a' }),
    upsert({ source: 'hr', entity: 'c', parent: 'b' }),
  ));
  const before = JSON.stringify(
    [...store.nodes.values()].map((n) => [n.id, n.parent, n.subtreeSize, [...n.children].sort()]),
  );
  store.applyBatch(batchOf(
    upsert({ source: 'hr', entity: 'a', version: 2, parent: 'c', eventId: 'cycle-1' }),
  ));
  const after = JSON.stringify(
    [...store.nodes.values()].map((n) => [n.id, n.parent, n.subtreeSize, [...n.children].sort()]),
  );
  assert.equal(before, after);
  assert.equal(store.errors.length, 1);
  assert.equal(store.errors[0].code, 'CYCLE');
  assert.equal(store.errors[0].eventId, 'cycle-1');
  assert.ok(store.errors[0].path.endsWith('.parent'));
  assert.equal(store.counters.rejected, 1);
});

test('self-parent is rejected as a cycle', () => {
  const store = makeStore();
  store.applyBatch(batchOf(upsert({ source: 'hr', entity: 'x', parent: 'x' })));
  assert.equal(store.errors[0].code, 'CYCLE');
  assert.equal(store.nodes.has('x'), false); // atomic: entity not created either
});

test('deleting a parent preserves children per policy; restore keeps child identity', () => {
  const store = makeStore();
  store.applyBatch(batchOf(
    upsert({ source: 'hr', entity: 'dept', parent: null, fields: { name: 'D' } }),
    upsert({ source: 'crm', entity: 'emp', parent: 'dept', fields: { name: 'E' } }),
  ));
  assert.equal(store.nodes.get('dept')!.subtreeSize, 2);

  store.applyBatch(batchOf(del({ source: 'hr', entity: 'dept', version: 2 })));
  assert.equal(store.nodes.has('dept'), false);
  assert.deepEqual(store.orphans, ['emp']); // preserved, not cascaded
  assert.equal(store.nodes.get('emp')!.parent, 'dept'); // claim kept

  store.applyBatch(batchOf(
    upsert({ source: 'web', entity: 'dept', parent: null, fields: { name: 'D2' } }),
  ));
  assert.deepEqual(store.orphans, []);
  assert.equal(store.nodes.get('emp')!.parent, 'dept');
  assert.deepEqual([...store.nodes.get('dept')!.children], ['emp']);
  assert.equal(store.nodes.get('dept')!.subtreeSize, 2);
  const lineage = store.lineage('emp') as any;
  assert.equal(lineage.name.effective.value, 'E'); // child fields untouched
});

test('delete from one source falls back to remaining sources (no entity loss)', () => {
  const store = makeStore();
  store.applyBatch(batchOf(
    upsert({ source: 'hr', entity: 'e1', parent: null, fields: { name: 'hr-name' } }),
    upsert({ source: 'erp', entity: 'e1', fields: { name: 'erp-name' } }),
  ));
  store.applyBatch(batchOf(del({ source: 'hr', entity: 'e1', version: 2 })));
  const lineage = store.lineage('e1') as any;
  assert.equal(lineage.name.effective.value, 'erp-name');
  assert.equal(store.nodes.has('e1'), true);
});
