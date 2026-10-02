import { test } from "node:test";
import assert from "node:assert/strict";
import {
  BoundedReader,
  BoundedWriter,
  CorruptionError,
  crc32,
} from "../src/binary.ts";

test("crc32 matches the IEEE reference vector", () => {
  assert.equal(crc32(Buffer.from("123456789")), 0xcbf43926);
  assert.equal(crc32(new Uint8Array(0)), 0);
});

test("u8/u16/u32/u64 round-trip including boundaries", () => {
  const w = new BoundedWriter(1); // tiny initial capacity forces growth
  w.writeU8(0).writeU8(255);
  w.writeU16(0).writeU16(65535);
  w.writeU32(0).writeU32(0xffffffff);
  w.writeU64(0n).writeU64((1n << 64n) - 1n).writeU64(Number.MAX_SAFE_INTEGER);
  const r = new BoundedReader(w.toBuffer());
  assert.equal(r.readU8(), 0);
  assert.equal(r.readU8(), 255);
  assert.equal(r.readU16(), 0);
  assert.equal(r.readU16(), 65535);
  assert.equal(r.readU32(), 0);
  assert.equal(r.readU32(), 0xffffffff);
  assert.equal(r.readU64(), 0n);
  assert.equal(r.readU64(), (1n << 64n) - 1n);
  assert.equal(r.readU64Number(), Number.MAX_SAFE_INTEGER);
  assert.equal(r.remaining, 0);
});

test("varint round-trip across all bucket boundaries", () => {
  const values = [
    0n, 1n, 127n, 128n, 300n, 16383n, 16384n,
    (1n << 21n) - 1n, 1n << 21n,
    (1n << 32n) - 1n, 1n << 32n,
    BigInt(Number.MAX_SAFE_INTEGER), (1n << 64n) - 1n,
  ];
  const w = new BoundedWriter();
  for (const v of values) w.writeVarInt(v);
  const r = new BoundedReader(w.toBuffer());
  for (const v of values) assert.equal(r.readVarInt(), v);
  assert.equal(r.remaining, 0);
});

test("varint rejects encodings longer than 64 bits", () => {
  // 10 continuation bytes then a terminator: 11 bytes is too long for u64.
  const tooLong = new Uint8Array([...Array(10).fill(0x80), 0x01]);
  assert.throws(() => new BoundedReader(tooLong).readVarInt(), CorruptionError);
  // 10th byte with payload > 1 overflows u64.
  const overflow = new Uint8Array([...Array(9).fill(0xff), 0x7f]);
  assert.throws(() => new BoundedReader(overflow).readVarInt(), CorruptionError);
});

test("truncated varint raises CorruptionError, not RangeError", () => {
  assert.throws(() => new BoundedReader(new Uint8Array([0x80])).readVarInt(), CorruptionError);
});

test("every out-of-bounds read raises CorruptionError with the offset", () => {
  const buf = new Uint8Array(8);
  const cases: [number, (r: BoundedReader) => void][] = [
    [7, (r) => r.readU16()],
    [5, (r) => r.readU32()],
    [1, (r) => r.readU64()],
    [8, (r) => r.readU8()],
    [6, (r) => r.readBytes(3)],
  ];
  for (const [offset, op] of cases) {
    try {
      op(new BoundedReader(buf, offset));
      assert.fail(`expected CorruptionError at offset ${offset}`);
    } catch (err) {
      assert.ok(err instanceof CorruptionError, `offset ${offset}: got ${err}`);
      assert.equal((err as CorruptionError).offset, offset);
    }
  }
});

test("readU64Number rejects values above MAX_SAFE_INTEGER", () => {
  const w = new BoundedWriter();
  w.writeU64(BigInt(Number.MAX_SAFE_INTEGER) + 1n);
  assert.throws(() => new BoundedReader(w.toBuffer()).readU64Number(), CorruptionError);
});

test("reader window bounds are validated at construction", () => {
  const buf = new Uint8Array(4);
  assert.throws(() => new BoundedReader(buf, -1), CorruptionError);
  assert.throws(() => new BoundedReader(buf, 0, 5), CorruptionError);
  assert.throws(() => new BoundedReader(buf, 3, 2), CorruptionError);
});

test("readBytes returns a zero-copy view aliasing the source buffer", () => {
  const src = Buffer.from([1, 2, 3, 4, 5, 6, 7, 8]);
  const r = new BoundedReader(src, 2);
  const view = r.readBytes(3);
  assert.deepEqual([...view], [3, 4, 5]);
  assert.equal(view.buffer, src.buffer);
  src[3] = 99; // mutation through the source is visible in the view
  assert.equal(view[1], 99);
});

test("writer grows without losing data (large payload)", () => {
  const w = new BoundedWriter(4);
  const payload = Buffer.alloc(100 * 1024);
  for (let i = 0; i < payload.length; i++) payload[i] = i & 0xff;
  w.writeLenBytes(payload);
  const r = new BoundedReader(w.toBuffer());
  const back = r.readLenBytes();
  assert.equal(back.length, payload.length);
  assert.ok(Buffer.from(back).equals(payload));
  assert.equal(r.remaining, 0);
});
