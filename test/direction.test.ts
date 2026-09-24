import { test } from 'node:test';
import assert from 'node:assert/strict';
import { History } from '../src/history.ts';
import {
  directionOf,
  mapSelection,
  mapSelectionsForward,
  mapSelectionBack,
  selectionFromUtf16,
  selectionToUtf16,
} from '../src/selection.ts';

test('reverse selection keeps its direction after an insertion in front of it', () => {
  const h = new History('hello world');
  const v1 = h.head;
  // anchor at 8, active at 2: a backward (reverse) selection
  const sel = selectionFromUtf16(h.getVersion(v1), 1, 8, 2);
  assert.equal(directionOf(sel), 'backward');

  const v2 = h.addVersion({ start: 0, deleteCount: 0, insert: 'XYZ' });
  const mapped = mapSelection(sel, h.mappingBetween(v1, v2));
  assert.equal(directionOf(mapped), 'backward', 'direction unchanged');
  assert.deepEqual(selectionToUtf16(h.getVersion(v2), mapped), { anchor: 11, active: 5 });
  assert.ok(mapped.anchor.boundary > mapped.active.boundary, 'anchor still after active');
});

test('reverse selection keeps direction across several edits and a composed mapping', () => {
  const h = new History('abcdef');
  const v1 = h.head;
  const sel = selectionFromUtf16(h.getVersion(v1), 1, 5, 1); // backward over "bcde"
  h.addVersion({ start: 0, deleteCount: 0, insert: '>>' });
  const v3 = h.addVersion({ start: 0, deleteCount: 1 });
  const v4 = h.addVersion({ start: 6, deleteCount: 0, insert: '!' });
  const mapped = mapSelection(sel, h.mappingBetween(v1, v4));
  assert.equal(directionOf(mapped), 'backward');
  assert.deepEqual(selectionToUtf16(h.getVersion(v4), mapped), { anchor: 6, active: 2 });
});

test('two cursors pressed to the same point by one deletion leave a single visible cursor', () => {
  const h = new History('abcde');
  const v1 = h.head;
  const c1 = selectionFromUtf16(h.getVersion(v1), 1, 1, 1);
  const c2 = selectionFromUtf16(h.getVersion(v1), 2, 3, 3);

  const v2 = h.addVersion({ start: 1, deleteCount: 2 }); // delete "bc"
  assert.equal(h.textOf(v2), 'ade');
  const m = h.mappingBetween(v1, v2);
  const fwd = mapSelectionsForward([c1, c2], m);

  assert.equal(fwd.selections.length, 1, 'only one visible cursor remains');
  assert.equal(fwd.selections[0].id, 1, 'primary cursor keeps its identity');
  assert.deepEqual(selectionToUtf16(h.getVersion(v2), fwd.selections[0]), { anchor: 1, active: 1 });
  assert.deepEqual(fwd.merges, [{ keptId: 1, absorbedIds: [2] }]);

  // provenance: source ids, directions and zero-width stickiness are kept
  const info = fwd.selections[0].mergedFrom;
  assert.ok(info);
  assert.equal(info.sourceVersion, v1);
  assert.deepEqual(
    info.sources.map((s) => [s.id, s.direction, s.anchor.stick, s.active.stick]),
    [
      [1, 'collapsed', 'left', 'right'],
      [2, 'collapsed', 'left', 'right'],
    ],
  );

  // undo restores both cursors with their own sources
  const back = mapSelectionBack(fwd.selections[0], m);
  assert.equal(back.ambiguous, false);
  assert.equal(back.selections.length, 2);
  assert.deepEqual(
    back.selections.map((s) => [s.id, selectionToUtf16(h.getVersion(v1), s).active]),
    [
      [1, 1],
      [2, 3],
    ],
  );
});

test('undo through History restores the original multiple selections', () => {
  const h = new History('a😀b\nc');
  const v1 = h.head;
  const s1 = selectionFromUtf16(h.getVersion(v1), 1, 1, 1);
  const s2 = selectionFromUtf16(h.getVersion(v1), 2, 4, 4);
  h.addVersion({ start: 1, deleteCount: 3 }); // delete "😀b\n"
  const m = h.mappingBetween(v1, h.head);
  const fwd = mapSelectionsForward([s1, s2], m);
  assert.equal(fwd.selections.length, 1);

  h.undo();
  const restored = fwd.selections.flatMap((s) => mapSelectionBack(s, m).selections);
  assert.equal(restored.length, 2);
  assert.deepEqual(restored.map((s) => s.id).sort(), [1, 2]);
  assert.deepEqual(
    restored.map((s) => selectionToUtf16(h.getVersion(h.head), s).active).sort((a, b) => a - b),
    [1, 4],
  );
});
