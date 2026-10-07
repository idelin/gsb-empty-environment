import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeGrid } from '../src/tree-grid.ts';
import { cols, node, rowIds } from './helpers.ts';

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((res) => (resolve = res));
  return { promise, resolve };
}

test('async expansion updates the flat visible grid', async () => {
  const grid = new TreeGrid({
    columns: cols('a', 'b'),
    nodes: [node('r1', undefined, { expanded: false, hasChildren: true }), node('r2')],
  });
  assert.deepEqual(rowIds(grid.getVisibleGrid()), ['r1', 'r2']);

  const load = deferred<ReturnType<typeof node>[]>();
  const expanding = grid.expandAsync('r1', () => load.promise);
  assert.equal(grid.isLoading('r1'), true);
  // not yet resolved: grid unchanged
  assert.deepEqual(rowIds(grid.getVisibleGrid()), ['r1', 'r2']);

  load.resolve([node('r1.1'), node('r1.2')]);
  await expanding;
  assert.equal(grid.isLoading('r1'), false);
  assert.deepEqual(rowIds(grid.getVisibleGrid()), ['r1', 'r1.1', 'r1.2', 'r2']);
});

test('navigation enters async-loaded rows and skips disabled ones', async () => {
  const grid = new TreeGrid({
    columns: cols('a'),
    nodes: [node('r1', undefined, { expanded: false, hasChildren: true }), node('r2')],
  });
  await grid.expandAsync('r1', async () => [
    node('r1.1', undefined, { disabled: true }),
    node('r1.2'),
  ]);

  grid.focusCell('r1', 'a');
  assert.equal(grid.moveDown(), true);
  assert.deepEqual(grid.getFocus(), { rowId: 'r1.2', colKey: 'a' });
  assert.equal(grid.moveDown(), true);
  assert.deepEqual(grid.getFocus(), { rowId: 'r2', colKey: 'a' });
});

test('concurrent expandAsync calls share one load', async () => {
  const grid = new TreeGrid({
    columns: cols('a'),
    nodes: [node('r1', undefined, { expanded: false, hasChildren: true })],
  });
  let calls = 0;
  const loader = async () => {
    calls++;
    return [node('r1.1')];
  };
  await Promise.all([grid.expandAsync('r1', loader), grid.expandAsync('r1', loader)]);
  assert.equal(calls, 1);
  assert.deepEqual(rowIds(grid.getVisibleGrid()), ['r1', 'r1.1']);
});

test('collapsing after async expansion returns focus to the ancestor row', async () => {
  const grid = new TreeGrid({
    columns: cols('a', 'b'),
    nodes: [node('r1', undefined, { expanded: false, hasChildren: true }), node('r2')],
  });
  await grid.expandAsync('r1', async () => [node('r1.1', [node('r1.1.1')])]);
  assert.deepEqual(rowIds(grid.getVisibleGrid()), ['r1', 'r1.1', 'r1.1.1', 'r2']);

  grid.focusCell('r1.1.1', 'b');
  grid.collapse('r1');
  assert.deepEqual(rowIds(grid.getVisibleGrid()), ['r1', 'r2']);
  assert.deepEqual(grid.getFocus(), { rowId: 'r1', colKey: 'b' });
});

test('expandAsync on unknown row rejects', async () => {
  const grid = new TreeGrid({ columns: cols('a'), nodes: [node('r1')] });
  await assert.rejects(grid.expandAsync('nope', async () => []), /Unknown row/);
});
