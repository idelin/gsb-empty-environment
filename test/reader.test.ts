import assert from 'node:assert/strict';
import { test } from 'node:test';
import { CodecError } from '../src/codec/errors.ts';
import { ByteReader } from '../src/codec/reader.ts';
import { ByteWriter } from '../src/codec/writer.ts';

test('reader enforces bounds on every primitive', () => {
  const r = new ByteReader(Buffer.from([1, 2]));
  assert.equal(r.u8(), 1);
  assert.throws(() => r.u16le(), /TRUNCATED|need 2/);
  assert.throws(() => r.u32le(), CodecError);
  assert.throws(() => r.bytesView(5), CodecError);
  assert.throws(() => r.skip(-1), CodecError);
  assert.equal(r.u8(), 2);
  assert.equal(r.remaining, 0);
});

test('reader rejects negative and oversized lengths', () => {
  const r = new ByteReader(Buffer.alloc(8));
  assert.throws(() => r.bytesView(-1), CodecError);
  assert.throws(() => r.bytesView(Number.MAX_SAFE_INTEGER), CodecError);
  assert.throws(() => r.subReader(9), CodecError);
});

test('reader constructor rejects invalid windows', () => {
  const buf = Buffer.alloc(4);
  assert.throws(() => new ByteReader(buf, -1), CodecError);
  assert.throws(() => new ByteReader(buf, 0, 5), CodecError);
  assert.throws(() => new ByteReader(buf, 3, 2), CodecError);
});

test('bytesView is zero-copy, bytesCopy detaches', () => {
  const backing = Buffer.from([1, 2, 3, 4, 5, 6]);
  const r = new ByteReader(backing);
  r.u8();
  const view = r.bytesView(3);
  assert.equal(view.buffer, backing.buffer); // same backing store: no copy
  backing[1] = 0xaa;
  assert.equal(view[0], 0xaa); // mutation visible through the view

  const r2 = new ByteReader(backing);
  r2.u8();
  const copy = r2.bytesCopy(3);
  backing[2] = 0xbb;
  assert.notEqual(copy[1], 0xbb);
});

test('writer grows geometrically and finishes detached', () => {
  const w = new ByteWriter(2);
  for (let i = 0; i < 1000; i++) w.u8(i & 0xff);
  const out = w.finish();
  assert.equal(out.length, 1000);
  assert.equal(out[999], 999 & 0xff);
});
