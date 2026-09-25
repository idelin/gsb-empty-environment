import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { jsonEquals } from '../src/core.ts';
import { batchOf, makeStore, upsert } from './helpers.ts';

test('higher-priority source wins over lower-priority source', () => {
  const store = makeStore();
  store.applyBatch(batchOf(
    upsert({ source: 'erp', entity: 'e1', fields: { name: 'from-erp' } }),
    upsert({ source: 'hr', entity: 'e1', fields: { name: 'from-hr' } }),
  ));
  const lineage = store.lineage('e1') as any;
  assert.equal(lineage.name.effective.value, 'from-hr');
  assert.equal(lineage.name.effective.source, 'hr');
  assert.equal(lineage.name.contributions.length, 2); // both kept for lineage
});

test('same id, two sources, different names at equal priority -> pending, never arbitrary', () => {
  const store = makeStore();
  store.applyBatch(batchOf(
    upsert({ source: 'crm', entity: 'e1', fields: { name: 'Alice' } }),
    upsert({ source: 'web', entity: 'e1', fields: { name: 'Alicia' } }),
  ));
  const lineage = store.lineage('e1') as any;
  assert.equal(lineage.name.effective, null);
  assert.equal(lineage.name.pending.length, 2);
  assert.equal(store.nodes.get('e1')!.conflict, true);
  // pending candidates carry source paths for locating the conflict
  const paths = lineage.name.pending.map((c: any) => c.path).sort();
  assert.ok(paths[0].startsWith('$.test.events['));
});

test('pending conflict is resolved when a higher-level source arrives', () => {
  const store = makeStore();
  store.applyBatch(batchOf(
    upsert({ source: 'crm', entity: 'e1', fields: { name: 'Alice' } }),
    upsert({ source: 'web', entity: 'e1', fields: { name: 'Alicia' } }),
  ));
  store.applyBatch(batchOf(upsert({ source: 'hr', entity: 'e1', fields: { name: 'Alice H.' } })));
  const lineage = store.lineage('e1') as any;
  assert.equal(lineage.name.effective.value, 'Alice H.');
  assert.equal(lineage.name.pending, null);
  assert.equal(store.nodes.get('e1')!.conflict, false);
});

test('newer version from same source replaces; stale version ignored', () => {
  const store = makeStore();
  store.applyBatch(batchOf(
    upsert({ source: 'crm', entity: 'e1', version: 2, fields: { name: 'v2' } }),
    upsert({ source: 'crm', entity: 'e1', version: 1, fields: { name: 'v1-stale' } }),
  ));
  let lineage = store.lineage('e1') as any;
  assert.equal(lineage.name.effective.value, 'v2');
  store.applyBatch(batchOf(upsert({ source: 'crm', entity: 'e1', version: 3, fields: { name: 'v3' } })));
  lineage = store.lineage('e1') as any;
  assert.equal(lineage.name.effective.value, 'v3');
  assert.equal(lineage.name.effective.version, 3);
});

test('object key order is insignificant, array order is significant', () => {
  assert.equal(jsonEquals({ a: 1, b: [1, 2] }, { b: [1, 2], a: 1 }), true);
  assert.equal(jsonEquals([1, 2], [2, 1]), false);
  const store = makeStore();
  store.applyBatch(batchOf(
    upsert({ source: 'crm', entity: 'e1', fields: { addr: { city: 'SH', zip: '200' } } }),
    upsert({ source: 'web', entity: 'e1', fields: { addr: { zip: '200', city: 'SH' } } }),
  ));
  let lineage = store.lineage('e1') as any;
  assert.equal(lineage.addr.pending, null); // same value, different key order: no conflict
  assert.equal(lineage.addr.effective.source, 'crm');
  store.applyBatch(batchOf(
    upsert({ source: 'crm', entity: 'e2', fields: { tags: ['a', 'b'] } }),
    upsert({ source: 'web', entity: 'e2', fields: { tags: ['b', 'a'] } }),
  ));
  lineage = store.lineage('e2') as any;
  assert.equal(lineage.tags.pending.length, 2); // array order matters: conflict
});
