import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ScalarText } from '../src/scalar-text.ts';

test('BMP-only text maps identically in both directions', () => {
  const text = ScalarText.from('hello world');
  assert.equal(text.scalarLength, 11);
  assert.equal(text.utf16Length, 11);
  for (let i = 0; i <= 11; i++) {
    assert.equal(text.scalarOffsetToUtf16(i), i);
    assert.equal(text.utf16OffsetToScalar(i), i);
  }
});

test('astral scalars occupy two UTF-16 units', () => {
  // a, U+1F600, b, U+10000, c
  const text = ScalarText.from('a\u{1F600}b\u{10000}c');
  assert.equal(text.scalarLength, 5);
  assert.equal(text.utf16Length, 7);

  // scalar -> utf16
  assert.deepEqual(
    [0, 1, 2, 3, 4, 5].map((i) => text.scalarOffsetToUtf16(i)),
    [0, 1, 3, 4, 6, 7],
  );
  // utf16 -> scalar (boundaries only)
  assert.equal(text.utf16OffsetToScalar(0), 0);
  assert.equal(text.utf16OffsetToScalar(1), 1);
  assert.equal(text.utf16OffsetToScalar(3), 2);
  assert.equal(text.utf16OffsetToScalar(4), 3);
  assert.equal(text.utf16OffsetToScalar(6), 4);
  assert.equal(text.utf16OffsetToScalar(7), 5);
});

test('utf16OffsetToScalar rejects offsets inside a surrogate pair', () => {
  const text = ScalarText.from('a\u{1F600}b');
  assert.equal(text.isUtf16Boundary(1), true);
  assert.equal(text.isUtf16Boundary(2), false);
  assert.throws(() => text.utf16OffsetToScalar(2), /splits a surrogate pair/);
});

test('mapping is a round-trip inverse on all boundaries', () => {
  const text = ScalarText.from('x\u{1F600}\u{10000}\u4E2dy\u{1F4A9}z');
  for (let s = 0; s <= text.scalarLength; s++) {
    assert.equal(text.utf16OffsetToScalar(text.scalarOffsetToUtf16(s)), s);
  }
  for (let u = 0; u <= text.utf16Length; u++) {
    if (text.isUtf16Boundary(u)) {
      assert.equal(text.scalarOffsetToUtf16(text.utf16OffsetToScalar(u)), u);
    }
  }
});

test('lone surrogates are sanitized to U+FFFD', () => {
  const text = ScalarText.from('\uD800x\uDC00');
  assert.deepEqual([...text.scalars], [0xfffd, 0x78, 0xfffd]);
  assert.equal(text.toString(), '\uFFFDx\uFFFD');
});

test('out-of-range offsets throw', () => {
  const text = ScalarText.from('ab');
  assert.throws(() => text.scalarOffsetToUtf16(3), RangeError);
  assert.throws(() => text.scalarOffsetToUtf16(-1), RangeError);
  assert.throws(() => text.utf16OffsetToScalar(3), RangeError);
});

test('line/character positions use UTF-16 columns', () => {
  // line 0: a <emoji> b ; line 1: <CJK> <CJK>
  const text = ScalarText.from('a\u{1F600}b\n\u4E2D\u6587');
  assert.equal(text.lineCount, 2);

  // 'a' at 0, emoji at units 1-2, 'b' at unit 3, '\n' at 4.
  assert.deepEqual(text.utf16OffsetToPosition(0), { line: 0, character: 0 });
  assert.deepEqual(text.utf16OffsetToPosition(3), { line: 0, character: 3 });
  assert.deepEqual(text.utf16OffsetToPosition(5), { line: 1, character: 0 });
  assert.deepEqual(text.utf16OffsetToPosition(6), { line: 1, character: 1 });

  assert.equal(text.positionToUtf16Offset({ line: 0, character: 3 }), 3);
  assert.equal(text.positionToUtf16Offset({ line: 1, character: 1 }), 6);

  // Inside the emoji: not a valid position.
  assert.throws(() => text.positionToUtf16Offset({ line: 0, character: 2 }), /surrogate pair/);
  // Past end of line.
  assert.throws(() => text.positionToUtf16Offset({ line: 0, character: 5 }), /past the end/);
  assert.throws(() => text.positionToUtf16Offset({ line: 2, character: 0 }), RangeError);
});

test('CRLF and lone CR act as line terminators', () => {
  const text = ScalarText.from('a\r\nb\rc');
  assert.equal(text.lineCount, 3);
  assert.deepEqual(text.utf16OffsetToPosition(3), { line: 1, character: 0 });
  assert.deepEqual(text.utf16OffsetToPosition(5), { line: 2, character: 0 });
});

test('toString round-trips the source', () => {
  const source = 'let \u53D8\u91CF = "\u{1F4A9}"; // \u{1F600}\n';
  assert.equal(ScalarText.from(source).toString(), source);
});
