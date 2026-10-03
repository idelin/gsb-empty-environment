import { test } from "node:test";
import assert from "node:assert/strict";
import { BitWriter, ReverseBitReader } from "../src/bitstream.ts";
import { mulberry32 } from "./util.ts";

test("bit writer/reader round-trip is exact LIFO", () => {
  const rand = mulberry32(7);
  const writer = new BitWriter();
  const entries: Array<[number, number]> = [];
  for (let i = 0; i < 5000; i++) {
    const nbBits = Math.floor(rand() * 17); // 0..16 bits
    const value = Math.floor(rand() * 2 ** nbBits);
    entries.push([value, nbBits]);
    writer.writeBits(value, nbBits);
  }
  const bytes = writer.finish();
  const reader = new ReverseBitReader(bytes);
  for (let i = entries.length - 1; i >= 0; i--) {
    const [value, nbBits] = entries[i];
    assert.equal(reader.readBits(nbBits), value, `entry ${i}`);
  }
});

test("zero-bit writes are no-ops", () => {
  const writer = new BitWriter();
  writer.writeBits(0, 0);
  writer.writeBits(0b101, 3);
  writer.writeBits(0, 0);
  const reader = new ReverseBitReader(writer.finish());
  assert.equal(reader.readBits(3), 0b101);
});

test("reader rejects empty and mark-less streams", () => {
  assert.throws(() => new ReverseBitReader(new Uint8Array(0)));
  assert.throws(() => new ReverseBitReader(new Uint8Array([0x00, 0x00])));
});

test("reader detects underflow", () => {
  const writer = new BitWriter();
  writer.writeBits(1, 4);
  const reader = new ReverseBitReader(writer.finish());
  reader.readBits(4);
  assert.throws(() => reader.readBits(1), /underflow/);
});
