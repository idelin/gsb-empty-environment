import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { batchOf, makeStore, upsert } from './helpers.ts';

test('duplicate events across batches are not double counted', () => {
  const store = makeStore();
  const event = upsert({ source: 'hr', entity: 'p', parent: null, eventId: 'dup-1' });
  store.applyBatch(batchOf(event));
  store.applyBatch(batchOf(event)); // same event id in a later batch
  assert.equal(store.counters.applied, 1);
  assert.equal(store.counters.duplicates, 1);
  assert.equal(store.nodes.get('p')!.subtreeSize, 1);
  assert.equal(store.nodes.size, 1);
});

test('old snapshot restores the old source view', () => {
  const store = makeStore();
  const b1 = batchOf(upsert({ source: 'erp', entity: 'e1', fields: { name: 'old-name' } }));
  const b2 = batchOf(upsert({ source: 'hr', entity: 'e1', fields: { name: 'new-name' } }));
  store.applyBatch(b1);
  store.applyBatch(b2);
  let lineage = store.lineage('e1') as any;
  assert.equal(lineage.name.effective.value, 'new-name');

  assert.equal(store.restore(b1.id), true);
  lineage = store.lineage('e1') as any;
  assert.equal(lineage.name.effective.value, 'old-name');
  assert.equal(lineage.name.effective.source, 'erp');
  assert.equal(lineage.name.contributions.length, 1); // hr contribution not yet known

  assert.equal(store.restore(b2.id), true);
  lineage = store.lineage('e1') as any;
  assert.equal(lineage.name.effective.value, 'new-name');
  assert.equal(store.restore('no-such-batch'), false);
});

test('errors and lineage locate concrete JSON paths', () => {
  const store = makeStore();
  store.applyBatch(batchOf(
    upsert({ source: 'hr', entity: 'a', parent: null, eventId: 'p1' }),
    upsert({ source: 'hr', entity: 'b', parent: 'a', eventId: 'p2' }),
  ));
  store.applyBatch(batchOf(
    upsert({ source: 'hr', entity: 'a', version: 2, parent: 'b', eventId: 'bad' }),
  ));
  assert.equal(store.errors.length, 1);
  assert.match(store.errors[0].path, /^\$\.test\.events\[\d+\]\.parent$/);
  const lineage = store.lineage('a') as any;
  assert.match(lineage._parent.contributions[0].path, /^\$\.test\.events\[\d+\]\.parent$/);
});

test('stale upsert after delete is blocked by tombstone', () => {
  const store = makeStore();
  store.applyBatch(batchOf(
    upsert({ source: 'hr', entity: 'e1', version: 5, fields: { name: 'x' } }),
  ));
  store.applyBatch(batchOf(
    { kind: 'delete', eventId: 'del-1', source: 'hr', table: 't', entity: 'e1', version: 6, path: '$.t' },
  ));
  assert.equal(store.nodes.has('e1'), false);
  store.applyBatch(batchOf(
    upsert({ source: 'hr', entity: 'e1', version: 4, fields: { name: 'zombie' } }),
  ));
  assert.equal(store.nodes.has('e1'), false); // version 4 < tombstone 6
  store.applyBatch(batchOf(
    upsert({ source: 'hr', entity: 'e1', version: 7, fields: { name: 'revived' } }),
  ));
  assert.equal(store.nodes.has('e1'), true);
});
