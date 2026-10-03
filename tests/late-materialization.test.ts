import { test } from 'node:test';
import assert from 'node:assert/strict';

import { parseQuery } from '../src/query.ts';
import {
  executeLateMaterialization,
  executeEager,
} from '../src/late-materialization.ts';
import { buildSampleTable, NUM_COLUMNS } from '../src/sample-data.ts';

const SQL = 'SELECT * FROM t WHERE a > 10 ORDER BY b LIMIT 10';
const ROW_COUNT = 10_000;

test('parser turns the SQL into the expected plan', () => {
  const plan = parseQuery(SQL);
  assert.deepEqual(plan, {
    filter: { column: 'a', op: '>', value: 10 },
    orderBy: { column: 'b', direction: 'asc' },
    limit: 10,
  });
});

test('early phases scan ONLY columns a and b (2 of 100)', () => {
  const store = buildSampleTable(ROW_COUNT);
  const result = executeLateMaterialization(store, parseQuery(SQL));

  assert.equal(result.io.fullColumnScans, 2, 'exactly two full column scans');
  assert.deepEqual(
    new Set(result.io.scannedColumns),
    new Set(['a', 'b']),
    'only the filter column and the sort column are scanned',
  );
});

test('the 98 expensive columns are fetched only for the 10 winning rows', () => {
  const store = buildSampleTable(ROW_COUNT);
  const result = executeLateMaterialization(store, parseQuery(SQL));

  assert.equal(result.rowIds.length, 10);
  // Point lookups: 98 late columns + 2 early columns re-fetched for assembly,
  // each for exactly the 10 winners.
  assert.equal(result.io.pointLookups, 100 * 10);
  // Crucially, no expensive column was fully scanned.
  assert.ok(
    result.io.scannedColumns.every((c) => c === 'a' || c === 'b'),
    'no expensive column appears in the scan log',
  );
});

test('saves at least 90% IO versus the eager baseline', () => {
  const store = buildSampleTable(ROW_COUNT);
  const plan = parseQuery(SQL);

  const eager = executeEager(store, plan);
  const late = executeLateMaterialization(store, plan);

  assert.equal(eager.io.cellsRead, NUM_COLUMNS * ROW_COUNT);
  const saving = 1 - late.io.cellsRead / eager.io.cellsRead;
  assert.ok(
    saving >= 0.9,
    `expected >=90% IO saving, got ${(saving * 100).toFixed(2)}% ` +
      `(${late.io.cellsRead} vs ${eager.io.cellsRead} cells)`,
  );
});

test('late materialization returns exactly the same rows as eager execution', () => {
  const store = buildSampleTable(ROW_COUNT);
  const plan = parseQuery(SQL);

  const expected = executeEager(store, plan);
  const actual = executeLateMaterialization(store, plan);

  assert.deepEqual(actual.rowIds, expected.rowIds);
  assert.deepEqual(actual.rows, expected.rows);
});

test('result rows are complete: all 100 columns materialized for each winner', () => {
  const store = buildSampleTable(ROW_COUNT);
  const { rows } = executeLateMaterialization(store, parseQuery(SQL));

  assert.equal(rows.length, 10);
  for (const row of rows) {
    assert.equal(Object.keys(row).length, NUM_COLUMNS);
    assert.ok(typeof row.a === 'number' && row.a > 10);
    assert.ok(typeof row.c0 === 'string' && row.c0.startsWith('c0-row'));
  }
  // ORDER BY b ASC respected
  const bs = rows.map((r) => r.b as number);
  assert.deepEqual(bs, [...bs].sort((x, y) => x - y));
});

test('works without filter / order / limit clauses too', () => {
  const store = buildSampleTable(1_000);

  const limitOnly = executeLateMaterialization(
    store,
    parseQuery('SELECT * FROM t LIMIT 5'),
  );
  assert.equal(limitOnly.rows.length, 5);
  assert.equal(limitOnly.io.fullColumnScans, 0, 'no early columns needed at all');

  const desc = executeLateMaterialization(
    store,
    parseQuery('SELECT * FROM t WHERE a >= 90 ORDER BY b DESC LIMIT 3'),
  );
  assert.equal(desc.rows.length, 3);
  const bs = desc.rows.map((r) => r.b as number);
  assert.deepEqual(bs, [...bs].sort((x, y) => y - x));
});
