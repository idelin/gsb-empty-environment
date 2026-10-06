import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeTableModel } from '../src/model.ts';
import { buildVisibleGrid, gridToMatrix } from '../src/grid.ts';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

function sampleModel(): TreeTableModel {
  return new TreeTableModel(
    ['c1', 'c2', 'c3'],
    [
      { id: 'A', expanded: true },
      { id: 'B', parentId: 'A', expanded: true },
      { id: 'D', parentId: 'B', disabledCells: ['c2'] },
      { id: 'C', parentId: 'A' },
    ],
  );
}

test('flat visible grid lists only visible rows in DFS order', () => {
  const model = sampleModel();
  const grid = buildVisibleGrid(model);
  assert.deepEqual(grid.columns, ['c1', 'c2', 'c3']);
  assert.deepEqual(grid.rows.map((row) => row.id), ['A', 'B', 'D', 'C']);
  assert.deepEqual(grid.rows.map((row) => row.depth), [0, 1, 2, 1]);
  assert.deepEqual(gridToMatrix(grid), [
    ['A:c1', 'A:c2', 'A:c3'],
    ['B:c1', 'B:c2', 'B:c3'],
    ['D:c1', 'D:c2!', 'D:c3'],
    ['C:c1', 'C:c2', 'C:c3'],
  ]);
});

test('collapsing hides the whole subtree in the flat grid', () => {
  const model = sampleModel();
  model.collapse('B');
  assert.deepEqual(buildVisibleGrid(model).rows.map((row) => row.id), ['A', 'B', 'C']);
  model.collapse('A');
  assert.deepEqual(buildVisibleGrid(model).rows.map((row) => row.id), ['A']);
});

test('async expand only changes the flat grid after the loader resolves', async () => {
  const model = sampleModel(); // C is collapsed with no children loaded yet
  const loader = deferred<readonly { id: string }[]>();
  const expansion = model.expandAsync('C', () => loader.promise);

  assert.equal(model.isLoading('C'), true);
  assert.deepEqual(buildVisibleGrid(model).rows.map((row) => row.id), ['A', 'B', 'D', 'C']);

  loader.resolve([{ id: 'C1' }, { id: 'C2', disabledCells: ['c1'] }]);
  await expansion;

  assert.equal(model.isLoading('C'), false);
  assert.equal(model.isExpanded('C'), true);
  const grid = buildVisibleGrid(model);
  assert.deepEqual(grid.rows.map((row) => row.id), ['A', 'B', 'D', 'C', 'C1', 'C2']);
  assert.deepEqual(gridToMatrix(grid).at(-1), ['C2:c1!', 'C2:c2', 'C2:c3']);
});

test('failed async expand restores the previous flat grid', async () => {
  const model = sampleModel();
  const loader = deferred<readonly { id: string }[]>();
  const expansion = model.expandAsync('C', () => loader.promise);
  loader.reject(new Error('network down'));
  await assert.rejects(expansion, /network down/);
  assert.equal(model.isLoading('C'), false);
  assert.equal(model.isExpanded('C'), false);
  assert.deepEqual(buildVisibleGrid(model).rows.map((row) => row.id), ['A', 'B', 'D', 'C']);
});

test('column deletion shrinks every row of the flat grid', () => {
  const model = sampleModel();
  model.removeColumn('c2');
  const grid = buildVisibleGrid(model);
  assert.deepEqual(grid.columns, ['c1', 'c3']);
  assert.deepEqual(gridToMatrix(grid)[2], ['D:c1', 'D:c3']);
  model.removeColumn('c1');
  assert.deepEqual(gridToMatrix(buildVisibleGrid(model))[0], ['A:c3']);
});

test('row removal drops the subtree from the flat grid', () => {
  const model = sampleModel();
  model.removeRow('B');
  assert.deepEqual(buildVisibleGrid(model).rows.map((row) => row.id), ['A', 'C']);
  assert.equal(model.hasRow('D'), false);
});
