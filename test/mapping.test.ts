import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Mapping } from '../src/mapping.ts';
import { TextDocument } from '../src/document.ts';

test('offset mapping: insert, delete, replace', () => {
  const ins = Mapping.fromEdit({ start: 2, deletedLen: 0, insertedLen: 3 });
  assert.equal(ins.mapOffset(1), 1);
  assert.equal(ins.mapOffset(2, 'before'), 2);
  assert.equal(ins.mapOffset(2, 'after'), 5);
  assert.equal(ins.mapOffset(7), 10);

  const del = Mapping.fromEdit({ start: 2, deletedLen: 4, insertedLen: 0 });
  assert.equal(del.mapOffset(1), 1);
  assert.equal(del.mapOffset(3, 'before'), 2);
  assert.equal(del.mapOffset(3, 'after'), 2);
  assert.equal(del.mapOffset(6, 'before'), 2);
  assert.equal(del.mapOffset(7), 3);

  const rep = Mapping.fromEdit({ start: 2, deletedLen: 4, insertedLen: 2 });
  assert.equal(rep.mapOffset(4, 'before'), 2);
  assert.equal(rep.mapOffset(4, 'after'), 4);
  assert.equal(rep.mapOffset(6, 'after'), 4);
  assert.equal(rep.mapOffset(8), 6);
});

test('composition maps across a chain of edits and is structural', () => {
  const a = Mapping.fromEdit({ start: 1, deletedLen: 0, insertedLen: 2 });
  const b = Mapping.fromEdit({ start: 4, deletedLen: 3, insertedLen: 1 });
  const c = a.compose(b);
  assert.equal(c.steps.length, 2, 'composition keeps steps, not just final offsets');
  // manual fold
  for (const [offset, affinity] of [[0, 'before'], [1, 'after'], [5, 'before'], [9, 'after']] as const) {
    const expect = b.mapOffset(a.mapOffset(offset, affinity), affinity);
    assert.equal(c.mapOffset(offset, affinity), expect);
  }
});

test('inversion reverses composition: (a.compose(b)).invert == b.invert.compose(a.invert)', () => {
  const a = Mapping.fromEdit({ start: 1, deletedLen: 2, insertedLen: 4 });
  const b = Mapping.fromEdit({ start: 0, deletedLen: 1, insertedLen: 3 });
  const lhs = a.compose(b).invert();
  const rhs = b.invert().compose(a.invert());
  assert.deepEqual(lhs.steps, rhs.steps);
});

test('round trip: offsets outside deleted spans survive forward+backward', () => {
  const m = Mapping.fromEdits([
    { start: 3, deletedLen: 2, insertedLen: 5 },
    { start: 0, deletedLen: 0, insertedLen: 1 },
    { start: 10, deletedLen: 4, insertedLen: 0 },
  ]);
  const inv = m.invert();
  // [3,5] is step 1's replaced span (boundaries collapse by affinity);
  // 6..10 land inside step 3's deleted span. Only truly surviving offsets round-trip.
  for (const o of [0, 1, 2, 11, 14, 20]) {
    for (const affinity of ['before', 'after'] as const) {
      const fwd = m.mapOffset(o, affinity);
      assert.equal(inv.mapOffset(fwd, affinity), o, `offset ${o} affinity ${affinity}`);
      assert.equal(m.mapOffsetBack(fwd, affinity).offset, o);
    }
  }
});

test('mapOffsetBack offset agrees with invert().mapOffset on random inputs', () => {
  const m = Mapping.fromEdits([
    { start: 2, deletedLen: 3, insertedLen: 4 },
    { start: 6, deletedLen: 1, insertedLen: 2 },
  ]);
  const inv = m.invert();
  for (let p = 0; p <= 12; p++) {
    for (const affinity of ['before', 'after'] as const) {
      assert.equal(m.mapOffsetBack(p, affinity).offset, inv.mapOffset(p, affinity));
    }
  }
});

test('reverse mapping reports ambiguity sources for deleted content', () => {
  const m = Mapping.fromEdit({ start: 2, deletedLen: 4, insertedLen: 1 });
  const r = m.mapOffsetBack(2);
  assert.equal(r.ambiguous, true);
  assert.deepEqual(r.sources, [{ kind: 'deleted', start: 2, end: 6 }]);

  const exact = m.mapOffsetBack(10);
  assert.equal(exact.ambiguous, false);
  assert.deepEqual(exact.sources, []);
});

test('reverse mapping marks offsets inside inserted text as foreign', () => {
  const m = Mapping.fromEdit({ start: 3, deletedLen: 0, insertedLen: 4 });
  const r = m.mapOffsetBack(5);
  assert.equal(r.ambiguous, true);
  assert.deepEqual(r.sources, [{ kind: 'inserted', start: 3, end: 7 }]);
});

test('ambiguity sources compose back to the oldest version coordinates', () => {
  const a = Mapping.fromEdit({ start: 5, deletedLen: 3, insertedLen: 0 }); // delete [5,8)
  const b = Mapping.fromEdit({ start: 0, deletedLen: 0, insertedLen: 10 }); // insert 10 at 0
  const m = a.compose(b);
  const r = m.mapOffsetBack(15); // collapse point of the deletion, shifted by insert
  assert.equal(r.ambiguous, true);
  assert.deepEqual(r.sources, [{ kind: 'deleted', start: 5, end: 8 }]);
  assert.equal(r.offset, 5);
});

test('document edits never split surrogate pairs', () => {
  const doc = TextDocument.fromText('a\u{1F600}b'); // len 4, emoji at [1,3)
  assert.equal(doc.length, 4);

  const left = doc.applyEdit(2, 1, ''); // cuts the emoji in half
  assert.deepEqual([left.edit.start, left.edit.end], [1, 3]);
  assert.equal(left.doc.text(), 'ab');

  const right = doc.applyEdit(1, 1, 'x'); // delete starting at pair lead
  assert.deepEqual([right.edit.start, right.edit.end], [1, 3]);
  assert.equal(right.doc.text(), 'axb');

  const ins = doc.applyEdit(2, 0, 'zz'); // insert inside the pair
  assert.equal(ins.edit.start, 1); // snapped outwards to the pair boundary
  assert.equal(ins.doc.text(), 'azz\u{1F600}b');
});

test('LF and CRLF are plain atoms; edits map across both', () => {
  const doc = TextDocument.fromText('a\nb\r\nc');
  assert.equal(doc.length, 6);
  const { doc: d2, mapping } = doc.applyEdit(0, 3, ''); // remove 'a\nb'
  assert.equal(d2.text(), '\r\nc');
  assert.equal(mapping.mapOffset(4), 1); // '\n' of CRLF now at 1
  const { doc: d3, mapping: m2 } = d2.applyEdit(1, 1, ''); // remove '\n' of CRLF
  assert.equal(d3.text(), '\rc');
  assert.equal(m2.mapOffset(2), 1);
});
