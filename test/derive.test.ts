import { test } from 'node:test';
import assert from 'node:assert/strict';
import { derive } from '../src/derive.ts';
import { glyphMetrics, sampleDoc } from '../src/fixtures.ts';
import type { HeaderCell, HeaderSnapshot } from '../src/types.ts';

function cell(snapshot: HeaderSnapshot, key: string): HeaderCell {
  const found = snapshot.cells.find((c) => c.key === key);
  assert.ok(found, `expected cell ${key}`);
  return found;
}

test('initial layout: spans, columns, data mapping', () => {
  const snap = derive(sampleDoc(), 0, glyphMetrics);
  assert.equal(snap.depth, 3);
  assert.deepEqual(
    snap.columns.map((c) => [c.key, c.dataIndex]),
    [
      ['leaf:c-name', 0],
      ['leaf:c-age', 1],
      ['leaf:c-math', 2],
      ['leaf:c-eng', 3],
      ['leaf:c-rank', 4],
      ['leaf:c-note', 5],
    ],
  );
  assert.deepEqual(
    [cell(snap, 'group:g-basic').row, cell(snap, 'group:g-basic').col, cell(snap, 'group:g-basic').colspan],
    [0, 0, 2],
  );
  assert.deepEqual(
    [cell(snap, 'group:g-metrics').col, cell(snap, 'group:g-metrics').colspan],
    [2, 3],
  );
  assert.deepEqual([cell(snap, 'group:g-scores').row, cell(snap, 'group:g-scores').colspan], [1, 2]);
  assert.equal(cell(snap, 'leaf:c-note').rowspan, 3);
  assert.equal(cell(snap, 'leaf:c-name').rowspan, 2);
  assert.equal(cell(snap, 'leaf:c-math').rowspan, 1);
  for (const c of snap.cells) {
    assert.ok(c.rowspan >= 1 && c.colspan >= 1, `no invalid span on ${c.key}`);
  }
});

test('widths come from fixture glyph metrics, not a layout engine', () => {
  const snap = derive(sampleDoc(), 0, glyphMetrics);
  // "Name" = 9+7+11+7 = 34, +12 padding = 46 -> clamped to min 48
  assert.equal(snap.columns[0].width, 48);
  assert.equal(snap.width, 6 * 48);
  assert.equal(cell(snap, 'group:g-basic').width, 96);
  assert.equal(cell(snap, 'group:g-metrics').width, 144);
});

test('keyboard tree follows the spanned grid', () => {
  const snap = derive(sampleDoc(), 0, glyphMetrics);
  assert.deepEqual(snap.nav['leaf:c-math'], {
    up: 'group:g-scores',
    down: null,
    left: 'leaf:c-age',
    right: 'leaf:c-eng',
  });
  assert.deepEqual(snap.nav['group:g-scores'], {
    up: 'group:g-metrics',
    down: 'leaf:c-math',
    left: 'leaf:c-age',
    right: 'leaf:c-rank',
  });
  assert.equal(snap.nav['leaf:c-note'].left, 'group:g-metrics');
  assert.equal(snap.nav['leaf:c-note'].up, null);
  assert.equal(snap.nav['leaf:c-name'].up, 'group:g-basic');
});

test('hiding every leaf of a group removes the group with no invalid spans', () => {
  const doc = sampleDoc();
  doc.hidden.add('c-math');
  doc.hidden.add('c-eng');
  const snap = derive(doc, 1, glyphMetrics);
  assert.ok(!snap.cells.some((c) => c.key === 'group:g-scores'), 'empty group disappears');
  assert.deepEqual([cell(snap, 'group:g-metrics').col, cell(snap, 'group:g-metrics').colspan], [2, 1]);
  assert.deepEqual(
    snap.columns.map((c) => c.key),
    ['leaf:c-name', 'leaf:c-age', 'leaf:c-rank', 'leaf:c-note'],
  );
  assert.equal(snap.depth, 2);
  assert.equal(cell(snap, 'leaf:c-note').rowspan, 2);
  for (const c of snap.cells) assert.ok(c.rowspan >= 1 && c.colspan >= 1);
});

test('hiding all leaves of a nested chain removes every ancestor group', () => {
  const doc = sampleDoc();
  for (const id of ['c-math', 'c-eng', 'c-rank']) doc.hidden.add(id);
  const snap = derive(doc, 1, glyphMetrics);
  assert.ok(!snap.cells.some((c) => c.key === 'group:g-metrics'));
  assert.deepEqual(
    snap.columns.map((c) => c.key),
    ['leaf:c-name', 'leaf:c-age', 'leaf:c-note'],
  );
});

test('hiding everything yields an empty but valid snapshot', () => {
  const doc = sampleDoc();
  for (const id of ['c-name', 'c-age', 'c-math', 'c-eng', 'c-rank', 'c-note']) doc.hidden.add(id);
  const snap = derive(doc, 1, glyphMetrics);
  assert.equal(snap.columns.length, 0);
  assert.equal(snap.cells.length, 0);
  assert.equal(snap.width, 0);
  assert.deepEqual(snap.dataIndexByKey, {});
});

test('collapsed group becomes one terminal column consuming one data index', () => {
  const doc = sampleDoc();
  doc.collapsed.add('g-metrics');
  const snap = derive(doc, 1, glyphMetrics);
  assert.deepEqual(
    snap.columns.map((c) => [c.key, c.kind]),
    [
      ['leaf:c-name', 'leaf'],
      ['leaf:c-age', 'leaf'],
      ['group:g-metrics', 'group-summary'],
      ['leaf:c-note', 'leaf'],
    ],
  );
  const summary = cell(snap, 'group:g-metrics');
  assert.equal(summary.kind, 'group-summary');
  assert.deepEqual([summary.row, summary.col, summary.rowspan, summary.colspan], [0, 2, 2, 1]);
  assert.equal(snap.dataIndexByKey['group:g-metrics'], 2);
  assert.equal(snap.nav['leaf:c-age'].right, 'group:g-metrics');
  assert.equal(snap.nav['group:g-metrics'].right, 'leaf:c-note');
  // "Metrics" = 11+7+4+4+3+6+6 = 41, +12 padding = 53
  assert.equal(summary.width, 53);
});

test('same-name columns are distinct identities', () => {
  const doc = sampleDoc();
  doc.hidden.add('c-math');
  const snap = derive(doc, 1, glyphMetrics);
  const scoreColumns = snap.columns.filter((c) => c.title === 'Score');
  assert.equal(scoreColumns.length, 1);
  assert.equal(scoreColumns[0].nodeId, 'c-eng');
  assert.equal(snap.dataIndexByKey['leaf:c-math'], undefined);
  assert.equal(snap.dataIndexByKey['leaf:c-eng'], 2);
});
