import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Mapping } from '../src/mapping.ts';
import { TextDocument } from '../src/document.ts';
import { Selection, SelectionSet } from '../src/selections.ts';
import { History } from '../src/history.ts';

test('anchor and active ends are stored separately with their own stickiness', () => {
  const sel = Selection.create(1, 2, 8, 'before', 'after');
  assert.equal(sel.anchor.offset, 2);
  assert.equal(sel.anchor.affinity, 'before');
  assert.equal(sel.active.offset, 8);
  assert.equal(sel.active.affinity, 'after');
  assert.equal(sel.direction, 'forward');
});

test('backward selection keeps its direction after an insertion before it', () => {
  const doc = TextDocument.fromText('hello world');
  const set = SelectionSet.of(Selection.create(1, 8, 3)); // backward: active < anchor
  assert.equal(set.selections[0].direction, 'backward');

  const { mapping } = doc.applyEdit(0, 0, '>>> '); // insert 4 chars at the start
  const { set: mapped, merges } = doc && set.mapForward(mapping);
  assert.equal(merges.length, 0);
  const sel = mapped.selections[0];
  assert.equal(sel.direction, 'backward');
  assert.equal(sel.anchor.offset, 12);
  assert.equal(sel.active.offset, 7);
  assert.ok(sel.active.offset < sel.anchor.offset);
});

test('direction is preserved while a selection stays zero-width', () => {
  const cursor = new Selection(
    7,
    { offset: 4, affinity: 'after' },
    { offset: 4, affinity: 'after' },
    'backward',
  );
  const m = Mapping.fromEdit({ start: 0, deletedLen: 0, insertedLen: 2 });
  const { set } = SelectionSet.of(cursor).mapForward(m);
  assert.equal(set.selections[0].direction, 'backward');
  assert.equal(set.selections[0].isCursor, true);
});

test('zero-width stickiness: cursor at a deletion boundary follows its affinity', () => {
  const doc = TextDocument.fromText('abcXYZdef');
  const { mapping } = doc.applyEdit(3, 3, ''); // delete 'XYZ'
  const { set: beforeSet } = SelectionSet.of(Selection.cursor(1, 3, 'before')).mapForward(mapping);
  const { set: afterSet } = SelectionSet.of(Selection.cursor(2, 3, 'after')).mapForward(mapping);
  assert.equal(beforeSet.selections[0].anchor.offset, 3);
  assert.equal(afterSet.selections[0].anchor.offset, 3);
  // ...but with an insertion they part ways:
  const { mapping: m2 } = doc.applyEdit(3, 3, '12345');
  const { set: b2 } = SelectionSet.of(Selection.cursor(1, 3, 'before')).mapForward(m2);
  const { set: a2 } = SelectionSet.of(Selection.cursor(2, 3, 'after')).mapForward(m2);
  assert.equal(b2.selections[0].anchor.offset, 3);
  assert.equal(a2.selections[0].anchor.offset, 8);
});

test('two cursors crushed onto one point by a single deletion merge to one visible cursor', () => {
  const doc = TextDocument.fromText('aaXXbb');
  const set = SelectionSet.of(Selection.cursor(1, 2), Selection.cursor(2, 4));
  const { mapping } = doc.applyEdit(2, 2, ''); // delete 'XX'
  const { set: mapped, merges } = set.mapForward(mapping);

  assert.equal(mapped.selections.length, 1, 'only one visible cursor remains');
  assert.equal(mapped.selections[0].id, 1, 'primary identity survives');
  assert.equal(mapped.selections[0].isCursor, true);
  assert.equal(mapped.selections[0].anchor.offset, 2);

  assert.equal(merges.length, 1);
  assert.equal(merges[0].into, 1);
  assert.deepEqual(
    merges[0].sources.map((s) => [s.id, s.anchor.offset, s.anchor.affinity]),
    [[2, 4, 'after']],
    'lost cursor keeps source id and zero-width stickiness',
  );
});

test('deletion merging selections keeps primary identity, direction and source records', () => {
  const doc = TextDocument.fromText('aa111bb222cc');
  // sel1 (backward): [1,5)  sel2 (forward): [7,10)
  const set = SelectionSet.of(Selection.create(1, 5, 1), Selection.create(2, 7, 10));
  const { mapping } = doc.applyEdit(4, 4, ''); // delete '1bb2' -> 'aa1122cc'
  const { set: mapped, merges } = set.mapForward(mapping);

  assert.equal(mapped.selections.length, 1);
  const merged = mapped.selections[0];
  assert.equal(merged.id, 1, 'primary (lowest id) identity survives');
  assert.equal(merged.direction, 'backward', 'primary direction survives');
  assert.equal(merged.from, 1);
  assert.equal(merged.to, 6);

  assert.equal(merges.length, 1);
  const lost = merges[0].sources.find((s) => s.id === 2);
  assert.ok(lost);
  assert.equal(lost.direction, 'forward');
  assert.deepEqual([lost.anchor.offset, lost.active.offset], [7, 10]);
  assert.equal(lost.anchor.affinity, 'after');
  assert.equal(lost.active.affinity, 'before');
});

test('reverse mapping reports merged-away selections as ambiguity', () => {
  const doc = TextDocument.fromText('aa111bb222cc');
  const set = SelectionSet.of(Selection.create(1, 1, 5), Selection.create(2, 7, 10));
  const { mapping } = doc.applyEdit(4, 4, '');
  const { set: merged } = set.mapForward(mapping);
  assert.equal(merged.selections.length, 1);

  const { set: back, ambiguities } = merged.mapBackward(mapping);
  const mergedReport = ambiguities.find((a) => a.kind === 'merged-sources');
  assert.ok(mergedReport && mergedReport.kind === 'merged-sources');
  assert.equal(mergedReport.selectionId, 1);
  assert.deepEqual(mergedReport.lost.map((s) => s.id), [2]);
  assert.equal(mergedReport.lost[0].direction, 'forward');
  assert.deepEqual(
    [mergedReport.lost[0].anchor.offset, mergedReport.lost[0].active.offset],
    [7, 10],
    'lost source positions are reported in the version where they lived',
  );

  // Primary selection still maps back to its own origin.
  const sel = back.selections[0];
  assert.equal(sel.id, 1);
  assert.deepEqual([sel.anchor.offset, sel.active.offset], [1, 10]);
});

test('reverse mapping reports deleted-content ambiguity at collapse points', () => {
  const doc = TextDocument.fromText('aa111bb222cc');
  const set = SelectionSet.of(Selection.cursor(1, 6)); // inside the span to delete
  const { mapping } = doc.applyEdit(4, 4, ''); // delete [4,8)
  const { set: mapped } = set.mapForward(mapping);
  assert.equal(mapped.selections[0].anchor.offset, 4, 'cursor crushed onto collapse point');

  const { ambiguities } = mapped.mapBackward(mapping);
  const offsetReport = ambiguities.find((a) => a.kind === 'offset');
  assert.ok(offsetReport && offsetReport.kind === 'offset');
  assert.equal(offsetReport.selectionId, 1);
  assert.deepEqual(offsetReport.sources, [{ kind: 'deleted', start: 4, end: 8 }]);
});

test('branch undo restores the original multiple selections exactly', () => {
  const h = new History();
  const doc = TextDocument.fromText('aa111bb222cc');
  const original = SelectionSet.of(Selection.create(1, 5, 1), Selection.create(2, 7, 10));
  const v0 = h.init(doc, original);

  const r = h.commit(4, 4, ''); // deletion merges the two selections
  assert.equal(r.merges.length, 1);
  assert.equal(h.selectionsAt(r.version).selections.length, 1);

  const restored = h.checkout(v0);
  assert.equal(restored.selections.length, 2, 'both selections come back');
  const s1 = restored.get(1);
  const s2 = restored.get(2);
  assert.ok(s1 && s2);
  assert.equal(s1.direction, 'backward');
  assert.deepEqual([s1.anchor.offset, s1.active.offset], [5, 1]);
  assert.equal(s2.direction, 'forward');
  assert.deepEqual([s2.anchor.offset, s2.active.offset], [7, 10]);
  assert.equal(s2.active.affinity, 'before', 'zero-width stickiness restored');
});

test('undo restores two cursors that were crushed together', () => {
  const h = new History();
  const v0 = h.init(
    TextDocument.fromText('aaXXbb'),
    SelectionSet.of(Selection.cursor(1, 2), Selection.cursor(2, 4)),
  );
  const r = h.commit(2, 2, '');
  assert.equal(h.selectionsAt(r.version).selections.length, 1);

  const restored = h.checkout(v0);
  assert.equal(restored.selections.length, 2);
  assert.deepEqual(
    restored.selections.map((s) => [s.id, s.anchor.offset]),
    [
      [1, 2],
      [2, 4],
    ],
  );
});

test('selections mapping survives surrogate pairs and CRLF', () => {
  const doc = TextDocument.fromText('\u{1F600}x\r\ny');
  //             offsets:  0..2 emoji, 2 'x', 3 '\r', 4 '\n', 5 'y', len 6
  const set = SelectionSet.of(Selection.create(1, 0, 5));
  const { mapping, edit } = doc.applyEdit(3, 2, ' '); // delete CRLF, insert space
  assert.deepEqual([edit.start, edit.end], [3, 5]);
  const { set: mapped } = set.mapForward(mapping);
  assert.equal(mapped.selections[0].from, 0);
  assert.equal(mapped.selections[0].to, 3, 'active end sticks before the inserted space');

  const stickyAfter = SelectionSet.of(Selection.create(1, 0, 5, 'after', 'after'));
  const { set: mapped2 } = stickyAfter.mapForward(mapping);
  assert.equal(mapped2.selections[0].to, 4, 'after-affinity rides past the inserted space');
});
