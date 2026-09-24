import { test } from 'node:test';
import assert from 'node:assert/strict';
import { IncrementalUtf8Decoder, OffsetMap } from '../src/index.ts';

const enc = new TextEncoder();

// Content: 'A' 'é' '€' '😀' <invalid E2 82> 'x' 'y' CR LF 'z' LF
// bytes:  0:A  1-2:é  3-5:€  6-9:😀  10-11:repl  12:x 13:y 14:CR 15:LF 16:z 17:LF
// u16:    0:A  1:é   2:€   3-4:😀   5:repl      6:x  7:y  8:CR 9:LF 10:z 11:LF
function buildMap(): OffsetMap {
  const d = new IncrementalUtf8Decoder('replace');
  const head = enc.encode('Aé€😀');
  const tail = enc.encode('xy\r\nz\n');
  const bytes = new Uint8Array(head.length + 2 + tail.length);
  bytes.set(head, 0);
  bytes.set(Uint8Array.of(0xe2, 0x82), head.length);
  bytes.set(tail, head.length + 2);
  d.push(bytes.subarray(0, 7)); // split inside the emoji and the invalid sequence
  d.push(bytes.subarray(7));
  d.finish();
  return d.map;
}

test('byteToU16 maps boundaries exactly and interiors to the character start', () => {
  const map = buildMap();
  const expected: Array<[number, number]> = [
    [0, 0], [1, 1], [2, 1], [3, 2], [4, 2], [5, 2], [6, 3], [7, 3], [8, 3], [9, 3],
    [10, 5], [11, 5], [12, 6], [13, 7], [14, 8], [15, 9], [16, 10], [17, 11], [18, 12],
  ];
  for (const [byte, u16] of expected) {
    assert.equal(map.byteToU16(byte), u16, `byteToU16(${byte})`);
  }
  assert.equal(map.byteToU16(100), 12, 'clamps past the end');
  assert.equal(map.byteToU16(-5), 0);
});

test('u16ToByte maps boundaries exactly and never splits a surrogate pair', () => {
  const map = buildMap();
  const expected: Array<[number, number]> = [
    [0, 0], [1, 1], [2, 3], [3, 6], [4, 6], [5, 10], [6, 12], [7, 13],
    [8, 14], [9, 15], [10, 16], [11, 17], [12, 18],
  ];
  for (const [u16, byte] of expected) {
    assert.equal(map.u16ToByte(u16), byte, `u16ToByte(${u16})`);
  }
  // u16 offset 4 is the middle of the 😀 surrogate pair: snaps to byte 6.
  assert.equal(map.u16ToByte(4), 6, 'surrogate pair is never split');
});

test('byte range query snaps outward and reports many-to-one coverage explicitly', () => {
  const map = buildMap();
  // Bytes 9..11 cut through 😀 (6..10) and the replacement (10..12).
  const r = map.byteRangeToU16(9, 11);
  assert.deepEqual({ start: r.start, end: r.end }, { start: 3, end: 6 });
  assert.deepEqual(r.replacements, [{ byteStart: 10, byteEnd: 12, u16Start: 5, u16End: 6 }]);
  // A range fully inside valid text has no replacement coverage.
  assert.deepEqual(map.byteRangeToU16(0, 3).replacements, []);
  // Range exactly around the replacement.
  const exact = map.byteRangeToU16(10, 12);
  assert.deepEqual({ start: exact.start, end: exact.end }, { start: 5, end: 6 });
  assert.equal(exact.replacements.length, 1);
});

test('u16 range query snaps outward and never splits the surrogate pair', () => {
  const map = buildMap();
  // u16 3..4 cuts the 😀 pair in half; result must cover the whole pair.
  const r = map.u16RangeToByte(3, 4);
  assert.deepEqual({ start: r.start, end: r.end }, { start: 6, end: 10 });
  // u16 4..6 starts mid-pair and ends after the replacement.
  const r2 = map.u16RangeToByte(4, 6);
  assert.deepEqual({ start: r2.start, end: r2.end }, { start: 6, end: 12 });
  assert.deepEqual(r2.replacements, [{ byteStart: 10, byteEnd: 12, u16Start: 5, u16End: 6 }]);
});

test('mapping is monotonic in both directions', () => {
  const map = buildMap();
  let prevByte = -1;
  let prevU16 = -1;
  for (let i = 0; i <= map.byteLength; i++) {
    const u = map.byteToU16(i);
    assert.ok(u >= prevU16, 'byteToU16 non-decreasing');
    prevU16 = u;
  }
  for (let i = 0; i <= map.u16Length; i++) {
    const b = map.u16ToByte(i);
    assert.ok(b >= prevByte, 'u16ToByte non-decreasing');
    prevByte = b;
  }
});

test('runs are compressed: 100k ASCII characters form a single run', () => {
  const d = new IncrementalUtf8Decoder('strict');
  const chunk = enc.encode('a'.repeat(1000));
  for (let i = 0; i < 100; i++) d.push(chunk);
  d.finish();
  assert.equal(d.map.runCount, 1);
  assert.equal(d.map.byteLength, 100_000);
  assert.equal(d.map.u16Length, 100_000);
  // Random access near the end uses only the map, no re-decode of the prefix.
  assert.equal(d.map.byteToU16(99_999), 99_999);
  assert.equal(d.map.u16ToByte(99_999), 99_999);
});

test('random access on a large mixed stream matches a linear reference', () => {
  const d = new IncrementalUtf8Decoder('replace');
  const pieces = ['ascii ', 'héllo ', '中文测试 ', '😀🎉 ', 'bad:', ''];
  const bytes: number[] = [];
  for (let round = 0; round < 200; round++) {
    for (const p of pieces) bytes.push(...enc.encode(p));
    bytes.push(0xe2, 0x82); // invalid -> one replacement covering 2 bytes
  }
  const all = Uint8Array.from(bytes);
  // Feed in awkward chunk sizes.
  for (let p = 0; p < all.length; p += 997) d.push(all.subarray(p, p + 997));
  d.finish();
  const map = d.map;

  // Independent linear reference, indexed by UTF-16 offset. Interior offsets
  // (the low half of a surrogate pair) snap to the character start.
  const text = new TextDecoder('utf-8').decode(all);
  const u16ToByteRef: number[] = [];
  const u16ToByteEndRef: number[] = [];
  {
    let bytePos = 0;
    let u16Pos = 0;
    for (let i = 0; i < text.length; i++) {
      const code = text.charCodeAt(i);
      let byteLen: number;
      let u16Len = 1;
      if (code >= 0xd800 && code <= 0xdbff) {
        byteLen = 4;
        u16Len = 2;
        i += 1;
      } else if (code === 0xfffd) {
        byteLen = 2; // the fixture's invalid bytes are always the pair E2 82
      } else if (code < 0x80) {
        byteLen = 1;
      } else if (code < 0x800) {
        byteLen = 2;
      } else {
        byteLen = 3;
      }
      u16ToByteRef[u16Pos] = bytePos;
      u16ToByteEndRef[u16Pos] = bytePos + byteLen;
      if (u16Len === 2) {
        u16ToByteRef[u16Pos + 1] = bytePos;
        u16ToByteEndRef[u16Pos + 1] = bytePos + byteLen;
      }
      bytePos += byteLen;
      u16Pos += u16Len;
    }
    u16ToByteRef[u16Pos] = bytePos;
    u16ToByteEndRef[u16Pos] = bytePos;
  }
  assert.equal(map.u16Length, text.length);
  assert.equal(map.byteLength, all.length);
  // Spot-check random positions against the reference.
  let state = 42;
  const rand = (): number => {
    state = (state * 1103515245 + 12345) & 0x7fffffff;
    return state;
  };
  for (let iter = 0; iter < 5000; iter++) {
    const u = rand() % (text.length + 1);
    assert.equal(map.u16ToByte(u), u16ToByteRef[u]);
  }
  for (let iter = 0; iter < 5000; iter++) {
    const b = rand() % (all.length + 1);
    const u = map.byteToU16(b);
    assert.ok(u >= 0 && u <= text.length);
    assert.ok(u16ToByteRef[u] <= b, 'byteToU16 never maps past the query point');
    assert.ok(b === all.length || b < u16ToByteEndRef[u], 'byteToU16 maps into the containing character');
  }
});

test('offset map survives JSON round-trip (checkpoint serialization)', () => {
  const map = buildMap();
  const restored = OffsetMap.fromJSON(JSON.parse(JSON.stringify(map.toJSON())));
  assert.equal(restored.byteLength, map.byteLength);
  assert.equal(restored.u16Length, map.u16Length);
  for (let i = 0; i <= map.byteLength; i++) {
    assert.equal(restored.byteToU16(i), map.byteToU16(i));
  }
  for (let i = 0; i <= map.u16Length; i++) {
    assert.equal(restored.u16ToByte(i), map.u16ToByte(i));
  }
});
