import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeTableModel } from '../src/model.ts';
import { Navigator } from '../src/navigator.ts';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

/**
 *  A (expanded)
 *  └─ B (expanded)
 *     └─ D (collapsed, child E loaded but hidden)
 *        └─ E
 *  └─ C
 */
function sampleModel(): TreeTableModel {
  return new TreeTableModel(
    ['c1', 'c2', 'c3', 'c4'],
    [
      { id: 'A', expanded: true },
      { id: 'B', parentId: 'A', expanded: true },
      { id: 'D', parentId: 'B' },
      { id: 'E', parentId: 'D' },
      { id: 'C', parentId: 'A' },
    ],
  );
}

test('moves between visible cells in all four directions', () => {
  const nav = new Navigator(sampleModel());
  assert.equal(nav.setFocus('A', 'c2'), true);
  assert.equal(nav.moveDown(), true);
  assert.deepEqual(nav.focus, { rowId: 'B', columnId: 'c2' });
  nav.moveDown();
  assert.deepEqual(nav.focus, { rowId: 'D', columnId: 'c2' });
  nav.moveDown();
  assert.deepEqual(nav.focus, { rowId: 'C', columnId: 'c2' });
  assert.equal(nav.moveDown(), false); // bottom edge: focus stays
  assert.deepEqual(nav.focus, { rowId: 'C', columnId: 'c2' });
  nav.moveRight();
  assert.deepEqual(nav.focus, { rowId: 'C', columnId: 'c3' });
  nav.moveUp();
  nav.moveUp();
  assert.deepEqual(nav.focus, { rowId: 'B', columnId: 'c3' });
  nav.moveLeft();
  nav.moveLeft();
  assert.deepEqual(nav.focus, { rowId: 'B', columnId: 'c1' });
  assert.equal(nav.moveLeft(), false); // left edge: focus stays
});

test('never jumps into hidden rows', () => {
  const model = sampleModel();
  const nav = new Navigator(model);
  assert.equal(nav.setFocus('E', 'c1'), false); // E is hidden under collapsed D
  assert.equal(nav.focus, null);
  assert.equal(nav.setFocus('D', 'c1'), true);
  assert.equal(nav.moveDown(), true);
  assert.deepEqual(nav.focus, { rowId: 'C', columnId: 'c1' }); // skips hidden E
});

test('skips disabled cells vertically and horizontally', () => {
  const model = sampleModel();
  model.setCellDisabled('B', 'c2', true);
  model.setCellDisabled('D', 'c2', true);
  model.setCellDisabled('A', 'c3', true);
  const nav = new Navigator(model);

  assert.equal(nav.setFocus('B', 'c2'), false); // disabled cell cannot be focused
  assert.equal(nav.setFocus('A', 'c2'), true);
  assert.equal(nav.moveDown(), true);
  assert.deepEqual(nav.focus, { rowId: 'C', columnId: 'c2' }); // B and D skipped
  assert.equal(nav.moveUp(), true);
  assert.deepEqual(nav.focus, { rowId: 'A', columnId: 'c2' });

  nav.moveRight();
  assert.deepEqual(nav.focus, { rowId: 'A', columnId: 'c4' }); // c3 skipped
  nav.moveLeft();
  assert.deepEqual(nav.focus, { rowId: 'A', columnId: 'c2' }); // c3 skipped back
});

test('collapse moves focus to nearest visible ancestor, same column', () => {
  const model = sampleModel();
  const nav = new Navigator(model);
  nav.setFocus('D', 'c3');
  model.collapse('B'); // hides D and E
  assert.deepEqual(nav.focus, { rowId: 'B', columnId: 'c3' });
  model.collapse('A'); // hides B too
  assert.deepEqual(nav.focus, { rowId: 'A', columnId: 'c3' });
});

test('collapse walks past multiple hidden ancestors to the visible one', () => {
  const model = sampleModel();
  model.expand('D');
  const nav = new Navigator(model);
  nav.setFocus('E', 'c4');
  model.collapse('A'); // B, D, E all hidden at once
  assert.deepEqual(nav.focus, { rowId: 'A', columnId: 'c4' });
});

test('ancestor refocus skips disabled cells to the nearest left column', () => {
  const model = sampleModel();
  model.setCellDisabled('B', 'c3', true);
  const nav = new Navigator(model);
  nav.setFocus('D', 'c3');
  model.collapse('B');
  assert.deepEqual(nav.focus, { rowId: 'B', columnId: 'c2' });
});

test('deleted focused column falls back to nearest left column', () => {
  const model = sampleModel();
  const nav = new Navigator(model);
  nav.setFocus('A', 'c3');
  model.removeColumn('c3');
  assert.deepEqual(nav.focus, { rowId: 'A', columnId: 'c2' });
  model.removeColumn('c2');
  assert.deepEqual(nav.focus, { rowId: 'A', columnId: 'c1' });
});

test('with no left column left, falls back to nearest right column', () => {
  const model = sampleModel();
  const nav = new Navigator(model);
  nav.setFocus('A', 'c1');
  model.removeColumn('c1');
  assert.deepEqual(nav.focus, { rowId: 'A', columnId: 'c2' });
});

test('deleted column skips a disabled nearest-left cell', () => {
  const model = sampleModel();
  model.setCellDisabled('A', 'c2', true);
  const nav = new Navigator(model);
  nav.setFocus('A', 'c3');
  model.removeColumn('c3');
  assert.deepEqual(nav.focus, { rowId: 'A', columnId: 'c1' });
});

test('removing the focused row refocuses the nearest visible ancestor', () => {
  const model = sampleModel();
  const nav = new Navigator(model);
  nav.setFocus('D', 'c2');
  model.removeRow('B'); // removes B, D and E
  assert.deepEqual(nav.focus, { rowId: 'A', columnId: 'c2' });
});

test('async expand keeps focus, later collapse refocuses the ancestor', async () => {
  const model = sampleModel();
  const nav = new Navigator(model);
  nav.setFocus('C', 'c2');

  const loader = deferred<readonly { id: string }[]>();
  const expansion = model.expandAsync('C', () => loader.promise);
  assert.deepEqual(nav.focus, { rowId: 'C', columnId: 'c2' }); // unchanged while loading

  loader.resolve([{ id: 'C1' }, { id: 'C2' }]);
  await expansion;
  assert.deepEqual(nav.focus, { rowId: 'C', columnId: 'c2' }); // C still visible

  assert.equal(nav.moveDown(), true);
  assert.deepEqual(nav.focus, { rowId: 'C1', columnId: 'c2' }); // new rows navigable

  model.collapse('A'); // hides the whole subtree including C1
  assert.deepEqual(nav.focus, { rowId: 'A', columnId: 'c2' });
});

test('focus survives unrelated expansion and column insertion', () => {
  const model = sampleModel();
  const nav = new Navigator(model);
  nav.setFocus('D', 'c3');
  model.expand('D');
  model.addColumn('c0', 0);
  assert.deepEqual(nav.focus, { rowId: 'D', columnId: 'c3' });
});

test('focusFirst lands on the first enabled visible cell', () => {
  const model = sampleModel();
  model.setCellDisabled('A', 'c1', true);
  const nav = new Navigator(model);
  assert.equal(nav.focusFirst(), true);
  assert.deepEqual(nav.focus, { rowId: 'A', columnId: 'c2' });
});
