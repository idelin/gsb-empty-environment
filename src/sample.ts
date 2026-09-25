// Replayable end-to-end scenario. Run with: npm run sample
//
// Demonstrates, in order:
//  b1  multi-table upserts, child-before-parent (orphan queue),
//      same-id name conflict at equal priority -> PENDING (never arbitrary);
//  b2  missing parent arrives (orphan attaches), conflict resolved by a
//      higher-priority source, cross-batch duplicate event ignored;
//  b3a moving A (dept-2) under its own descendant B (dept-9) -> CYCLE,
//      rejected atomically, aggregates unchanged;
//  b3b legitimate parent correction moves the dept-9 subtree, and only the
//      affected ancestor chains are updated (lastTouched);
//  b4  parent deleted (children preserved per policy, no cascade), then the
//      parent is restored from another source and children reattach with
//      their identity intact;
//  b5  restoring an old snapshot brings back the old source view;
//  finally the incremental state is verified against a full rebuild and an
//  independent aggregate recomputation.

import { strict as assert } from 'node:assert';
import type { Batch } from './core.ts';
import { canonical, rebuild, statsMismatches } from './rebuild.ts';
import { DELETE_POLICY, Store } from './store.ts';

const sources = [
  { id: 'erp', priority: 10 },
  { id: 'crm', priority: 20 },
  { id: 'web', priority: 20 },
  { id: 'hr', priority: 30 },
];

const batches: Batch[] = [
  {
    id: 'b1',
    events: [
      { kind: 'upsert', eventId: 'e1', source: 'hr', table: 'departments', entity: 'dept-1', version: 1, parent: null, fields: { name: 'Engineering' }, path: '$.batches[0].events[0]' },
      { kind: 'upsert', eventId: 'e2', source: 'crm', table: 'employees', entity: 'emp-1', version: 1, parent: 'dept-9', fields: { name: 'Alice' }, path: '$.batches[0].events[1]' },
      { kind: 'upsert', eventId: 'e3', source: 'crm', table: 'departments', entity: 'dept-2', version: 1, parent: 'dept-1', fields: { name: 'Platform' }, path: '$.batches[0].events[2]' },
      { kind: 'upsert', eventId: 'e4', source: 'web', table: 'employees', entity: 'emp-1', version: 1, fields: { name: 'Alicia' }, path: '$.batches[0].events[3]' },
      { kind: 'upsert', eventId: 'e5', source: 'erp', table: 'employees', entity: 'emp-1', version: 1, fields: { name: 'Alice L.' }, path: '$.batches[0].events[4]' },
    ],
  },
  {
    id: 'b2',
    events: [
      { kind: 'upsert', eventId: 'e6', source: 'hr', table: 'departments', entity: 'dept-9', version: 1, parent: 'dept-2', fields: { name: 'Infra' }, path: '$.batches[1].events[0]' },
      { kind: 'upsert', eventId: 'e7', source: 'hr', table: 'employees', entity: 'emp-1', version: 1, fields: { name: 'Alice' }, path: '$.batches[1].events[1]' },
      { kind: 'upsert', eventId: 'e2', source: 'crm', table: 'employees', entity: 'emp-1', version: 1, parent: 'dept-9', fields: { name: 'Alice' }, path: '$.batches[1].events[2]' },
    ],
  },
  {
    id: 'b3a',
    events: [
      { kind: 'upsert', eventId: 'e8', source: 'hr', table: 'departments', entity: 'dept-2', version: 2, parent: 'dept-9', path: '$.batches[2].events[0]' },
    ],
  },
  {
    id: 'b3b',
    events: [
      { kind: 'upsert', eventId: 'e9', source: 'hr', table: 'departments', entity: 'dept-9', version: 2, parent: 'dept-1', path: '$.batches[3].events[0]' },
    ],
  },
  {
    id: 'b4',
    events: [
      { kind: 'delete', eventId: 'e10', source: 'hr', table: 'departments', entity: 'dept-9', version: 3, path: '$.batches[4].events[0]' },
      { kind: 'upsert', eventId: 'e11', source: 'crm', table: 'departments', entity: 'dept-9', version: 1, parent: 'dept-2', fields: { name: 'Infra (CRM)' }, path: '$.batches[4].events[1]' },
    ],
  },
];

function show(label: string, value: unknown): void {
  console.log(`\n== ${label}`);
  console.log(JSON.stringify(value, null, 2));
}

const store = new Store({ sources, onParentDelete: DELETE_POLICY });

store.applyBatch(batches[0]);
show('b1: orphan queue (emp-1 arrived before dept-9)', store.orphans);
show('b1: emp-1 name is PENDING (crm vs web, equal priority)', store.lineage('emp-1'));
assert.deepEqual(store.orphans, ['emp-1']);
assert.equal(store.nodes.get('emp-1')!.conflict, true);
assert.equal(store.nodes.get('dept-1')!.subtreeConflicts, 0);

store.applyBatch(batches[1]);
show('b2: orphans attached, counters (duplicate e2 not double counted)', {
  orphans: store.orphans,
  counters: store.counters,
  emp1Name: store.lineage('emp-1'),
});
assert.deepEqual(store.orphans, []);
assert.equal(store.counters.duplicates, 1);
assert.equal(store.nodes.get('dept-1')!.subtreeSize, 4);

const statsBeforeCycle = JSON.stringify(
  [...store.nodes.values()].map((n) => [n.id, n.subtreeSize, n.subtreeConflicts, n.parent]),
);
store.applyBatch(batches[2]);
const statsAfterCycle = JSON.stringify(
  [...store.nodes.values()].map((n) => [n.id, n.subtreeSize, n.subtreeConflicts, n.parent]),
);
show('b3a: cycle rejected atomically, stats unchanged', {
  errors: store.errors,
  statsUnchanged: statsBeforeCycle === statsAfterCycle,
});
assert.equal(statsBeforeCycle, statsAfterCycle);
assert.equal(store.errors.at(-1)!.code, 'CYCLE');
assert.equal(store.errors.at(-1)!.path, '$.batches[2].events[0].parent');

store.applyBatch(batches[3]);
show('b3b: subtree moved; only affected ancestor chains touched', {
  touched: [...store.lastTouched].sort(),
  dept1: store.nodes.get('dept-1')!.subtreeSize,
  dept2: store.nodes.get('dept-2')!.subtreeSize,
});
assert.deepEqual([...store.lastTouched].sort(), ['dept-1', 'dept-2', 'dept-9']);
assert.equal(store.nodes.get('dept-2')!.subtreeSize, 1);
assert.equal(store.nodes.get('dept-1')!.subtreeSize, 4);

store.applyBatch(batches[4]);
show('b4: dept-9 deleted (hr) then restored (crm); emp-1 identity preserved', {
  orphansAfterDeleteRestore: store.orphans,
  emp1Parent: store.nodes.get('emp-1')!.parent,
  emp1Name: store.lineage('emp-1'),
  dept9Name: store.lineage('dept-9'),
});
assert.deepEqual(store.orphans, []);
assert.equal(store.nodes.get('emp-1')!.parent, 'dept-9');

assert.equal(store.restore('b1'), true);
show('b5: snapshot b1 restored, old source view is back', {
  orphans: store.orphans,
  emp1Name: store.lineage('emp-1'),
  dept9: store.nodes.has('dept-9'),
});
assert.deepEqual(store.orphans, ['emp-1']);
assert.equal(store.nodes.has('dept-9'), false);
assert.equal(store.nodes.get('emp-1')!.conflict, true);

assert.equal(store.restore('b4'), true);
const rebuilt = rebuild(sources, store.batches);
assert.deepEqual(canonical(store), canonical(rebuilt));
assert.deepEqual(statsMismatches(store), []);
show('verify: incremental == full rebuild, aggregates consistent', {
  rebuildEqual: true,
  statsMismatches: statsMismatches(store),
  counters: store.counters,
});

console.log('\nREPLAY OK');
