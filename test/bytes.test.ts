import test from "node:test";
import assert from "node:assert/strict";
import { ByteReader, ByteWriter } from "../src/bytes.ts";

test("varint round-trips boundary values", () => {
  const values = [0, 1, 2, 127, 128, 129, 300, 16383, 16384, 65535, 2 ** 31 - 1, 2 ** 32 - 1];
  const writer = new ByteWriter();
  for (const v of values) writer.writeVarint(v);
  const reader = new ByteReader(writer.toUint8Array());
  for (const v of values) assert.equal(reader.readVarint(), v);
});

test("varint uses one byte for small values and grows as needed", () => {
  const writer = new ByteWriter();
  writer.writeVarint(127);
  assert.equal(writer.length, 1);
  const writer2 = new ByteWriter();
  writer2.writeVarint(128);
  assert.equal(writer2.length, 2);
});

test("u32 round-trips big-endian", () => {
  const writer = new ByteWriter();
  writer.writeU32(0xdeadbeef);
  const bytes = writer.toUint8Array();
  assert.deepEqual([...bytes], [0xde, 0xad, 0xbe, 0xef]);
  assert.equal(new ByteReader(bytes).readU32(), 0xdeadbeef);
});

test("writer grows beyond initial capacity", () => {
  const writer = new ByteWriter(4);
  for (let i = 0; i < 1000; i++) writer.writeByte(i & 0xff);
  const bytes = writer.toUint8Array();
  assert.equal(bytes.length, 1000);
  assert.equal(bytes[999], 999 & 0xff);
});
