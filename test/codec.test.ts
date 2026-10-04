import test from "node:test";
import * as assert from "node:assert/strict";
import {
  Reader, Writer, CodecError,
  crc32, readVarint, writeVarint, varintLength,
  MAX_VARINT_VALUE,
} from "../src/codec";

test("varint roundtrip across boundary values", () => {
  const values = [
    0, 1, 127, 128, 255, 300, 16383, 16384, 2097151, 2097152,
    2 ** 31 - 1, 2 ** 31, 2 ** 32, 2 ** 42, MAX_VARINT_VALUE,
  ];
  const buf = Buffer.alloc(16);
  for (const v of values) {
    const n = writeVarint(buf, 0, v);
    assert.equal(n, varintLength(v));
    const back = readVarint(buf, 0);
    assert.equal(back.value, v);
    assert.equal(back.bytes, n);
  }
});

test("varint canonical lengths", () => {
  assert.equal(varintLength(0), 1);
  assert.equal(varintLength(127), 1);
  assert.equal(varintLength(128), 2);
  assert.equal(varintLength(2 ** 21 - 1), 3);
  assert.equal(varintLength(2 ** 21), 4);
  assert.equal(varintLength(MAX_VARINT_VALUE), 8);
});

test("varint rejects invalid values and encodings", () => {
  const buf = Buffer.alloc(16);
  assert.throws(() => writeVarint(buf, 0, -1), CodecError);
  assert.throws(() => writeVarint(buf, 0, 1.5), CodecError);
  assert.throws(() => writeVarint(buf, 0, MAX_VARINT_VALUE + 1), CodecError);
  assert.throws(() => writeVarint(buf, 0, NaN), CodecError);
  // 9-byte varint: continuation bit never cleared within 8 bytes.
  const nine = Buffer.from([0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01]);
  assert.throws(() => readVarint(nine, 0), /varint too long/);
  // truncated varint
  assert.throws(() => readVarint(Buffer.from([0x80]), 0), /truncated varint/);
  // value overflowing MAX_SAFE_INTEGER (8th byte payload too large)
  const overflow = Buffer.from([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f]);
  assert.throws(() => readVarint(overflow, 0), /MAX_SAFE_INTEGER/);
});

test("crc32 matches the standard check vector", () => {
  assert.equal(crc32(Buffer.from("123456789", "utf8")), 0xcbf43926);
  assert.equal(crc32(Buffer.alloc(0)), 0);
});

test("Reader enforces bounds on every primitive", () => {
  const r = new Reader(Buffer.from([1, 2, 3]));
  assert.equal(r.readU8(), 1);
  assert.equal(r.remaining, 2);
  assert.throws(() => r.readU32(), CodecError);       // needs 4, has 2
  assert.throws(() => r.readBytes(3), /out of bounds/);
  r.skip(2);
  assert.ok(r.exhausted);
  assert.throws(() => r.readU8(), CodecError);
  assert.throws(() => r.skip(1), CodecError);
});

test("Reader honors an explicit limit smaller than the buffer", () => {
  const buf = Buffer.from([10, 20, 30, 40, 50]);
  const r = new Reader(buf, 3);
  assert.equal(r.remaining, 3);
  r.skip(3);
  assert.throws(() => r.readU8(), /out of bounds/);
  assert.throws(() => new Reader(buf, 99), CodecError);
});

test("readBytes is zero-copy: views share memory with the source", () => {
  // > 8KB so the source is not pool-allocated; identity is unambiguous.
  const src = Buffer.allocUnsafe(16 * 1024);
  src.writeUInt8(0xaa, 100);
  const r = new Reader(src);
  r.skip(100);
  const view = r.readBytes(4);
  assert.equal(view.buffer, src.buffer, "view must reference the same ArrayBuffer");
  assert.equal(view.byteOffset, src.byteOffset + 100);
  // mutation through the view is visible in the source and vice versa
  view.writeUInt8(0xbb, 0);
  assert.equal(src.readUInt8(100), 0xbb);
  src.writeUInt8(0xcc, 101);
  assert.equal(view.readUInt8(1), 0xcc);
});

test("Writer grows geometrically and roundtrips through Reader", () => {
  const w = new Writer(4);
  const payload = Buffer.allocUnsafe(10 * 1024);
  for (let i = 0; i < payload.length; i++) payload[i] = i & 0xff;
  w.writeU8(7).writeVarint(300).writeU32(0xdeadbeef).writeBytes(payload);
  const out = w.toBuffer();
  const r = new Reader(out);
  assert.equal(r.readU8(), 7);
  assert.equal(r.readVarint(), 300);
  assert.equal(r.readU32(), 0xdeadbeef);
  assert.ok(r.readBytes(payload.length).equals(payload));
  assert.ok(r.exhausted);
});

test("Writer rejects non-positive capacity", () => {
  assert.throws(() => new Writer(0), CodecError);
});
