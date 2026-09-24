import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import {
  CheckpointMismatchError,
  IncrementalUtf8Decoder,
  Utf8DecodeError,
  type DecodeEvent,
} from '../src/index.ts';

const enc = new TextEncoder();
const textOf = (events: DecodeEvent[]): string => events.map((e) => e.text).join('');

const SAMPLE = 'héllo 😀\r\nnext\nline € 中文';
const sampleBytes = enc.encode(SAMPLE);

function oneShot(strategy: 'strict' | 'replace'): { events: DecodeEvent[]; d: IncrementalUtf8Decoder } {
  const d = new IncrementalUtf8Decoder(strategy);
  const events = [...d.push(sampleBytes), ...d.finish()];
  return { events, d };
}

test('checkpoint -> JSON -> resume produces the identical stream', () => {
  const { events: refEvents, d: refDecoder } = oneShot('replace');

  const d1 = new IncrementalUtf8Decoder('replace');
  const head = d1.push(sampleBytes.subarray(0, 9)); // cuts inside the emoji
  const checkpoint = JSON.parse(JSON.stringify(d1.checkpoint()));

  const d2 = IncrementalUtf8Decoder.resume(checkpoint, sampleBytes.subarray(0, 9));
  const tail = [...d2.push(sampleBytes.subarray(9)), ...d2.finish()];

  const all = [...head, ...tail];
  assert.equal(textOf(all), SAMPLE);
  assert.deepEqual(
    all.map((e) => [e.kind, e.byteStart, e.byteEnd, e.u16Start, e.u16Length, e.text]),
    refEvents.map((e) => [e.kind, e.byteStart, e.byteEnd, e.u16Start, e.u16Length, e.text]),
  );
  assert.deepEqual(d2.map.toJSON(), refDecoder.map.toJSON());
  assert.equal(d2.byteOffset, sampleBytes.length);
  assert.equal(d2.u16Offset, SAMPLE.length);
});

test('checkpoint mid-sequence resumes the pending character', () => {
  // emoji '😀' starts at byte 7 ('h'=1, 'é'=2, 'llo'=3, ' '=1 -> 7).
  const d1 = new IncrementalUtf8Decoder('strict');
  d1.push(sampleBytes.subarray(0, 9)); // 2 of 4 emoji bytes pending
  const cp = d1.checkpoint();
  assert.deepEqual(cp.pending, [0xf0, 0x9f]);
  const d2 = IncrementalUtf8Decoder.resume(cp, sampleBytes.subarray(0, 9));
  const events = [...d2.push(sampleBytes.subarray(9)), ...d2.finish()];
  const emoji = events.find((e) => e.text === '😀');
  assert.ok(emoji !== undefined);
  assert.deepEqual([emoji.byteStart, emoji.byteEnd], [7, 11], 'absolute positions survive resume');
});

test('checkpoint digest binds the input prefix: other streams are rejected', () => {
  const d1 = new IncrementalUtf8Decoder('replace');
  d1.push(sampleBytes.subarray(0, 10));
  const cp = d1.checkpoint();

  // Same length, different content -> digest mismatch.
  const other = enc.encode('XXXXXXXXXX');
  assert.throws(() => IncrementalUtf8Decoder.resume(cp, other), CheckpointMismatchError);
  // Truncated prefix -> length mismatch.
  assert.throws(() => IncrementalUtf8Decoder.resume(cp, sampleBytes.subarray(0, 9)), CheckpointMismatchError);
  // The checkpoint's digest really is the SHA-256 of the consumed prefix.
  assert.equal(cp.prefixDigest, createHash('sha256').update(sampleBytes.subarray(0, 10)).digest('hex'));
  // Correct prefix resumes fine.
  assert.doesNotThrow(() => IncrementalUtf8Decoder.resume(cp, sampleBytes.subarray(0, 10)));
});

test('strict errors after resume still report absolute positions', () => {
  const d1 = new IncrementalUtf8Decoder('strict');
  d1.push(enc.encode('prefix-'));
  const cp = d1.checkpoint();
  const d2 = IncrementalUtf8Decoder.resume(cp, enc.encode('prefix-'));
  assert.throws(
    () => d2.push(Uint8Array.of(0x80)),
    (e: unknown) => e instanceof Utf8DecodeError && e.byteOffset === 7,
  );
});

test('replace strategy: many-to-one coverage survives checkpoint round-trip', () => {
  const bytes = Uint8Array.of(0x61, 0xe2, 0x82, 0x62);
  const d1 = new IncrementalUtf8Decoder('replace');
  d1.push(bytes.subarray(0, 3)); // 'a' + pending E2 82
  const cp = JSON.parse(JSON.stringify(d1.checkpoint()));
  const d2 = IncrementalUtf8Decoder.resume(cp, bytes.subarray(0, 3));
  d2.push(bytes.subarray(3)); // 'b' forces the replacement of E2 82
  d2.finish();
  const r = d2.map.byteRangeToU16(0, 4);
  assert.deepEqual(r.replacements, [{ byteStart: 1, byteEnd: 3, u16Start: 1, u16End: 2 }]);
  assert.equal(d2.map.byteToU16(2), 1, 'interior of the invalid sequence maps to its replacement');
});
