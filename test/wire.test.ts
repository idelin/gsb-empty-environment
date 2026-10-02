import { test } from "node:test";
import assert from "node:assert/strict";
import { WireReader, WireType } from "../src/wire/reader.ts";
import { WireWriter } from "../src/wire/writer.ts";
import { WireError } from "../src/wire/errors.ts";

function readerOf(...bytes: number[]): WireReader {
  return new WireReader(Uint8Array.from(bytes));
}

test("varint: 7-bit compaction boundaries", () => {
  const cases: Array<[bigint, number[]]> = [
    [0n, [0x00]],
    [1n, [0x01]],
    [127n, [0x7f]], // max single-byte (7 bits)
    [128n, [0x80, 0x01]], // first two-byte value
    [300n, [0xac, 0x02]], // canonical protobuf example
    [16383n, [0xff, 0x7f]],
    [16384n, [0x80, 0x80, 0x01]],
    [0xffff_ffffn, [0xff, 0xff, 0xff, 0xff, 0x0f]],
    [0xffff_ffff_ffff_ffffn, [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01]],
  ];
  for (const [value, bytes] of cases) {
    const r = readerOf(...bytes);
    assert.equal(r.readVarint(), value, `decode ${value}`);
    assert.equal(r.remaining, 0);
    const w = new WireWriter();
    w.writeVarint(value);
    assert.deepEqual([...w.finish()], bytes, `encode ${value}`);
  }
});

test("varint: roundtrip sweep across bit widths", () => {
  for (let bits = 0; bits < 64; bits++) {
    const value = (1n << BigInt(bits)) | 1n;
    const w = new WireWriter();
    w.writeVarint(value);
    assert.equal(new WireReader(w.finish()).readVarint(), value);
  }
});

test("varint: rejects >64-bit overflow", () => {
  // 10th byte carries more than the single allowed bit.
  const r = readerOf(0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02);
  assert.throws(() => r.readVarint(), (e: unknown) => e instanceof WireError && e.kind === "overflow");
});

test("varint: rejects unterminated sequence longer than 10 bytes", () => {
  const r = readerOf(0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80);
  assert.throws(() => r.readVarint(), (e: unknown) => e instanceof WireError && e.kind === "overflow");
});

test("varint: truncated continuation byte raises truncation error", () => {
  const r = readerOf(0x80); // continuation bit set, but stream ends
  assert.throws(
    () => r.readVarint(),
    (e: unknown) => e instanceof WireError && e.kind === "truncation" && e.offset === 1,
  );
});

test("zigzag: signed mapping 0,-1,1,-2 ... and extremes", () => {
  const cases: Array<[number, number]> = [
    [0, 0],
    [-1, 1],
    [1, 2],
    [-2, 3],
    [2, 4],
    [2147483647, 4294967294],
    [-2147483648, 4294967295],
  ];
  for (const [decoded, encoded] of cases) {
    const w = new WireWriter();
    w.writeZigZag32(decoded);
    assert.deepEqual([...w.finish()], [...new WireWriter().writeVarint(encoded).finish()]);
    assert.equal(new WireReader(w.finish()).readZigZag32(), decoded);
  }
});

test("zigzag64: 64-bit extremes", () => {
  const cases: Array<[bigint, bigint]> = [
    [0n, 0n],
    [-1n, 1n],
    [1n, 2n],
    [9223372036854775807n, 18446744073709551614n],
    [-9223372036854775808n, 18446744073709551615n],
  ];
  for (const [decoded, encoded] of cases) {
    const w = new WireWriter();
    w.writeZigZag64(decoded);
    assert.deepEqual([...w.finish()], [...new WireWriter().writeVarint(encoded).finish()]);
    assert.equal(new WireReader(w.finish()).readZigZag64(), decoded);
  }
});

test("fixed32/fixed64: little-endian roundtrip and truncation", () => {
  const w = new WireWriter();
  w.writeFixed32(0xdeadbeef).writeFixed64(0x0102030405060708n);
  const r = new WireReader(w.finish());
  assert.equal(r.readFixed32(), 0xdeadbeef);
  assert.equal(r.readFixed64(), 0x0102030405060708n);

  assert.throws(
    () => readerOf(0x01, 0x02, 0x03).readFixed32(),
    (e: unknown) => e instanceof WireError && e.kind === "truncation",
  );
  assert.throws(
    () => readerOf(0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07).readFixed64(),
    (e: unknown) => e instanceof WireError && e.kind === "truncation",
  );
});

test("float/double roundtrip", () => {
  const w = new WireWriter();
  w.writeFloat(3.5).writeDouble(-12345.6789);
  const r = new WireReader(w.finish());
  assert.equal(r.readFloat(), 3.5);
  assert.ok(Math.abs(r.readDouble() - -12345.6789) < 1e-12);
});

test("length-delimited: bytes and string", () => {
  const w = new WireWriter();
  w.writeString("héllo 世界").writeBytes(Uint8Array.from([1, 2, 3]));
  const r = new WireReader(w.finish());
  assert.equal(r.readString(), "héllo 世界");
  assert.deepEqual([...r.readBytes()], [1, 2, 3]);
});

test("length-delimited: declared length beyond buffer is rejected", () => {
  const r = readerOf(0x05, 0x01, 0x02); // says 5 bytes, only 2 present
  assert.throws(
    () => r.readBytes(),
    (e: unknown) => e instanceof WireError && e.kind === "truncation",
  );
});

test("tags: field/wire-type split, field 0 and groups rejected", () => {
  const w = new WireWriter();
  w.writeTag(15, WireType.Fixed32);
  const tag = new WireReader(w.finish()).readTag();
  assert.deepEqual(tag, { field: 15, wireType: WireType.Fixed32 });

  assert.throws(
    () => readerOf(0x00).readTag(),
    (e: unknown) => e instanceof WireError && e.kind === "invalid-tag",
  );
  assert.throws(
    () => readerOf(0x0b).readTag(), // field 1, StartGroup
    (e: unknown) => e instanceof WireError && e.kind === "invalid-wire-type",
  );
});

test("skip: every wire type is skipped with bounds checks", () => {
  const w = new WireWriter();
  w.writeVarint(150).writeFixed64(1n).writeString("skip me").writeFixed32(7);
  const r = new WireReader(w.finish());
  r.skip(WireType.Varint);
  r.skip(WireType.Fixed64);
  r.skip(WireType.LengthDelimited);
  r.skip(WireType.Fixed32);
  assert.equal(r.remaining, 0);

  const truncated = readerOf(0x0a); // length 10, no payload
  assert.throws(
    () => truncated.skip(WireType.LengthDelimited),
    (e: unknown) => e instanceof WireError && e.kind === "truncation",
  );
});
