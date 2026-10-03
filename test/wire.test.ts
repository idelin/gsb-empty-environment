import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  DecodeError,
  WireReader,
  WireType,
  WireWriter,
  decodeZigZag32,
  decodeZigZag64,
  encodeZigZag32,
  encodeZigZag64,
} from '../src/wire.ts';

function bytes(...values: number[]): Uint8Array {
  return Uint8Array.from(values);
}

describe('varint (7-bit compression)', () => {
  it('encodes canonical vectors', () => {
    assert.deepEqual(new WireWriter().writeVarint64(0n).finish(), bytes(0x00));
    assert.deepEqual(new WireWriter().writeVarint64(1n).finish(), bytes(0x01));
    assert.deepEqual(new WireWriter().writeVarint64(127n).finish(), bytes(0x7f));
    assert.deepEqual(new WireWriter().writeVarint64(128n).finish(), bytes(0x80, 0x01));
    assert.deepEqual(new WireWriter().writeVarint64(300n).finish(), bytes(0xac, 0x02));
    assert.deepEqual(
      new WireWriter().writeVarint64(0xffff_ffff_ffff_ffffn).finish(),
      bytes(0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01),
    );
  });

  it('round-trips boundary values', () => {
    const values = [
      0n, 1n, 127n, 128n, 300n,
      (1n << 31n) - 1n, 1n << 31n,
      (1n << 32n) - 1n, 1n << 32n,
      (1n << 53n) - 1n,
      (1n << 63n) - 1n, 1n << 63n,
      (1n << 64n) - 1n,
    ];
    for (const value of values) {
      const encoded = new WireWriter().writeVarint64(value).finish();
      const reader = new WireReader(encoded);
      assert.equal(reader.readVarint64(), value, `value ${value}`);
      assert.ok(reader.done);
    }
  });

  it('rejects a truncated varint without reading out of bounds', () => {
    assert.throws(() => new WireReader(bytes(0x80)).readVarint64(), DecodeError);
    assert.throws(() => new WireReader(bytes(0xac)).readVarint64(), /truncated varint/);
    assert.throws(() => new WireReader(new Uint8Array(0)).readVarint64(), DecodeError);
  });

  it('rejects varints overflowing 64 bits', () => {
    // 10th byte carrying more than 1 payload bit.
    const ten = bytes(0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02);
    assert.throws(() => new WireReader(ten).readVarint64(), /overflows 64 bits/);
    // 11-byte varint (continuation bit still set on the 10th byte).
    const eleven = bytes(0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01);
    assert.throws(() => new WireReader(eleven).readVarint64(), /overflows 64 bits/);
  });

  it('rejects uint32 varints exceeding 32 bits', () => {
    const encoded = new WireWriter().writeVarint64(1n << 32n).finish();
    assert.throws(() => new WireReader(encoded).readVarint32(), /exceeds 32 bits/);
  });

  it('decodes negative int32 from 10-byte sign-extended varints', () => {
    const encoded = new WireWriter().writeInt32(-1).finish();
    assert.equal(encoded.length, 10);
    assert.equal(new WireReader(encoded).readInt32(), -1);
    const encodedMin = new WireWriter().writeInt32(-0x80000000).finish();
    assert.equal(new WireReader(encodedMin).readInt32(), -0x80000000);
  });
});

describe('zigzag', () => {
  it('maps signed values to unsigned per spec', () => {
    const cases: Array<[number, number]> = [
      [0, 0], [-1, 1], [1, 2], [-2, 3],
      [2147483647, 4294967294], [-2147483648, 4294967295],
    ];
    for (const [signed, unsigned] of cases) {
      assert.equal(encodeZigZag32(signed), unsigned);
      assert.equal(decodeZigZag32(unsigned), signed);
    }
  });

  it('maps 64-bit extremes', () => {
    const cases: Array<[bigint, bigint]> = [
      [0n, 0n], [-1n, 1n], [1n, 2n], [-2n, 3n],
      [(1n << 63n) - 1n, (1n << 64n) - 2n],
      [-(1n << 63n), (1n << 64n) - 1n],
    ];
    for (const [signed, unsigned] of cases) {
      assert.equal(encodeZigZag64(signed), unsigned);
      assert.equal(decodeZigZag64(unsigned), signed);
    }
  });

  it('round-trips sint32/sint64 through the wire', () => {
    for (const v of [0, -1, 1, -2147483648, 2147483647]) {
      const buf = new WireWriter().writeSint32(v).finish();
      assert.equal(new WireReader(buf).readSint32(), v);
    }
    for (const v of [0n, -1n, 1n, -(1n << 63n), (1n << 63n) - 1n]) {
      const buf = new WireWriter().writeSint64(v).finish();
      assert.equal(new WireReader(buf).readSint64(), v);
    }
  });
});

describe('fixed32 / fixed64 (little-endian)', () => {
  it('writes fixed32 little-endian', () => {
    assert.deepEqual(
      new WireWriter().writeFixed32(0x78563412).finish(),
      bytes(0x12, 0x34, 0x56, 0x78),
    );
    assert.equal(new WireReader(bytes(0x12, 0x34, 0x56, 0x78)).readFixed32(), 0x78563412);
  });

  it('round-trips fixed64 including the sign bit', () => {
    const values = [0n, 1n, 0x0102030405060708n, (1n << 64n) - 1n];
    for (const v of values) {
      const buf = new WireWriter().writeFixed64(v).finish();
      assert.equal(buf.length, 8);
      assert.equal(new WireReader(buf).readFixed64(), v);
    }
    const neg = new WireWriter().writeSfixed64(-2n).finish();
    assert.equal(new WireReader(neg).readSfixed64(), -2n);
  });

  it('round-trips float and double', () => {
    const f = new WireWriter().writeFloat(1.5).finish();
    assert.equal(new WireReader(f).readFloat(), 1.5);
    const d = new WireWriter().writeDouble(-Math.PI).finish();
    assert.equal(new WireReader(d).readDouble(), -Math.PI);
  });

  it('rejects truncated fixed-width fields', () => {
    assert.throws(() => new WireReader(bytes(1, 2, 3)).readFixed32(), /truncated fixed32/);
    assert.throws(() => new WireReader(bytes(1, 2, 3, 4, 5, 6, 7)).readFixed64(), /truncated fixed64/);
    assert.throws(() => new WireReader(bytes(1, 2)).readDouble(), /truncated double/);
  });
});

describe('tags and skipping', () => {
  it('parses field number and wire type', () => {
    const buf = new WireWriter().writeTag(15, WireType.LengthDelimited).finish();
    const tag = new WireReader(buf).readTag();
    assert.deepEqual(tag, { field: 15, wireType: WireType.LengthDelimited });
  });

  it('returns null at end of input', () => {
    assert.equal(new WireReader(new Uint8Array(0)).readTag(), null);
  });

  it('rejects field number 0 and reserved wire types', () => {
    assert.throws(() => new WireReader(bytes(0x00)).readTag(), /field number 0/);
    assert.throws(() => new WireReader(bytes(0x0b)).readTag(), /unsupported wire type 3/);
    assert.throws(() => new WireReader(bytes(0x0e)).readTag(), /unsupported wire type 6/);
  });

  it('skips every wire type safely', () => {
    const writer = new WireWriter();
    writer.writeTag(1, WireType.Varint).writeVarint64(150n);
    writer.writeTag(2, WireType.Fixed64).writeFixed64(42n);
    writer.writeTag(3, WireType.LengthDelimited).writeLengthDelimited(bytes(9, 9, 9));
    writer.writeTag(4, WireType.Fixed32).writeFixed32(7);
    writer.writeTag(5, WireType.Varint).writeVarint64(1n);
    const reader = new WireReader(writer.finish());
    for (let i = 0; i < 4; i++) {
      const tag = reader.readTag();
      assert.ok(tag);
      reader.skipField(tag.wireType);
    }
    const last = reader.readTag();
    assert.equal(last?.field, 5);
    assert.equal(reader.readVarint64(), 1n);
    assert.ok(reader.done);
  });

  it('rejects skipping past the end of the buffer', () => {
    // length-delimited field claiming 5 bytes with only 2 present.
    const buf = new WireWriter().writeTag(1, WireType.LengthDelimited).writeVarint64(5n).writeBytes(bytes(1, 2)).finish();
    const reader = new WireReader(buf);
    const tag = reader.readTag();
    assert.ok(tag);
    assert.throws(() => reader.skipField(tag.wireType), /truncated length-delimited payload/);
  });
});

describe('length-delimited bounds safety', () => {
  it('rejects a payload shorter than its declared length', () => {
    const buf = bytes(0x05, 0x01, 0x02); // declares 5, provides 2
    assert.throws(() => new WireReader(buf).readLengthDelimited(), /truncated/);
  });

  it('rejects a truncated length prefix itself', () => {
    assert.throws(() => new WireReader(bytes(0x80)).readLengthDelimited(), /truncated varint/);
  });

  it('sub-reader is scoped to the nested payload', () => {
    const inner = new WireWriter().writeVarint64(7n).finish();
    const outer = new WireWriter().writeLengthDelimited(inner).writeVarint64(9n).finish();
    const reader = new WireReader(outer);
    const sub = reader.readSubReader();
    assert.equal(sub.readVarint64(), 7n);
    assert.ok(sub.done);
    assert.equal(reader.readVarint64(), 9n);
  });

  it('never reads out of bounds on any truncation of a nested message', () => {
    const inner = new WireWriter()
      .writeTag(1, WireType.Varint).writeVarint64(300n)
      .writeTag(2, WireType.Fixed64).writeFixed64(0xdeadbeefn)
      .finish();
    const full = new WireWriter()
      .writeTag(1, WireType.LengthDelimited).writeLengthDelimited(inner)
      .finish();
    for (let cut = 0; cut < full.length; cut++) {
      assert.throws(
        () => {
          const reader = new WireReader(full.subarray(0, cut));
          const tag = reader.readTag();
          if (tag === null) throw new DecodeError('empty', 0);
          const sub = reader.readSubReader();
          while (!sub.done) {
            const t = sub.readTag();
            if (t === null) break;
            sub.skipField(t.wireType);
          }
        },
        DecodeError,
        `cut at ${cut}`,
      );
    }
  });
});
