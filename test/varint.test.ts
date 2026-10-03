import assert from 'node:assert/strict';
import { test } from 'node:test';
import { CodecError } from '../src/codec/errors.ts';
import { ByteReader } from '../src/codec/reader.ts';
import { ByteWriter } from '../src/codec/writer.ts';
import { MAX_VARINT_BYTES, varintLength } from '../src/codec/varint.ts';

function roundtrip(v: number): number {
  const w = new ByteWriter();
  w.varint(v);
  const buf = w.finish();
  assert.equal(buf.length, varintLength(v));
  const r = new ByteReader(buf);
  const out = r.varint();
  assert.equal(r.remaining, 0);
  return out;
}

test('varint roundtrips boundary values', () => {
  const values = [0, 1, 127, 128, 255, 300, 16383, 16384, 2 ** 21 - 1, 2 ** 31 - 1, 2 ** 32, 2 ** 53 - 1];
  for (const v of values) assert.equal(roundtrip(v), v, `roundtrip ${v}`);
});

test('varint canonical byte patterns', () => {
  const w = new ByteWriter();
  w.varint(0);
  assert.deepEqual([...w.finish()], [0]);
  const w2 = new ByteWriter();
  w2.varint(300);
  assert.deepEqual([...w2.finish()], [0xac, 0x02]);
});

test('varint rejects negative and unsafe integers on write', () => {
  assert.throws(() => new ByteWriter().varint(-1), CodecError);
  assert.throws(() => new ByteWriter().varint(2 ** 53), CodecError);
  assert.throws(() => new ByteWriter().varint(1.5), CodecError);
  assert.throws(() => new ByteWriter().varint(NaN), CodecError);
});

test('varint decode rejects overflow beyond 10 bytes', () => {
  const buf = Buffer.alloc(MAX_VARINT_BYTES + 1, 0x80);
  buf[MAX_VARINT_BYTES] = 0x01;
  assert.throws(() => new ByteReader(buf).varint(), /exceeds 10 bytes/);
});

test('varint decode rejects values above safe integer range', () => {
  // 0xFFFFFFFFFFFFFFFF (9 bytes of 0xFF + 0x7F) = 2^63-1 > MAX_SAFE_INTEGER
  const buf = Buffer.concat([Buffer.alloc(9, 0xff), Buffer.from([0x7f])]);
  assert.throws(() => new ByteReader(buf).varint(), /overflows safe integer/);
});

test('varint decode rejects truncated input', () => {
  assert.throws(() => new ByteReader(Buffer.from([0x80])).varint(), CodecError);
  assert.throws(() => new ByteReader(Buffer.alloc(0)).varint(), CodecError);
});
