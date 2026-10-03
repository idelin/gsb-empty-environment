import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  MalformedWireError,
  TruncationError,
  WireReader,
  WireWriter,
  encodeVarint,
  zigZagDecode,
  zigZagEncode,
} from '../src/wire.ts';

test('varint round-trips boundary values', () => {
  const values = [0n, 1n, 127n, 128n, 300n, 16384n, 2n ** 32n - 1n, 2n ** 63n, 2n ** 64n - 1n];
  for (const value of values) {
    const reader = new WireReader(encodeVarint(value));
    assert.equal(reader.readVarint(), value);
    assert.equal(reader.eof, true);
  }
});

test('varint uses 7-bit compression with continuation bits', () => {
  assert.deepEqual([...encodeVarint(0)], [0x00]);
  assert.deepEqual([...encodeVarint(1)], [0x01]);
  assert.deepEqual([...encodeVarint(127)], [0x7f]);
  assert.deepEqual([...encodeVarint(128)], [0x80, 0x01]);
  assert.deepEqual([...encodeVarint(300)], [0xac, 0x02]);
  assert.equal(encodeVarint(2n ** 64n - 1n).length, 10);
});

test('negative int64 encodes as 10-byte two-complement varint', () => {
  const encoded = encodeVarint(-1n);
  assert.equal(encoded.length, 10);
  const reader = new WireReader(encoded);
  assert.equal(BigInt.asIntN(64, reader.readVarint()), -1n);
});

test('truncated varint raises TruncationError with bounds info', () => {
  const reader = new WireReader(Uint8Array.from([0x80, 0x80]));
  assert.throws(() => reader.readVarint(), (error: unknown) => {
    assert.ok(error instanceof TruncationError);
    assert.equal(error.offset, 2);
    assert.equal(error.needed, 1);
    assert.equal(error.available, 0);
    return true;
  });
});

test('varint longer than 10 bytes is malformed', () => {
  const reader = new WireReader(new Uint8Array(11).fill(0x80));
  assert.throws(() => reader.readVarint(), MalformedWireError);
});

test('zigzag mapping follows the protobuf spec', () => {
  const cases: Array<[bigint, bigint]> = [
    [0n, 0n],
    [-1n, 1n],
    [1n, 2n],
    [-2n, 3n],
    [2147483647n, 4294967294n],
    [-2147483648n, 4294967295n],
    [2n ** 63n - 1n, 2n ** 64n - 2n],
    [-(2n ** 63n), 2n ** 64n - 1n],
  ];
  for (const [signed, encoded] of cases) {
    assert.equal(zigZagEncode(signed), encoded);
    assert.equal(zigZagDecode(encoded), signed);
  }
});

test('fixed32/fixed64 little-endian round-trip', () => {
  const writer = new WireWriter();
  writer.writeFixed32(0xdeadbeef);
  writer.writeFixed64(0x0102030405060708n);
  writer.writeFixed64(-2n);
  const reader = new WireReader(writer.finish());
  assert.equal(reader.readFixed32(), 0xdeadbeef);
  assert.equal(reader.readFixed64(), 0x0102030405060708n);
  assert.equal(reader.readSFixed64(), -2n);
  assert.equal(reader.eof, true);
});

test('fixed32/fixed64 truncation is guarded', () => {
  assert.throws(() => new WireReader(new Uint8Array(3)).readFixed32(), TruncationError);
  assert.throws(() => new WireReader(new Uint8Array(7)).readFixed64(), TruncationError);
  assert.throws(() => new WireReader(new Uint8Array(0)).readFloat(), TruncationError);
  assert.throws(() => new WireReader(new Uint8Array(4)).readDouble(), TruncationError);
});

test('float/double round-trip', () => {
  const writer = new WireWriter();
  writer.writeFloat(1.5);
  writer.writeDouble(-3.141592653589793);
  const reader = new WireReader(writer.finish());
  assert.equal(reader.readFloat(), 1.5);
  assert.equal(reader.readDouble(), -3.141592653589793);
});

test('length-delimited payload truncation is guarded', () => {
  const bytes = Uint8Array.from([0x05, 0x01, 0x02]); // declares 5, provides 2
  const reader = new WireReader(bytes);
  assert.throws(() => reader.readBytes(), (error: unknown) => {
    assert.ok(error instanceof TruncationError);
    assert.equal(error.needed, 5);
    assert.equal(error.available, 2);
    return true;
  });
});

test('skip handles every wire type and rejects unknown ones', () => {
  const writer = new WireWriter();
  writer.writeVarint(7); // wire type 0
  writer.writeFixed64(1n); // wire type 1
  writer.writeBytes(Uint8Array.from([1, 2, 3])); // wire type 2
  writer.writeFixed32(9); // wire type 5
  const reader = new WireReader(writer.finish());
  reader.skip(0);
  reader.skip(1);
  reader.skip(2);
  reader.skip(5);
  assert.equal(reader.eof, true);
  assert.throws(() => new WireReader(new Uint8Array(1)).skip(3), MalformedWireError);
});

test('truncated length prefix inside skip is guarded', () => {
  const reader = new WireReader(Uint8Array.from([0x0a])); // only a tag, no payload
  reader.readVarint();
  assert.throws(() => reader.skip(2), TruncationError);
});
