import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WireReader, WireWriter, TruncatedError, OverflowError } from '../src/index.ts';

function roundtripVarint(v: bigint): Uint8Array {
  return new WireWriter().varint(v).finish();
}

test('varint: canonical encodings match the protobuf spec', () => {
  assert.deepEqual([...roundtripVarint(0n)], [0x00]);
  assert.deepEqual([...roundtripVarint(1n)], [0x01]);
  assert.deepEqual([...roundtripVarint(127n)], [0x7f]);
  assert.deepEqual([...roundtripVarint(128n)], [0x80, 0x01]);
  assert.deepEqual([...roundtripVarint(300n)], [0xac, 0x02]); // 规范经典样例
  assert.deepEqual([...roundtripVarint(16383n)], [0xff, 0x7f]);
  assert.deepEqual([...roundtripVarint(16384n)], [0x80, 0x80, 0x01]);
});

test('varint: 7-bit compaction boundaries round-trip', () => {
  const boundaries = [0n, 1n, 126n, 127n, 128n, 129n];
  for (let i = 1n; i < 64n; i++) {
    boundaries.push((1n << i) - 1n, 1n << i, (1n << i) + 1n);
  }
  boundaries.push(0xffff_ffff_ffff_ffffn); // 2^64 - 1
  for (const v of boundaries) {
    const enc = roundtripVarint(v);
    const bitLen = v === 0n ? 1 : v.toString(2).length;
    assert.equal(enc.length, Math.ceil(bitLen / 7), `length for ${v}`);
    assert.equal(new WireReader(enc).uint64(), v);
  }
});

test('varint: 64-bit max uses exactly 10 bytes', () => {
  const enc = roundtripVarint(0xffff_ffff_ffff_ffffn);
  assert.equal(enc.length, 10);
  assert.equal(enc[9], 0x01);
  assert.equal(new WireReader(enc).uint64(), 0xffff_ffff_ffff_ffffn);
});

test('varint: 11-byte sequence is rejected as overflow', () => {
  const buf = new Uint8Array([0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01]);
  assert.throws(() => new WireReader(buf).varint64(), OverflowError);
});

test('varint: 10th byte > 1 overflows 64 bits', () => {
  const buf = new Uint8Array([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f]);
  assert.throws(() => new WireReader(buf).varint64(), OverflowError);
});

test('varint: truncation mid-sequence raises TruncatedError', () => {
  for (let cut = 0; cut < 10; cut++) {
    const buf = roundtripVarint(0xffff_ffff_ffff_ffffn).subarray(0, cut);
    if (cut === 0) {
      assert.throws(() => new WireReader(buf).varint64(), TruncatedError);
    } else {
      assert.throws(() => new WireReader(buf).varint64(), TruncatedError);
    }
  }
});

test('zigzag32: signed mapping per spec', () => {
  const cases: [number, number][] = [[0, 0], [-1, 1], [1, 2], [-2, 3], [2147483647, 4294967294], [-2147483648, 4294967295]];
  for (const [signed, unsigned] of cases) {
    const w = new WireWriter().zigzag32(signed);
    assert.equal(new WireReader(w.finish()).uint32(), unsigned >>> 0);
    assert.equal(new WireReader(w.finish()).zigzag32(), signed);
  }
});

test('zigzag64: extremes round-trip via bigint', () => {
  const cases = [0n, -1n, 1n, -2n, 2n, 0x7fff_ffff_ffff_ffffn, -0x8000_0000_0000_0000n];
  for (const v of cases) {
    const enc = new WireWriter().zigzag64(v).finish();
    assert.equal(new WireReader(enc).zigzag64(), v);
  }
});

test('fixed32/fixed64/float/double little-endian round-trip', () => {
  const w = new WireWriter()
    .fixed32(0xdeadbeef).sfixed32(-2).float(1.5)
    .fixed64(0x0102_0304_0506_0708n).sfixed64(-2n).double(Math.PI);
  const r = new WireReader(w.finish());
  assert.equal(r.fixed32(), 0xdeadbeef);
  assert.equal(r.sfixed32(), -2);
  assert.equal(r.float(), 1.5);
  assert.equal(r.fixed64(), 0x0102_0304_0506_0708n);
  assert.equal(r.sfixed64(), -2n);
  assert.equal(r.double(), Math.PI);
  assert.ok(r.done);
});

test('fixed reads are bounds-checked', () => {
  assert.throws(() => new WireReader(new Uint8Array(3)).fixed32(), TruncatedError);
  assert.throws(() => new WireReader(new Uint8Array(7)).fixed64(), TruncatedError);
  assert.throws(() => new WireReader(new Uint8Array(0)).double(), TruncatedError);
});

test('int32 negative sign-extends to 10-byte varint (protobuf semantics)', () => {
  const enc = new WireWriter().int32(-1).finish();
  assert.equal(enc.length, 10);
  assert.equal(new WireReader(enc).int32(), -1);
});

test('writer.finish returns exact-length buffer, no capacity leak', () => {
  const w = new WireWriter();
  for (let i = 0; i < 1000; i++) w.varint(i);
  const out = w.finish();
  assert.equal(out.length, w.length);
  assert.equal(out.buffer.byteLength, out.length); // 不暴露内部扩容容量
});
