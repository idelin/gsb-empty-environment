import assert from 'node:assert/strict';
import { PivotTable, stableSort, compareLabels } from '../build/index.js';

let passed = 0;
let failed = 0;
const failures = [];

function test(name, fn) {
  try {
    fn();
    passed += 1;
    console.log('ok   - ' + name);
  } catch (err) {
    failed += 1;
    failures.push(name);
    console.log('FAIL - ' + name);
    console.log('       ' + String(err && err.message).split('\n').join('\n       '));
  }
}

function closeTo(actual, expected, eps = 1e-9) {
  assert.ok(typeof actual === 'number', 'expected a number, got ' + String(actual));
  assert.ok(Math.abs(actual - expected) < eps, actual + ' != ' + expected);
}

function makeBasic() {
  const pivot = new PivotTable({
    rows: ['region', 'city'],
    columns: ['year', 'quarter'],
    measures: [{ field: 'sales', aggregation: 'sum' }],
  });
  pivot.addRecords([
    { id: 'r1', region: 'East', city: 'Hangzhou', year: '2024', quarter: 'Q1', sales: 10 },
    { id: 'r2', region: 'East', city: 'Hangzhou', year: '2024', quarter: 'Q2', sales: 20 },
    { id: 'r3', region: 'East', city: 'Ningbo', year: '2024', quarter: 'Q1', sales: 5 },
    { id: 'r4', region: 'West', city: 'Chengdu', year: '2023', quarter: 'Q1', sales: 7 },
  ]);
  return pivot;
}

test('multi-level rows/columns with subtotals and grand total', () => {
  const result = makeBasic().getResult();
  assert.deepEqual(
    result.columns.map((c) => [c.path.join('/'), c.isSubtotal, c.isGrandTotal]),
    [
      ['2024/Q1', false, false],
      ['2024/Q2', false, false],
      ['2024', true, false],
      ['2023/Q1', false, false],
      ['2023', true, false],
      ['总计', false, true],
    ],
  );
  assert.equal(result.rows.length, 6);
  const rowCells = result.rows.map((r) => r.cells.map((c) => c[0]));
  assert.deepEqual(rowCells, [
    [10, 20, 30, null, null, 30],
    [5, null, 5, null, null, 5],
    [15, 20, 35, null, null, 35],
    [null, null, null, 7, 7, 7],
    [null, null, null, 7, 7, 7],
    [15, 20, 35, 7, 7, 42],
  ]);
  assert.deepEqual(result.grandTotal, [42]);
});

test('sparse headers merge repeated group labels', () => {
  const result = makeBasic().getResult();
  assert.deepEqual(
    result.rows.map((r) => r.headers),
    [
      ['East', 'Hangzhou'],
      [null, 'Ningbo'],
      ['East 小计', null],
      ['West', 'Chengdu'],
      ['West 小计', null],
      ['总计', null],
    ],
  );
  assert.ok(result.rows[2].isSubtotal);
  assert.ok(result.rows[5].isGrandTotal);
});

test('retracting a record updates aggregates and prunes empty paths', () => {
  const pivot = makeBasic();
  assert.equal(pivot.removeRecord('r3'), true);
  let result = pivot.getResult();
  assert.deepEqual(result.rows.map((r) => r.path.join('/')), [
    'East/Hangzhou',
    'East',
    'West/Chengdu',
    'West',
    '',
  ]);
  assert.deepEqual(result.rows[1].cells.map((c) => c[0]), [10, 20, 30, null, null, 30]);
  assert.deepEqual(result.grandTotal, [37]);
  assert.equal(pivot.removeRecord('missing'), false);
  pivot.removeRecord('r1');
  pivot.removeRecord('r2');
  pivot.removeRecord('r4');
  result = pivot.getResult();
  assert.equal(result.rows.length, 1);
  assert.ok(result.rows[0].isGrandTotal);
  assert.deepEqual(result.columns.map((c) => c.path.join('/')), ['总计']);
  assert.deepEqual(result.rows[0].cells.map((c) => c[0]), [null]);
  const stats = pivot.debugStats();
  assert.equal(stats.records, 0);
  assert.equal(stats.rowNodes, 1);
  assert.equal(stats.colNodes, 1);
  assert.equal(stats.aggNodes, 0);
});

test('duplicate records aggregate together and retract individually', () => {
  const pivot = new PivotTable({
    rows: ['g'],
    columns: [],
    measures: [
      { field: 'v', aggregation: 'sum' },
      { field: 'v', aggregation: 'count' },
    ],
  });
  pivot.addRecord({ id: 1, g: 'A', v: 10 });
  pivot.addRecord({ id: 2, g: 'A', v: 20 });
  let result = pivot.getResult();
  const totalCol = result.columns.length - 1;
  assert.deepEqual(result.rows[0].cells[totalCol], [30, 2]);
  pivot.removeRecord(1);
  result = pivot.getResult();
  assert.deepEqual(result.rows[0].cells[totalCol], [20, 1]);
  assert.deepEqual(result.rows.map((r) => r.path.join('/')), ['A', '']);
  pivot.removeRecord(2);
  result = pivot.getResult();
  assert.deepEqual(result.rows.map((r) => r.path.join('/')), ['']);
});

test('null dimension values use a dedicated blank label', () => {
  const pivot = new PivotTable({
    rows: ['region', 'city'],
    columns: ['year'],
    measures: [{ field: 'v', aggregation: 'sum' }],
  });
  pivot.addRecord({ id: 1, region: 'East', city: null, year: '2024', v: 1 });
  pivot.addRecord({ id: 2, region: '', year: '2024', v: 2 });
  pivot.addRecord({ id: 3, year: '2024', v: 4 });
  const result = pivot.getResult();
  assert.deepEqual(result.rows.map((r) => r.path.join('/')), [
    'East/(空)',
    'East',
    '(空)/(空)',
    '(空)',
    '',
  ]);
  assert.deepEqual(result.grandTotal, [7]);
});

test('custom blank label is honoured', () => {
  const pivot = new PivotTable({
    rows: ['a'],
    columns: [],
    measures: [{ field: 'v', aggregation: 'sum' }],
    blankLabel: '(blank)',
  });
  pivot.addRecord({ id: 1, v: 3 });
  const result = pivot.getResult();
  assert.equal(result.rows[0].path[0], '(blank)');
});

test('stableSort keeps insertion order for equal keys', () => {
  const items = [
    { k: 2, i: 0 },
    { k: 1, i: 1 },
    { k: 2, i: 2 },
    { k: 1, i: 3 },
    { k: 1, i: 4 },
    { k: 2, i: 5 },
  ];
  const sorted = stableSort(items, (a, b) => a.k - b.k);
  assert.deepEqual(sorted.map((x) => x.i), [1, 3, 4, 0, 2, 5]);
  assert.deepEqual(items.map((x) => x.i), [0, 1, 2, 3, 4, 5]);
});

test('dimension sorting is stable and supports asc/desc/comparator', () => {
  const pivot = new PivotTable({
    rows: ['g'],
    columns: [],
    measures: [{ field: 'v', aggregation: 'sum' }],
    sorts: { g: (a, b) => compareLabels(a[0], b[0]) },
  });
  for (const [i, g] of ['b1', 'a1', 'b2', 'a2'].entries()) {
    pivot.addRecord({ id: i, g, v: 1 });
  }
  let result = pivot.getResult();
  assert.deepEqual(
    result.rows.filter((r) => !r.isGrandTotal).map((r) => r.path[0]),
    ['a1', 'a2', 'b1', 'b2'],
  );

  const desc = new PivotTable({
    rows: ['g'],
    columns: [],
    measures: [{ field: 'v', aggregation: 'sum' }],
    sorts: { g: 'desc' },
  });
  for (const [i, g] of ['b', 'a', 'c'].entries()) {
    desc.addRecord({ id: i, g, v: 1 });
  }
  result = desc.getResult();
  assert.deepEqual(
    result.rows.filter((r) => !r.isGrandTotal).map((r) => r.path[0]),
    ['c', 'b', 'a'],
  );

  const asc = new PivotTable({
    rows: ['g'],
    columns: [],
    measures: [{ field: 'v', aggregation: 'sum' }],
    sorts: { g: 'asc' },
  });
  for (const [i, g] of ['10', '9', '100'].entries()) {
    asc.addRecord({ id: i, g, v: 1 });
  }
  result = asc.getResult();
  assert.deepEqual(
    result.rows.filter((r) => !r.isGrandTotal).map((r) => r.path[0]),
    ['9', '10', '100'],
  );
});

test('same field name at different levels stays independent', () => {
  const pivot = new PivotTable({
    rows: ['loc', 'loc'],
    columns: [],
    measures: [{ field: 'v', aggregation: 'sum' }],
  });
  pivot.addRecord({ id: 1, loc: 'East', v: 3 });
  pivot.addRecord({ id: 2, loc: 'West', v: 5 });
  const result = pivot.getResult();
  assert.deepEqual(result.rows.map((r) => r.path.join('/')), [
    'East/East',
    'East',
    'West/West',
    'West',
    '',
  ]);
  assert.deepEqual(result.rows[0].cells[0], [3]);
  assert.deepEqual(result.rows[1].cells[0], [3]);
  assert.deepEqual(result.grandTotal, [8]);
});

test('same member value at different levels is not merged', () => {
  const pivot = new PivotTable({
    rows: ['region', 'city'],
    columns: [],
    measures: [{ field: 'v', aggregation: 'sum' }],
  });
  pivot.addRecord({ id: 1, region: 'East', city: 'East', v: 1 });
  pivot.addRecord({ id: 2, region: 'East', city: 'West', v: 2 });
  pivot.addRecord({ id: 3, region: 'West', city: 'East', v: 4 });
  const result = pivot.getResult();
  assert.deepEqual(result.rows.map((r) => [r.path.join('/'), r.cells[0][0]]), [
    ['East/East', 1],
    ['East/West', 2],
    ['East', 3],
    ['West/East', 4],
    ['West', 4],
    ['', 7],
  ]);
});

test('same field name in rows and columns stays independent', () => {
  const pivot = new PivotTable({
    rows: ['region'],
    columns: ['region'],
    measures: [{ field: 'v', aggregation: 'sum' }],
  });
  pivot.addRecord({ id: 1, region: 'A', v: 1 });
  pivot.addRecord({ id: 2, region: 'B', v: 2 });
  const result = pivot.getResult();
  assert.deepEqual(result.columns.map((c) => c.path.join('/')), ['A', 'B', '总计']);
  assert.deepEqual(result.rows.map((r) => r.path.join('/')), ['A', 'B', '']);
  assert.deepEqual(result.rows[0].cells.map((c) => c[0]), [1, null, 1]);
  assert.deepEqual(result.rows[1].cells.map((c) => c[0]), [null, 2, 2]);
});

test('average of subtotal is computed from sum/count, not child averages', () => {
  const pivot = new PivotTable({
    rows: ['region', 'city'],
    columns: [],
    measures: [{ field: 'amount', aggregation: 'avg' }],
  });
  pivot.addRecord({ id: 1, region: 'R1', city: 'c1', amount: 10 });
  pivot.addRecord({ id: 2, region: 'R2', city: 'c2', amount: 20 });
  pivot.addRecord({ id: 3, region: 'R2', city: 'c2', amount: 30 });
  pivot.addRecord({ id: 4, region: 'R2', city: 'c2', amount: 50 });
  const result = pivot.getResult();
  const totalCol = result.columns.length - 1;
  const byPath = new Map(result.rows.map((r) => [r.path.join('/'), r]));
  closeTo(byPath.get('R1').cells[totalCol][0], 10);
  closeTo(byPath.get('R2').cells[totalCol][0], 100 / 3);
  closeTo(byPath.get('').cells[totalCol][0], 27.5);
  assert.ok(Math.abs(byPath.get('').cells[totalCol][0] - (10 + 100 / 3) / 2) > 1);
});

test('average subtotal across columns also uses sum/count', () => {
  const pivot = new PivotTable({
    rows: ['g'],
    columns: ['year', 'quarter'],
    measures: [{ field: 'v', aggregation: 'avg' }],
  });
  pivot.addRecord({ id: 1, g: 'A', year: '2024', quarter: 'Q1', v: 10 });
  pivot.addRecord({ id: 2, g: 'A', year: '2024', quarter: 'Q2', v: 20 });
  pivot.addRecord({ id: 3, g: 'A', year: '2024', quarter: 'Q2', v: 50 });
  const result = pivot.getResult();
  const subtotalCol = result.columns.findIndex((c) => c.isSubtotal);
  closeTo(result.rows[0].cells[subtotalCol][0], 80 / 3);
});

test('min/max recompute correctly after retraction', () => {
  const pivot = new PivotTable({
    rows: ['g'],
    columns: [],
    measures: [
      { field: 'v', aggregation: 'min' },
      { field: 'v', aggregation: 'max' },
    ],
  });
  pivot.addRecord({ id: 1, g: 'A', v: 5 });
  pivot.addRecord({ id: 2, g: 'A', v: 9 });
  pivot.addRecord({ id: 3, g: 'A', v: 7 });
  const totalCol = 0;
  let result = pivot.getResult();
  assert.deepEqual(result.rows[0].cells[totalCol], [5, 9]);
  pivot.removeRecord(2);
  result = pivot.getResult();
  assert.deepEqual(result.rows[0].cells[totalCol], [5, 7]);
  pivot.removeRecord(1);
  result = pivot.getResult();
  assert.deepEqual(result.rows[0].cells[totalCol], [7, 7]);
});

test('null measure values are skipped by sum/count/avg', () => {
  const pivot = new PivotTable({
    rows: ['g'],
    columns: [],
    measures: [
      { field: 'v', aggregation: 'sum' },
      { field: 'v', aggregation: 'count' },
      { field: 'v', aggregation: 'avg' },
    ],
  });
  pivot.addRecord({ id: 1, g: 'A', v: 6 });
  pivot.addRecord({ id: 2, g: 'A', v: null });
  pivot.addRecord({ id: 3, g: 'A' });
  const result = pivot.getResult();
  assert.deepEqual(result.rows[0].cells[0], [6, 1, 6]);
});

test('duplicate record id throws', () => {
  const pivot = new PivotTable({
    rows: ['g'],
    columns: [],
    measures: [{ field: 'v', aggregation: 'sum' }],
  });
  pivot.addRecord({ id: 1, g: 'A', v: 1 });
  assert.throws(() => pivot.addRecord({ id: 1, g: 'A', v: 2 }), /Duplicate record id/);
});

test('clear resets all state', () => {
  const pivot = makeBasic();
  pivot.clear();
  const stats = pivot.debugStats();
  assert.deepEqual(stats, { records: 0, rowNodes: 1, colNodes: 1, aggNodes: 0 });
  const result = pivot.getResult();
  assert.equal(result.rows.length, 1);
  assert.deepEqual(result.grandTotal, [null]);
});

console.log('');
console.log(passed + ' passed, ' + failed + ' failed');
if (failed > 0) {
  console.log('failures: ' + failures.join(', '));
  process.exit(1);
}
