import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BitReader, BitWriter } from '../src/bitstream.ts';

test('bit writer/reader round-trips across byte boundaries', () => {
  const writer = new BitWriter();
  const widths = [1, 3, 8, 5, 13, 32, 2, 7, 24];
  const values = widths.map((w, i) => (0x1234abcd + i * 7919) >>> (32 - w));
  widths.forEach((w, i) => writer.writeBits(values[i], w));
  const data = writer.toUint8Array();

  const reader = new BitReader(data);
  widths.forEach((w, i) => {
    assert.equal(reader.readBits(w), values[i] >>> 0, `field ${i} (${w} bits)`);
  });
});

test('writer pads the final partial byte with zeros (MSB first)', () => {
  const writer = new BitWriter();
  writer.writeBits(0b101, 3);
  assert.deepEqual([...writer.toUint8Array()], [0b10100000]);
});

test('reader throws on overrun', () => {
  const reader = new BitReader(new Uint8Array([0xff]));
  reader.readBits(8);
  assert.throws(() => reader.readBit(), /unexpected end of data/);
});

test('reader honours byte offset', () => {
  const reader = new BitReader(new Uint8Array([0xaa, 0b11000000, 0x00]), 1);
  assert.equal(reader.readBits(2), 0b11);
});
