import assert from 'node:assert/strict';
import { PivotTable, DEFAULT_NULL_LABEL } from '../dist/pivot.mjs';

let passed = 0;
function test(name, fn) {
  try {
    fn();
    passed++;
    console.log(`ok - ${name}`);
  } catch (err) {
    console.error(`FAIL - ${name}`);
    console.error(err);
    process.exitCode = 1;
  }
}

function totalCell(view) {
  const grand = view.rows.find((r) => r.kind === 'grandTotal');
  return grand.cells[grand.cells.length - 1];
}

function findRow(view, kind, pathPrefix) {
  return view.rows.find(
    (r) => r.kind === kind && JSON.stringify(r.path) === JSON.stringify(pathPrefix),
  );
}

test('multi-level rows/columns with subtotals and grand total', () => {
  const pt = new PivotTable({
    rows: ['region', 'city'],
    columns: ['year'],
    measures: [{ field: 'sales', agg: 'sum' }, { field: 'sales', agg: 'count', label: '单数' }],
  });
  pt.addRecord({ region: '华东', city: '上海', year: 2024, sales: 100 });
  pt.addRecord({ region: '华东', city: '上海', year: 2025, sales: 50 });
  pt.addRecord({ region: '华东', city: '杭州', year: 2024, sales: 30 });
  pt.addRecord({ region: '华北', city: '北京', year: 2025, sales: 200 });

  const view = pt.render();
  assert.deepEqual(view.columns.map((c) => c.header), ['2024', '2025', '总计']);

  const sh = findRow(view, 'detail', ['华东', '上海']);
  assert.deepEqual(sh.cells.map((c) => c.values[0]), [100, 50, 150]);

  const sub = findRow(view, 'subtotal', ['华东']);
  assert.deepEqual(sub.cells.map((c) => c.values[0]), [130, 50, 180]);
  assert.equal(sub.header, '华东 小计');

  const grand = view.rows[view.rows.length - 1];
  assert.equal(grand.kind, 'grandTotal');
  assert.deepEqual(grand.cells.map((c) => c.values[0]), [130, 250, 380]);
  assert.deepEqual(grand.cells.map((c) => c.values[1]), [2, 2, 4]);
});

test('null dimension values use a dedicated label', () => {
  const pt = new PivotTable({
    rows: ['region'],
    measures: [{ field: 'v', agg: 'sum' }],
  });
  pt.addRecord({ region: null, v: 1 });
  pt.addRecord({ v: 2 });
  pt.addRecord({ region: '华东', v: 4 });

  const view = pt.render();
  const nullRow = findRow(view, 'detail', [DEFAULT_NULL_LABEL]);
  assert.ok(nullRow, 'null values grouped under the null label');
  assert.equal(nullRow.cells[0].values[0], 3);
  assert.equal(totalCell(view).values[0], 7);

  const custom = new PivotTable({
    rows: ['region'],
    measures: [{ field: 'v', agg: 'sum' }],
    nullLabel: '(缺失)',
  });
  custom.addRecord({ region: null, v: 1 });
  assert.ok(findRow(custom.render(), 'detail', ['(缺失)']));
});

test('same label at different levels stays on distinct paths', () => {
  const pt = new PivotTable({
    rows: ['region', 'city'],
    measures: [{ field: 'v', agg: 'sum' }],
  });
  pt.addRecord({ region: '北', city: '北', v: 1 });
  pt.addRecord({ region: '北', city: '南', v: 2 });
  pt.addRecord({ region: '南', city: '北', v: 4 });

  const paths = pt.rowPaths();
  assert.deepEqual(paths, [['北'], ['北', '北'], ['北', '南'], ['南'], ['南', '北']]);

  const view = pt.render();
  assert.equal(findRow(view, 'detail', ['北', '北']).cells[0].values[0], 1);
  assert.equal(findRow(view, 'detail', ['南', '北']).cells[0].values[0], 4);
  assert.equal(findRow(view, 'subtotal', ['北']).cells[0].values[0], 3);
  assert.equal(findRow(view, 'subtotal', ['南']).cells[0].values[0], 4);
  assert.equal(totalCell(view).values[0], 7);
});

test('duplicate records aggregate and retract one at a time', () => {
  const pt = new PivotTable({
    rows: ['region'],
    measures: [{ field: 'v', agg: 'sum' }, { field: 'v', agg: 'count' }],
  });
  const rec = { region: '华东', v: 10 };
  const id1 = pt.addRecord(rec);
  const id2 = pt.addRecord({ ...rec });
  const id3 = pt.addRecord({ ...rec });

  let view = pt.render();
  assert.deepEqual(findRow(view, 'detail', ['华东']).cells[0].values, [30, 3]);

  assert.equal(pt.retractRecord(id2), true);
  view = pt.render();
  assert.deepEqual(findRow(view, 'detail', ['华东']).cells[0].values, [20, 2]);

  assert.equal(pt.retractRecord(id1), true);
  assert.equal(pt.retractRecord(id3), true);
  view = pt.render();
  assert.equal(findRow(view, 'detail', ['华东']), undefined);
  assert.deepEqual(totalCell(view).values, [null, null]);
  assert.equal(pt.retractRecord(id1), false, 'double retract rejected');
});

test('retracting the last record cleans up dimension paths', () => {
  const pt = new PivotTable({
    rows: ['region', 'city'],
    columns: ['year'],
    measures: [{ field: 'v', agg: 'sum' }],
  });
  const keep = pt.addRecord({ region: '华东', city: '上海', year: 2024, v: 1 });
  const gone = pt.addRecord({ region: '华北', city: '北京', year: 2025, v: 2 });

  pt.retractRecord(gone);
  assert.deepEqual(pt.rowPaths(), [['华东'], ['华东', '上海']]);
  let view = pt.render();
  assert.deepEqual(view.columns.map((c) => c.header), ['2024', '总计']);
  assert.equal(view.rows.filter((r) => r.kind !== 'grandTotal').length, 2);

  pt.retractRecord(keep);
  assert.deepEqual(pt.rowPaths(), []);
  view = pt.render();
  assert.deepEqual(view.rows.map((r) => r.kind), ['grandTotal']);
  assert.deepEqual(view.columns.map((c) => c.header), ['总计']);
  assert.deepEqual(totalCell(view).values, [null]);
  assert.equal(pt.size, 0);
});

test('average of grand total is sum/count, not average of subtotals', () => {
  const pt = new PivotTable({
    rows: ['grp'],
    measures: [{ field: 'v', agg: 'avg' }, { field: 'v', agg: 'sum' }, { field: 'v', agg: 'count' }],
  });
  pt.addRecord({ grp: 'A', v: 10 });
  pt.addRecord({ grp: 'A', v: 20 });
  pt.addRecord({ grp: 'B', v: 30 });

  const view = pt.render();
  assert.equal(findRow(view, 'detail', ['A']).cells[0].values[0], 15);
  assert.equal(findRow(view, 'detail', ['B']).cells[0].values[0], 30);
  const grand = totalCell(view).values;
  assert.equal(grand[0], 20, '(10+20+30)/3, not (15+30)/2');
  assert.equal(grand[1], 60);
  assert.equal(grand[2], 3);
});

test('stable sort: ties keep insertion order, null label last', () => {
  const pt = new PivotTable({
    rows: ['region'],
    measures: [{ field: 'v', agg: 'sum' }],
    rowSort: { by: 'measure', direction: 'desc' },
  });
  pt.addRecord({ region: '甲', v: 10 });
  pt.addRecord({ region: '乙', v: 30 });
  pt.addRecord({ region: '丙', v: 10 });
  pt.addRecord({ region: null, v: 99 });

  const order = pt.render().rows.filter((r) => r.kind === 'detail').map((r) => r.header);
  assert.deepEqual(order, ['乙', '甲', '丙', DEFAULT_NULL_LABEL]);

  const asc = new PivotTable({
    rows: ['region'],
    measures: [{ field: 'v', agg: 'sum' }],
    rowSort: { by: 'label', direction: 'asc' },
  });
  asc.addRecord({ region: 'b', v: 1 });
  asc.addRecord({ region: 'a', v: 1 });
  asc.addRecord({ region: null, v: 1 });
  const labels = asc.render().rows.filter((r) => r.kind === 'detail').map((r) => r.header);
  assert.deepEqual(labels, ['a', 'b', DEFAULT_NULL_LABEL]);
});

test('columns sort and sparse columns stay independent per row', () => {
  const pt = new PivotTable({
    rows: ['region'],
    columns: ['q'],
    measures: [{ field: 'v', agg: 'sum' }],
    columnSort: { by: 'label', direction: 'desc' },
  });
  pt.addRecord({ region: '甲', q: 'Q1', v: 1 });
  pt.addRecord({ region: '乙', q: 'Q2', v: 2 });

  const view = pt.render();
  assert.deepEqual(view.columns.map((c) => c.header), ['Q2', 'Q1', '总计']);
  assert.deepEqual(findRow(view, 'detail', ['甲']).cells.map((c) => c.values[0]), [null, 1, 1]);
  assert.deepEqual(findRow(view, 'detail', ['乙']).cells.map((c) => c.values[0]), [2, null, 2]);
});

console.log(`\n${passed} tests passed`);
