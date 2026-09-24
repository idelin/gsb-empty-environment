import { test } from 'node:test';
import assert from 'node:assert/strict';
import { History } from '../src/history.ts';
import {
  boundaryToUtf16,
  selectionFromUtf16,
  selectionToUtf16,
  utf16ToBoundary,
  mapSelection,
} from '../src/selection.ts';

test('UTF-16 offsets count surrogate pairs as two code units', () => {
  const h = new History('a😀b');
  const v = h.getVersion(h.head);
  // chars: a | 😀 | b  -> utf16 offsets: 0 a 1 😀 3 b 4
  assert.equal(boundaryToUtf16(v, 0), 0);
  assert.equal(boundaryToUtf16(v, 1), 1);
  assert.equal(boundaryToUtf16(v, 2), 3);
  assert.equal(boundaryToUtf16(v, 3), 4);
});

test('an offset inside a surrogate pair snaps down, never splitting it', () => {
  const h = new History('a😀b');
  const v = h.getVersion(h.head);
  assert.equal(utf16ToBoundary(v, 2), 1, 'mid-pair offset snaps to pair start');
  assert.equal(utf16ToBoundary(v, 3), 2, 'pair end is the next boundary');
  const sel = selectionFromUtf16(v, 1, 2, 4);
  const back = selectionToUtf16(v, sel);
  assert.deepEqual(back, { anchor: 1, active: 4 });
});

test('mapping across edits never produces pair-splitting offsets', () => {
  const h = new History('x😀y😀z');
  const v1 = h.head;
  const v2 = h.addVersion({ start: 0, deleteCount: 1, insert: 'QQ' }); // "QQ😀y😀z"
  const m = h.mappingBetween(v1, v2);
  const before = h.getVersion(v1);
  const after = h.getVersion(v2);
  for (let b = 0; b <= before.chars.length; b++) {
    for (const stick of ['left', 'right'] as const) {
      const mapped = mapSelection(
        { id: 1, anchor: { boundary: b, stick }, active: { boundary: b, stick } },
        m,
      );
      const off = selectionToUtf16(after, mapped);
      // round-trip must be exact: offset sits on a code-point boundary
      assert.equal(utf16ToBoundary(after, off.anchor), mapped.anchor.boundary);
    }
  }
});

test('LF line endings: edits and mappings around \\n', () => {
  const h = new History('one\ntwo\nthree');
  const v1 = h.head;
  const sel = selectionFromUtf16(h.getVersion(v1), 7, 4, 8); // "two\n" start..end
  const v2 = h.addVersion({ start: 0, deleteCount: 4 }); // delete "one\n"
  const m = h.mappingBetween(v1, v2);
  const mapped = mapSelection(sel, m);
  assert.deepEqual(selectionToUtf16(h.getVersion(v2), mapped), { anchor: 0, active: 4 });
  assert.equal(h.textOf(v2), 'two\nthree');
});

test('CRLF line endings: \\r and \\n keep separate identities', () => {
  const h = new History('one\r\ntwo\r\nthree');
  const v1 = h.head;
  assert.equal(h.getVersion(v1).chars.length, 15);
  const sel = selectionFromUtf16(h.getVersion(v1), 9, 5, 9); // "two\r"
  const v2 = h.addVersion({ start: 3, deleteCount: 2 }); // delete the first "\r\n"
  const m = h.mappingBetween(v1, v2);
  const mapped = mapSelection(sel, m);
  assert.equal(h.textOf(v2), 'onetwo\r\nthree');
  assert.deepEqual(selectionToUtf16(h.getVersion(v2), mapped), { anchor: 3, active: 7 });
});

test('converting LF to CRLF shifts later selections by one per line', () => {
  const h = new History('a\nb\nc');
  const v1 = h.head;
  const cursor = selectionFromUtf16(h.getVersion(v1), 1, 5, 5); // before "c"
  const v2 = h.addVersion({ start: 1, deleteCount: 1, insert: '\r\n' });
  const v3 = h.addVersion({ start: 4, deleteCount: 1, insert: '\r\n' });
  assert.equal(h.textOf(v3), 'a\r\nb\r\nc');
  const m = h.mappingBetween(v1, v3);
  const mapped = mapSelection(cursor, m);
  assert.deepEqual(selectionToUtf16(h.getVersion(v3), mapped), { anchor: 7, active: 7 });
});
