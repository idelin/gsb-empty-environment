import { execute } from '../src/executor.ts';
import { referenceExecute } from '../src/reference.ts';
import { BatchSource } from '../src/source.ts';
import type { SourceEvent, SourceSpec } from '../src/source.ts';
import type { QueryPlan } from '../src/types.ts';
import assert from 'node:assert/strict';

const spec: SourceSpec = {
  narrowColumns: ['age', 'name'],
  wideColumns: ['bio', 'blob'],
  batches: [
    {
      dataVersion: 1,
      rowIds: [101, 102, 103, 104],
      rowVersions: [1, 1, 2, 1],
      columns: {
        age: [17, 34, null, 62],
        name: ['amy', 'bob', 'cy', 'di'],
        bio: ['likes cats', 'climbs', 'quiet', 'retired'],
        blob: ['<1MB>', '<2MB>', '<1MB>', '<3MB>'],
      },
    },
    {
      dataVersion: 1,
      rowIds: [105, 102, 107],
      rowVersions: [1, 3, 1],
      columns: {
        age: [25, 41, 19],
        name: ['el', 'bob', 'gus'],
        bio: ['runs', 'climbs harder', 'student'],
        blob: ['<4MB>', '<2MB>', '<1MB>'],
      },
    },
    {
      dataVersion: 2,
      rowIds: [108, 109],
      rowVersions: [1, 1],
      columns: {
        age: [34, null],
        name: ['ada', 'max'],
        bio: ['engineer', 'unknown'],
        blob: ['<1MB>', '<2MB>'],
      },
    },
  ],
};

const events: SourceEvent[] = [];
const source = new BatchSource(spec, { poolSize: 2, onEvent: (e) => events.push(e) });

console.log('=== physical layout (per batch) ===');
for (const [i, b] of spec.batches.entries()) {
  console.log(
    `batch ${i}: rows=${b.rowIds.length} dataVersion=${b.dataVersion} ` +
      `rowIds=[${b.rowIds}] rowVersions=[${b.rowVersions}]`,
  );
  for (const name of spec.narrowColumns) {
    const values = b.columns[name];
    const bits = values.map((v) => (v === null ? '0' : '1')).join('');
    console.log(`  narrow ${name.padEnd(5)} validity=${bits} values=[${values}]`);
  }
  console.log(`  wide   ${spec.wideColumns.join(', ')} -> stay in storage, loaded on demand`);
}

const plan: QueryPlan = {
  filter: { column: 'age', op: '>=', value: 18 },
  sort: { column: 'age' },
  project: ['name', 'bio'],
};
console.log('\n=== plan ===');
console.log(JSON.stringify(plan));

const result = await execute(source, plan);

console.log('\n=== batch lifecycle / load events ===');
for (const e of events) {
  if (e.kind === 'acquire') {
    console.log(`acquire  slot=${e.slotId} batch=${e.batchId} generation=${e.generation}`);
  } else if (e.kind === 'release') {
    console.log(`release  slot=${e.slotId} batch=${e.batchId}`);
  } else {
    console.log(`load     column=${e.column} batch=${e.batchId} rows=${e.rows}`);
  }
}

console.log('\n=== result rows (filter -> stable sort -> late projection) ===');
for (const row of result.rows) {
  console.log(`rowId=${row.rowId} v${row.version}`, row.values);
}

console.log('\n=== wide-column load statistics ===');
for (const [column, s] of Object.entries(result.stats)) {
  console.log(`${column}: calls=${s.calls} rowsLoaded=${s.rowsLoaded}`);
}
console.log(`blob: calls=${source.stats.loadCount('blob')} (unprojected wide column, never read)`);

const expected = referenceExecute(spec, plan);
assert.deepEqual(result.rows, expected);
console.log('\npipeline output matches the eager row-by-row reference, value by value.');
