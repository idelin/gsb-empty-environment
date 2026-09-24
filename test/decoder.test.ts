import test from 'node:test';
import assert from 'node:assert/strict';
import {
  IncrementalUtf8Decoder,
  Utf8DecodeError,
  MAP_BLOCK_SIZE,
  type Checkpoint,
} from '../src/index.ts';

const encoder = new TextEncoder();

function decodeWhole(bytes: number[] | Uint8Array, strategy: 'strict' | 'replace') {
  const decoder = new IncrementalUtf8Decoder({ strategy });
  decoder.push(bytes instanceof Uint8Array ? bytes : Uint8Array.from(bytes));
  return decoder.finish();
}

test('ascii and multibyte text decodes the same chunked or whole', () => {
  const text = 'Hello, 世界! café 😀';
  const bytes = encoder.encode(text);
  const whole = decodeWhole(bytes, 'strict');
  assert.equal(whole.text, text);
  // One byte per chunk.
  const decoder = new IncrementalUtf8Decoder({ strategy: 'strict' });
  for (const byte of bytes) decoder.push(Uint8Array.of(byte));
  assert.equal(decoder.finish().text, text);
});

test('4-byte emoji split into 4 chunks still yields exactly one character', () => {
  const bytes = encoder.encode('😀'); // F0 9F 98 80
  assert.equal(bytes.length, 4);
  const decoder = new IncrementalUtf8Decoder({ strategy: 'strict' });
  for (const byte of bytes) decoder.push(Uint8Array.of(byte));
  const result = decoder.finish();
  assert.equal(result.text, '😀');
  assert.equal([...result.text].length, 1);
  assert.equal(result.utf16Length, 2);
  // Every byte of the emoji maps to the same UTF-16 start; the pair is atomic.
  for (let b = 0; b < 4; b++) assert.equal(decoder.map.byteToUtf16(b), 0);
  assert.equal(decoder.map.byteToUtf16(4), 2);
  assert.equal(decoder.map.utf16ToByte(1), 0, 'must not split the surrogate pair');
  assert.equal(decoder.map.utf16ToByte(2), 4);
});

test('truncated sequence is only resolved at finish (strict reports absolute offset)', () => {
  const decoder = new IncrementalUtf8Decoder({ strategy: 'strict' });
  decoder.push(encoder.encode('ab'));
  decoder.push(Uint8Array.of(0xf0, 0x9f)); // no error yet: prefix of a 4-byte char
  assert.equal(decoder.text, 'ab');
  assert.throws(() => decoder.finish(), (error: unknown) => {
    assert.ok(error instanceof Utf8DecodeError);
    assert.equal(error.kind, 'truncated');
    assert.equal(error.byteOffset, 2); // absolute offset of the sequence start
    assert.equal(error.detectedAt, 4);
    return true;
  });
});

test('truncated sequence in replace mode yields one U+FFFD at finish', () => {
  const decoder = new IncrementalUtf8Decoder({ strategy: 'replace' });
  decoder.push(encoder.encode('ab'));
  decoder.push(Uint8Array.of(0xf0, 0x9f));
  const result = decoder.finish();
  assert.equal(result.text, 'ab�');
  const range = decoder.map.byteRangeToTextRange(2, 4);
  assert.deepEqual(range.covered, [
    { byteStart: 2, byteEnd: 4, textStart: 2, textEnd: 3, reason: 'replacement' },
  ]);
  assert.equal(range.collapsed, true);
});

test('strict mode reports absolute byte positions across chunk boundaries', () => {
  const decoder = new IncrementalUtf8Decoder({ strategy: 'strict' });
  decoder.push(encoder.encode('abc'));
  decoder.push(Uint8Array.of(0xe2, 0x82)); // valid prefix of U+20AC
  assert.throws(() => decoder.push(Uint8Array.of(0x41)), (error: unknown) => {
    assert.ok(error instanceof Utf8DecodeError);
    assert.equal(error.kind, 'invalid');
    assert.equal(error.byteOffset, 3); // start of the ill-formed sequence
    assert.equal(error.detectedAt, 5); // offending byte
    return true;
  });
});

test('replace mode consumption matches TextDecoder for ill-formed sequences', () => {
  const reference = new TextDecoder('utf-8', { fatal: false });
  const cases: number[][] = [
    [0x80],
    [0xbf, 0xbf],
    [0xc0, 0x80],
    [0xc1, 0xbf],
    [0xf5, 0x80, 0x80, 0x80],
    [0xed, 0xa0, 0x80], // surrogate halves
    [0xe0, 0x80, 0x80], // overlong
    [0xf0, 0x8f, 0x80, 0x80], // overlong
    [0xf4, 0x90, 0x80, 0x80], // > U+10FFFF
    [0xf0, 0x41, 0x42], // bad continuation, then ASCII
    [0xe2, 0x82, 0x41],
    [0x61, 0xf0, 0x9f, 0x62], // truncated char mid-stream
  ];
  for (const bytes of cases) {
    const expected = reference.decode(Uint8Array.from(bytes));
    // Whole-buffer and byte-by-byte must agree with the reference.
    assert.equal(decodeWhole(bytes, 'replace').text, expected, `whole: ${bytes}`);
    const decoder = new IncrementalUtf8Decoder({ strategy: 'replace' });
    for (const byte of bytes) decoder.push(Uint8Array.of(byte));
    assert.equal(decoder.finish().text, expected, `chunked: ${bytes}`);
  }
});

test('mapping stays monotonic and consistent under replacement', () => {
  const decoder = new IncrementalUtf8Decoder({ strategy: 'replace' });
  decoder.push(Uint8Array.from([0x61, 0x80, 0x80, 0x62, 0xf0, 0x9f, 0x98, 0x80]));
  const result = decoder.finish();
  assert.equal(result.text, 'a��b😀');
  let previous = -1;
  for (let b = 0; b <= result.byteLength; b++) {
    const u = decoder.map.byteToUtf16(b);
    assert.ok(u >= previous, `monotonic at byte ${b}`);
    previous = u;
  }
  assert.equal(decoder.map.byteToUtf16(result.byteLength), result.utf16Length);
  assert.equal(decoder.map.utf16ToByte(result.utf16Length), result.byteLength);
});

test('bidirectional range queries with replacement collapse intervals', () => {
  // 'A' emoji 'B' <0x80 0x80 -> two stray bytes> 'C'
  const bytes = Uint8Array.from([
    0x41, 0xf0, 0x9f, 0x98, 0x80, 0x42, 0x80, 0x80, 0x43,
  ]);
  const decoder = new IncrementalUtf8Decoder({ strategy: 'replace' });
  decoder.push(bytes);
  const result = decoder.finish();
  // Each stray continuation byte is its own ill-formed sequence: two U+FFFD.
  assert.equal(result.text, 'A😀B��C');

  const range = decoder.map.byteRangeToTextRange(6, 8);
  assert.equal(range.start, 4);
  assert.equal(range.end, 6);
  assert.deepEqual(range.covered, []);
  // The emoji is a 4->2 many-to-one unit.
  const emoji = decoder.map.byteRangeToTextRange(1, 5);
  assert.deepEqual(emoji, {
    start: 1,
    end: 3,
    collapsed: true,
    covered: [{ byteStart: 1, byteEnd: 5, textStart: 1, textEnd: 3, reason: 'multibyte' }],
  });
  // A truncated 3-byte sequence replaced by ONE char is an explicit collapse.
  const decoder2 = new IncrementalUtf8Decoder({ strategy: 'replace' });
  decoder2.push(Uint8Array.from([0x58, 0xe2, 0x82]));
  decoder2.finish();
  const collapsed = decoder2.map.byteRangeToTextRange(1, 3);
  assert.equal(collapsed.collapsed, true);
  assert.deepEqual(collapsed.covered, [
    { byteStart: 1, byteEnd: 3, textStart: 1, textEnd: 2, reason: 'replacement' },
  ]);
});

test('text ranges snap to character boundaries (no surrogate splitting)', () => {
  const decoder = new IncrementalUtf8Decoder({ strategy: 'strict' });
  decoder.push(encoder.encode('A😀B'));
  decoder.finish();
  // Asking for the inside of the surrogate pair snaps outward to the emoji.
  const range = decoder.map.textRangeToByteRange(2, 2);
  assert.deepEqual(range, { byteStart: 1, byteEnd: 5, textStart: 1, textEnd: 3 });
  const right = decoder.map.textRangeToByteRange(1, 2);
  assert.deepEqual(right, { byteStart: 1, byteEnd: 5, textStart: 1, textEnd: 3 });
});

test('LF and CRLF line endings map byte<->text exactly', () => {
  const text = 'a\r\nb\nc\r\n';
  const bytes = encoder.encode(text);
  const decoder = new IncrementalUtf8Decoder({ strategy: 'strict' });
  decoder.push(bytes.subarray(0, 3));
  decoder.push(bytes.subarray(3));
  const result = decoder.finish();
  assert.equal(result.text, text);
  assert.equal(result.byteLength, result.utf16Length); // all ASCII here
  for (let offset = 0; offset <= result.byteLength; offset++) {
    assert.equal(decoder.map.byteToUtf16(offset), offset);
    assert.equal(decoder.map.utf16ToByte(offset), offset);
  }
  // CRLF stays two addressable units; ranges over it round-trip.
  assert.deepEqual(decoder.map.byteRangeToTextRange(1, 3), {
    start: 1,
    end: 3,
    collapsed: false,
    covered: [],
  });
  assert.deepEqual(decoder.map.textRangeToByteRange(1, 3), {
    byteStart: 1,
    byteEnd: 3,
    textStart: 1,
    textEnd: 3,
  });
});

test('mixed script + newlines: every offset round-trips', () => {
  const text = '标题\r\n第二行 café 😀\nend';
  const bytes = encoder.encode(text);
  const decoder = new IncrementalUtf8Decoder({ strategy: 'strict' });
  for (const byte of bytes) decoder.push(Uint8Array.of(byte));
  const result = decoder.finish();
  // Reference map built independently from per-character encoding.
  const expectedByte: number[] = [];
  const expectedUtf16: number[] = [];
  let bytePos = 0;
  let utf16Pos = 0;
  for (const char of text) {
    const byteLen = encoder.encode(char).length;
    const utf16Len = char.length;
    for (let k = 0; k < byteLen; k++) expectedByte[bytePos + k] = utf16Pos;
    for (let k = 0; k < utf16Len; k++) expectedUtf16[utf16Pos + k] = bytePos;
    bytePos += byteLen;
    utf16Pos += utf16Len;
  }
  expectedByte[bytePos] = utf16Pos;
  expectedUtf16[utf16Pos] = bytePos;
  for (let b = 0; b <= bytePos; b++) {
    assert.equal(decoder.map.byteToUtf16(b), expectedByte[b], `byte ${b}`);
  }
  for (let u = 0; u <= utf16Pos; u++) {
    assert.equal(decoder.map.utf16ToByte(u), expectedUtf16[u], `utf16 ${u}`);
  }
});

test('checkpoint survives JSON round-trip and resumes mid-sequence', () => {
  const prefix = Uint8Array.from([0x41, 0xf0, 0x9f]); // 'A' + half an emoji
  const decoder = new IncrementalUtf8Decoder({ strategy: 'strict' });
  decoder.push(prefix);
  const checkpoint: Checkpoint = JSON.parse(JSON.stringify(decoder.checkpoint()));
  const resumed = IncrementalUtf8Decoder.resume(checkpoint, prefix);
  resumed.push(Uint8Array.of(0x98, 0x80)); // complete the emoji
  resumed.push(encoder.encode('!'));
  const result = resumed.finish();
  assert.equal(result.text, 'A😀!');
  assert.equal(result.byteLength, 6);
  assert.equal(resumed.map.byteToUtf16(1), 1);
  assert.equal(resumed.map.byteToUtf16(5), 3);
});

test('resume rejects a different stream via prefix digest', () => {
  const decoder = new IncrementalUtf8Decoder({ strategy: 'replace' });
  decoder.push(encoder.encode('stream-one'));
  const checkpoint = decoder.checkpoint();
  assert.throws(
    () => IncrementalUtf8Decoder.resume(checkpoint, encoder.encode('stream-two')),
    /digest mismatch/,
  );
  assert.throws(
    () => IncrementalUtf8Decoder.resume(checkpoint, encoder.encode('stream-on')),
    /digest mismatch|refusing/,
  );
  // Same prefix resumes fine and continues the original stream.
  const resumed = IncrementalUtf8Decoder.resume(checkpoint, encoder.encode('stream-one'));
  resumed.push(encoder.encode('+tail'));
  assert.equal(resumed.finish().text, 'stream-one+tail');
});

test('random access queries scan at most one block (no full-prefix rescan)', () => {
  const decoder = new IncrementalUtf8Decoder({ strategy: 'strict' });
  const chunk = encoder.encode('aé😀\n');
  for (let i = 0; i < 5000; i++) decoder.push(chunk);
  const result = decoder.finish();
  assert.ok(decoder.map.blockCount > 10, 'mapping is split into many blocks');
  // Query near the very end: only the tail block may be scanned.
  decoder.map.byteToUtf16(result.byteLength - 1);
  assert.ok(
    decoder.map.lastScanSize <= MAP_BLOCK_SIZE,
    `scanned ${decoder.map.lastScanSize} units`,
  );
  decoder.map.utf16ToByte(result.utf16Length - 1);
  assert.ok(decoder.map.lastScanSize <= MAP_BLOCK_SIZE);
  // Spot-check correctness against an independently computed expectation.
  // Each chunk is 'a'(1/1) 'é'(2/1) '😀'(4/2) '\n'(1/1) = 8 bytes / 5 utf16.
  const chunkIndex = 3210;
  const byteOffset = chunkIndex * 8 + 3; // inside the emoji of that chunk
  assert.equal(decoder.map.byteToUtf16(byteOffset), chunkIndex * 5 + 2);
});

test('empty input and empty chunks are valid', () => {
  const decoder = new IncrementalUtf8Decoder({ strategy: 'strict' });
  decoder.push(new Uint8Array(0));
  const result = decoder.finish();
  assert.equal(result.text, '');
  assert.equal(decoder.map.byteToUtf16(0), 0);
  assert.equal(decoder.map.utf16ToByte(0), 0);
});
