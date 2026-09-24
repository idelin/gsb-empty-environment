import { test } from 'node:test';
import assert from 'node:assert/strict';
import { History } from '../src/history.ts';
import {
  directionOf,
  mapSelectionBack,
  mapSelectionsForward,
  selectionFromUtf16,
} from '../src/selection.ts';

test('merged selections keep primary identity and full provenance', () => {
  const h = new History('abcdef');
  const v1 = h.head;
  const s1 = selectionFromUtf16(h.getVersion(v1), 1, 1, 3); // forward over "bc"
  const s2 = selectionFromUtf16(h.getVersion(v1), 2, 5, 4); // backward over "e"
  assert.equal(directionOf(s1), 'forward');
  assert.equal(directionOf(s2), 'backward');

  const v2 = h.addVersion({ start: 1, deleteCount: 4 }); // delete "bcde" -> "af"
  const m = h.mappingBetween(v1, v2);
  const fwd = mapSelectionsForward([s1, s2], m);

  assert.equal(fwd.selections.length, 1);
  const kept = fwd.selections[0];
  assert.equal(kept.id, 1, 'primary selection identity survives');
  assert.equal(directionOf(kept), 'collapsed');

  const info = kept.mergedFrom;
  assert.ok(info);
  assert.equal(info.sourceVersion, v1);
  assert.equal(info.sources.length, 2);
  // source ids, directions and zero-width stickiness are all preserved
  assert.deepEqual(
    info.sources.map((s) => [s.id, s.direction, s.anchor.stick, s.active.stick]),
    [
      [1, 'forward', 'left', 'right'],
      [2, 'backward', 'left', 'right'],
    ],
  );
  assert.deepEqual(fwd.merges, [{ keptId: 1, absorbedIds: [2] }]);
});

test('reverse mapping of a merged selection restores every source on undo', () => {
  const h = new History('abcdef');
  const v1 = h.head;
  const s1 = selectionFromUtf16(h.getVersion(v1), 1, 1, 3);
  const s2 = selectionFromUtf16(h.getVersion(v1), 2, 5, 4);
  const v2 = h.addVersion({ start: 1, deleteCount: 4 });
  const m = h.mappingBetween(v1, v2);
  const fwd = mapSelectionsForward([s1, s2], m);

  const back = mapSelectionBack(fwd.selections[0], m);
  assert.equal(back.ambiguous, false);
  assert.equal(back.selections.length, 2);
  assert.deepEqual(back.selections.map((s) => s.id), [1, 2]);
  assert.equal(directionOf(back.selections[0]), 'forward');
  assert.equal(directionOf(back.selections[1]), 'backward');
});

test('reverse mapping without provenance reports ambiguity and its sources', () => {
  const h = new History('abcdef');
  const v1 = h.head;
  const v2 = h.addVersion({ start: 1, deleteCount: 4 }); // "af"
  const m = h.mappingBetween(v1, v2);

  // a fresh cursor in v2 sitting where "bcde" used to be: no provenance
  const cold = selectionFromUtf16(h.getVersion(v2), 9, 1, 1);
  const back = mapSelectionBack(cold, m);
  assert.equal(back.ambiguous, true);
  assert.equal(back.selections.length, 1);
  assert.ok(back.ambiguity.length >= 1);
  assert.match(back.ambiguity[0], /deleted between v1 and v2/);
  assert.deepEqual(
    back.lostContext.map((l) => [l.selectionId, l.endpoint]),
    [
      [9, 'anchor'],
      [9, 'active'],
    ],
  );
});

test('positions away from deletions map back unambiguously', () => {
  const h = new History('abcdef');
  const v1 = h.head;
  const v2 = h.addVersion({ start: 2, deleteCount: 2 }); // "abef"
  const m = h.mappingBetween(v1, v2);
  const cursor = selectionFromUtf16(h.getVersion(v2), 3, 0, 0);
  const back = mapSelectionBack(cursor, m);
  assert.equal(back.ambiguous, false);
  assert.deepEqual(back.lostContext, []);
  assert.equal(back.selections[0].active.boundary, 0);
});
