/**
 * Runnable demo: 100-column table, 10,000 rows.
 *
 *   SELECT * FROM t WHERE a > 10 ORDER BY b LIMIT 10
 *
 * Prints the IO profile of the late-materialization executor vs. the
 * eager baseline, and the resulting top-10 rows (truncated).
 */

import { parseQuery } from '../src/query.ts';
import {
  executeLateMaterialization,
  executeEager,
} from '../src/late-materialization.ts';
import { buildSampleTable, NUM_COLUMNS } from '../src/sample-data.ts';

const SQL = 'SELECT * FROM t WHERE a > 10 ORDER BY b LIMIT 10';
const ROW_COUNT = 10_000;

const store = buildSampleTable(ROW_COUNT);
const plan = parseQuery(SQL);

console.log(`Table : ${ROW_COUNT} rows x ${NUM_COLUMNS} columns (a, b, c0..c97)`);
console.log(`Query : ${SQL}\n`);

const eager = executeEager(store, plan);
const late = executeLateMaterialization(store, plan);

console.log('--- Eager (baseline: scan all 100 columns up-front) ---');
console.log(`  full column scans : ${eager.io.fullColumnScans}`);
console.log(`  cells read        : ${eager.io.cellsRead.toLocaleString()}`);

console.log('\n--- Late Materialization (this operator) ---');
console.log(`  full column scans : ${late.io.fullColumnScans}  (columns: ${late.io.scannedColumns.join(', ')})`);
console.log(`  point lookups     : ${late.io.pointLookups}  (100 cols x ${late.rowIds.length} winners)`);
console.log(`  cells read        : ${late.io.cellsRead.toLocaleString()}`);

const saving = (1 - late.io.cellsRead / eager.io.cellsRead) * 100;
console.log(`\nIO saving: ${saving.toFixed(2)}%  (${eager.io.cellsRead.toLocaleString()} -> ${late.io.cellsRead.toLocaleString()} cells)`);

console.log('\nWinning physical row ids:', late.rowIds.join(', '));
console.log('\nTop-10 rows (showing a, b, c0 only; all 100 columns materialized):');
for (const row of late.rows) {
  console.log(`  a=${String(row.a).padStart(2)}  b=${row.b}  c0=${(row.c0 as string).slice(0, 24)}...`);
}
