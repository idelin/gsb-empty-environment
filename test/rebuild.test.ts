import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import type { Batch } from '../src/core.ts';
import { canonical, rebuild, statsMismatches } from '../src/rebuild.ts';
import { SOURCES, batchOf, del, makeStore, upsert } from './helpers.ts';

function scenarioBatches(): Batch[] {
  return [
    batchOf(
      upsert({ source: 'hr', entity: 'root', parent: null, fields: { name: 'Root' } }),
      upsert({ source: 'crm', entity: 'emp-1', parent: 'dept-x', fields: { name: 'Ann' } }), // orphan
      upsert({ source: 'web', entity: 'emp-1', fields: { name: 'Anne' } }), // pending conflict
      upsert({ source: 'crm', entity: 'dept-x', parent: 'root', fields: { name: 'X' } }), // parent arrives
    ),
    batchOf(
      upsert({ source: 'hr', entity: 'emp-1', fields: { name: 'Ann H.' } }), // resolve by level
      upsert({ source: 'hr', entity: 'dept-y', parent: 'dept-x' }),
      upsert({ source: 'hr', entity: 'emp-2', parent: 'dept-y' }),
    ),
    batchOf(
      upsert({ source: 'hr', entity: 'dept-x', version: 2, parent: 'emp-2', eventId: 'cyc' }), // cycle
    ),
    batchOf(
      upsert({ source: 'hr', entity: 'dept-y', version: 2, parent: 'root' }), // legit move
      del({ source: 'crm', entity: 'dept-x', version: 2 }), // dept-x survives? crm was only source -> removed
      upsert({ source: 'web', entity: 'dept-x', parent: 'root', fields: { name: 'X2' } }), // restored
    ),
  ];
}

test('incremental state equals full rebuild from the event log', () => {
  const store = makeStore();
  const batches = scenarioBatches();
  for (const b of batches) store.applyBatch(b);
  const rebuilt = rebuild(SOURCES, store.batches);
  assert.deepEqual(canonical(store), canonical(rebuilt));
});

test('stored aggregates match independent recomputation at every step', () => {
  const store = makeStore();
  for (const b of scenarioBatches()) {
    store.applyBatch(b);
    assert.deepEqual(statsMismatches(store), [], `after batch ${b.id}`);
  }
});

test('only affected ancestor chains are updated on a deep move', () => {
  const store = makeStore();
  store.applyBatch(batchOf(
    upsert({ source: 'hr', entity: 'r', parent: null }),
    upsert({ source: 'hr', entity: 'a', parent: 'r' }),
    upsert({ source: 'hr', entity: 'a1', parent: 'a' }),
    upsert({ source: 'hr', entity: 'b', parent: 'r' }),
    upsert({ source: 'hr', entity: 'b1', parent: 'b' }),
    upsert({ source: 'hr', entity: 'leaf', parent: 'b1' }),
  ));
  store.applyBatch(batchOf(upsert({ source: 'hr', entity: 'b1', version: 2, parent: 'a1' })));
  // affected: b1 (moved), old chain (b, r), new chain (a1, a, r)
  assert.deepEqual([...store.lastTouched].sort(), ['a', 'a1', 'b', 'b1', 'r']);
  assert.equal(store.nodes.get('leaf')!.subtreeSize, 1);
  assert.equal(store.nodes.get('r')!.subtreeSize, 6);
  assert.equal(store.nodes.get('b')!.subtreeSize, 1);
  assert.equal(store.nodes.get('a1')!.subtreeSize, 3);
});

test('rebuild matches even after snapshot restore to latest batch', () => {
  const store = makeStore();
  const batches = scenarioBatches();
  for (const b of batches) store.applyBatch(b);
  const firstBatchId = store.batches[0].id;
  const lastBatchId = store.batches.at(-1)!.id;
  assert.equal(store.restore(firstBatchId), true);
  assert.equal(store.restore(lastBatchId), true); // back to latest
  const rebuilt = rebuild(SOURCES, store.batches);
  assert.deepEqual(canonical(store), canonical(rebuilt));
});
