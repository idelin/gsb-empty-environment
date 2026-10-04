import { SourceText } from '../src/sourceText';
import { test, assert, assertDeepEqual, fail } from './harness';

test('utf16 <-> scalar bidirectional mapping with surrogate pairs', () => {
  // a(U+0061) 😀(U+1F600, surrogate pair) b 中 c
  const src = new SourceText('a😀b中c');
  assert(src.scalarLength === 5, 'scalar length');
  assert(src.utf16Length === 6, 'utf16 length');

  // scalar index -> utf16 offset
  assertDeepEqual(
    [0, 1, 2, 3, 4, 5].map((i) => src.scalarIndexToUtf16Offset(i)),
    [0, 1, 3, 4, 5, 6],
    'scalarIndexToUtf16Offset',
  );

  // utf16 offset -> scalar index (round trip on every scalar boundary)
  for (let i = 0; i <= src.scalarLength; i++) {
    const offset = src.scalarIndexToUtf16Offset(i);
    assert(src.utf16OffsetToScalarIndex(offset) === i, `round trip at scalar ${i}`);
  }

  // offset 2 lands inside the 😀 surrogate pair -> must throw
  let threw = false;
  try {
    src.utf16OffsetToScalarIndex(2);
  } catch {
    threw = true;
  }
  assert(threw, 'mid-surrogate offset must throw');
});

test('sliceScalars returns original astral characters', () => {
  const src = new SourceText('x😀y');
  assert(src.sliceScalars(1, 2) === '😀', 'astral slice');
  assert(src.sliceUtf16(1, 3) === '😀', 'utf16 slice');
  assert(src.toString() === 'x😀y', 'toString round trip');
});

test('out-of-range mapping throws', () => {
  const src = new SourceText('ab');
  for (const fn of [
    () => src.scalarIndexToUtf16Offset(3),
    () => src.utf16OffsetToScalarIndex(3),
    () => src.scalarAt(2),
  ]) {
    let threw = false;
    try {
      fn();
    } catch (e) {
      threw = e instanceof RangeError;
    }
    if (!threw) fail('expected RangeError');
  }
});
