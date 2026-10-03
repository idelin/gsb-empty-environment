import assert from 'node:assert/strict';
import { test } from 'node:test';
import { CodecError } from '../src/codec/errors.ts';
import { crc32 } from '../src/codec/crc32.ts';
import {
  decodeFrameView,
  encodeFrame,
  FRAME_HEADER_SIZE,
  FRAME_MAGIC,
  FRAME_TRAILER_SIZE,
  FRAME_VERSION,
  MAX_FRAME_PAYLOAD,
  tryDecodeFrame,
} from '../src/codec/frame.ts';

test('frame roundtrip with zero-copy payload view', () => {
  const payload = Buffer.from('hello paxos');
  const frame = encodeFrame(payload);
  assert.equal(frame.readUInt16LE(0), FRAME_MAGIC);
  assert.equal(frame[2], FRAME_VERSION);
  assert.equal(frame.length, FRAME_HEADER_SIZE + payload.length + FRAME_TRAILER_SIZE);

  const view = decodeFrameView(frame);
  assert.equal(view.version, FRAME_VERSION);
  assert.equal(view.payload.toString(), 'hello paxos');
  assert.equal(view.payload.buffer, frame.buffer); // zero-copy: same backing store
  assert.equal(view.nextOffset, frame.length);
});

test('frame rejects bad magic', () => {
  const frame = encodeFrame(Buffer.from('x'));
  frame[0] = 0xff;
  assert.throws(() => decodeFrameView(frame), /bad frame magic/);
});

test('frame rejects crc corruption in header and payload', () => {
  const a = encodeFrame(Buffer.from('payload-bytes'));
  a[2] = 0x99; // version byte, covered by crc
  assert.throws(() => decodeFrameView(a), /crc mismatch|supported range/i);

  const b = encodeFrame(Buffer.from('payload-bytes'));
  b[FRAME_HEADER_SIZE + 3] ^= 0xff; // payload byte
  assert.throws(() => decodeFrameView(b), /crc mismatch/);

  const c = encodeFrame(Buffer.from('payload-bytes'));
  c[c.length - 1] ^= 0xff; // crc byte itself
  assert.throws(() => decodeFrameView(c), /crc mismatch/);
});

test('frame rejects unsupported versions', () => {
  const frame = encodeFrame(Buffer.from('x'), FRAME_VERSION);
  frame[2] = FRAME_VERSION + 1;
  // fix crc so only the version check can fire
  const crc = crc32(frame.subarray(0, frame.length - 4));
  frame.writeUInt32LE(crc, frame.length - 4);
  assert.throws(() => decodeFrameView(frame), /outside supported range/);
});


test('frame rejects length prefix overflow', () => {
  const payload = Buffer.from('abc');
  const frame = encodeFrame(payload);
  frame.writeUInt32LE(MAX_FRAME_PAYLOAD + 1, 3);
  assert.throws(() => decodeFrameView(frame), /exceeds max/);
});

test('tryDecodeFrame returns null on truncation, throws on corruption', () => {
  const frame = encodeFrame(Buffer.from('streaming'));
  for (const cut of [1, FRAME_HEADER_SIZE - 1, FRAME_HEADER_SIZE, frame.length - 2]) {
    assert.equal(tryDecodeFrame(frame.subarray(0, cut)), null, `cut at ${cut}`);
  }
  const corrupt = Buffer.from(frame);
  corrupt[0] = 0x00;
  assert.throws(() => tryDecodeFrame(corrupt), CodecError);
});

test('decodeFrameView walks concatenated frames by offset', () => {
  const f1 = encodeFrame(Buffer.from('one'));
  const f2 = encodeFrame(Buffer.from('two'));
  const f3 = encodeFrame(Buffer.from('three'));
  const all = Buffer.concat([f1, f2, f3]);
  const v1 = decodeFrameView(all, 0);
  const v2 = decodeFrameView(all, v1.nextOffset);
  const v3 = decodeFrameView(all, v2.nextOffset);
  assert.deepEqual(
    [v1.payload.toString(), v2.payload.toString(), v3.payload.toString()],
    ['one', 'two', 'three'],
  );
  assert.equal(v3.nextOffset, all.length);
});

test('decodeFrameView throws TRUNCATED past the end', () => {
  const frame = encodeFrame(Buffer.from('x'));
  assert.throws(() => decodeFrameView(frame, frame.length), CodecError);
});
