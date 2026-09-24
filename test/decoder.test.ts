import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  IncrementalUtf8Decoder,
  Utf8DecodeError,
  type DecodeEvent,
} from '../src/index.ts';

const enc = new TextEncoder();

function decodeAll(bytes: Uint8Array, sizes: number[], strategy: 'strict' | 'replace'): DecodeEvent[] {
  const d = new IncrementalUtf8Decoder(strategy);
  const events: DecodeEvent[] = [];
  let pos = 0;
  for (const s of sizes) {
    events.push(...d.push(bytes.subarray(pos, pos + s)));
    pos += s;
  }
  events.push(...d.finish());
  return events;
}

test('4-byte emoji split into four 1-byte chunks yields exactly one character', () => {
  const d = new IncrementalUtf8Decoder('strict');
  const events: DecodeEvent[] = [];
  for (const b of [0xf0, 0x9f, 0x98, 0x80]) {
    events.push(...d.push(Uint8Array.of(b)));
    assert.ok(events.length === 0 || b === 0x80, 'no event before the sequence completes');
  }
  events.push(...d.finish());
  assert.equal(events.length, 1);
  assert.equal(events[0].kind, 'text');
  assert.equal(events[0].text, '\u{1F600}');
  assert.equal(events[0].u16Length, 2);
  assert.deepEqual([events[0].byteStart, events[0].byteEnd], [0, 4]);
  assert.equal(d.u16Offset, 2);
});

test('truncated sequence is only determined at finish()', () => {
  const d = new IncrementalUtf8Decoder('replace');
  const early = d.push(Uint8Array.of(0xe2, 0x82));
  assert.equal(early.length, 0, 'no error and no output before finish');
  const fin = d.finish();
  assert.equal(fin.length, 1);
  assert.equal(fin[0].kind, 'replacement');
  assert.equal(fin[0].text, '�');
  assert.deepEqual([fin[0].byteStart, fin[0].byteEnd], [0, 2]);
});

test('strict mode reports truncated sequence at finish with absolute position', () => {
  const d = new IncrementalUtf8Decoder('strict');
  d.push(enc.encode('ab'));
  d.push(Uint8Array.of(0xf0, 0x9f));
  assert.throws(
    () => d.finish(),
    (e: unknown) =>
      e instanceof Utf8DecodeError &&
      e.kind === 'truncated' &&
      e.byteOffset === 2 &&
      e.sequence.length === 2,
  );
});

test('strict error reports absolute byte position across chunk boundaries', () => {
  const d = new IncrementalUtf8Decoder('strict');
  d.push(enc.encode('hello'));
  d.push(Uint8Array.of(0xc3)); // incomplete é, no error yet
  assert.throws(
    () => d.push(Uint8Array.of(0x41)),
    (e: unknown) =>
      e instanceof Utf8DecodeError &&
      e.kind === 'invalid-continuation' &&
      e.byteOffset === 5 &&
      e.sequence.length === 1,
  );
});

test('invalid lead byte position is absolute in strict mode', () => {
  const d = new IncrementalUtf8Decoder('strict');
  d.push(enc.encode('abc'));
  assert.throws(
    () => d.push(Uint8Array.of(0xff)),
    (e: unknown) => e instanceof Utf8DecodeError && e.kind === 'invalid-lead' && e.byteOffset === 3,
  );
});

test('replace consumption: maximal valid prefix replaced once, offending byte re-examined', () => {
  // 61 E2 82 41 -> 'a' + U+FFFD (covers E2 82) + 'A'
  const events = decodeAll(Uint8Array.of(0x61, 0xe2, 0x82, 0x41), [4], 'replace');
  assert.deepEqual(
    events.map((e) => [e.kind, e.byteStart, e.byteEnd, e.text]),
    [
      ['text', 0, 1, 'a'],
      ['replacement', 1, 3, '�'],
      ['text', 3, 4, 'A'],
    ],
  );
  // ED A0 80 (surrogate) -> three replacements, one per byte
  const s = decodeAll(Uint8Array.of(0xed, 0xa0, 0x80), [3], 'replace');
  assert.equal(s.filter((e) => e.kind === 'replacement').length, 3);
  assert.equal(s.map((e) => e.text).join(''), '���');
});

test('chunk boundaries never change events, text, or the offset map', () => {
  const text = 'héllo 😀 world\r\nnext\nline € end';
  const bytes = enc.encode(text);
  const reference = decodeAll(bytes, [bytes.length], 'replace');
  const refShape = JSON.stringify(reference.map((e) => [e.kind, e.byteStart, e.byteEnd, e.u16Start, e.text]));
  for (let size = 1; size <= bytes.length; size++) {
    const sizes: number[] = [];
    for (let p = 0; p < bytes.length; p += size) sizes.push(Math.min(size, bytes.length - p));
    const events = decodeAll(bytes, sizes, 'replace');
    assert.equal(JSON.stringify(events.map((e) => [e.kind, e.byteStart, e.byteEnd, e.u16Start, e.text])), refShape, `chunk size ${size}`);
    assert.equal(events.map((e) => e.text).join(''), text);
  }
});

test('LF and CRLF line breaks decode and map correctly', () => {
  const bytes = enc.encode('a\r\nb\nc');
  const events = decodeAll(bytes, [2, 2, 2], 'strict');
  assert.equal(events.map((e) => e.text).join(''), 'a\r\nb\nc');
  const d = new IncrementalUtf8Decoder('strict');
  d.push(bytes);
  d.finish();
  const map = d.map;
  // 'a'=0, '\r'=1, '\n'=2, 'b'=3, '\n'=4, 'c'=5 in both spaces (all ASCII).
  for (let i = 0; i <= 6; i++) {
    assert.equal(map.byteToU16(i), i);
    assert.equal(map.u16ToByte(i), i);
  }
  assert.equal(map.byteLength, 6);
  assert.equal(map.u16Length, 6);
});

test('push after finish throws', () => {
  const d = new IncrementalUtf8Decoder('replace');
  d.finish();
  assert.throws(() => d.push(Uint8Array.of(0x61)), /finished/);
});
