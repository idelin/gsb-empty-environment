import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Reader, WireType, Writer } from '../src/wire.ts';
import { ProtoError, isProtoError } from '../src/errors.ts';

test('Writer/Reader 标量往返', () => {
  const w = new Writer();
  w.writeTag(1, WireType.Varint).writeVarint(150);
  w.writeTag(2, WireType.Fixed64).writeFixed64(0x0102030405060708n);
  w.writeTag(3, WireType.LengthDelimited).writeString('你好, protobuf');
  w.writeTag(4, WireType.Fixed32).writeFixed32(0xdeadbeef);
  const buf = w.finish();

  const r = new Reader(buf);
  let tag = r.readTag();
  assert.deepEqual(tag, { fieldNumber: 1, wireType: WireType.Varint });
  assert.equal(r.readVarint(), 150n);
  tag = r.readTag();
  assert.deepEqual(tag, { fieldNumber: 2, wireType: WireType.Fixed64 });
  assert.equal(r.readFixed64(), 0x0102030405060708n);
  tag = r.readTag();
  assert.equal(tag.fieldNumber, 3);
  assert.equal(r.readString(), '你好, protobuf');
  tag = r.readTag();
  assert.equal(tag.fieldNumber, 4);
  assert.equal(r.readFixed32(), 0xdeadbeef);
  r.assertEnd();
});

test('fixed32/64 小端序与 float/double', () => {
  const w = new Writer();
  w.writeFixed32(1);
  w.writeFixed64(1n);
  w.writeFloat(1.5);
  w.writeDouble(Math.PI);
  const buf = w.finish();
  assert.deepEqual(buf.subarray(0, 4), Uint8Array.from([1, 0, 0, 0]));
  assert.deepEqual(buf.subarray(4, 12), Uint8Array.from([1, 0, 0, 0, 0, 0, 0, 0]));
  const r = new Reader(buf);
  assert.equal(r.readFixed32(), 1);
  assert.equal(r.readFixed64(), 1n);
  assert.equal(r.readFloat(), 1.5);
  assert.equal(r.readDouble(), Math.PI);
});

test('非法 wire type 与字段号被拒绝', () => {
  const w = new Writer();
  w.writeVarint((1 << 3) | 3); // wire type 3（已废弃的 SGROUP 直接构造）
  assert.throws(() => new Reader(w.finish()).readTag(),
    (e: unknown) => isProtoError(e, 'INVALID_WIRE_TYPE'));
  const w2 = new Writer();
  w2.writeVarint(0); // 字段号 0 非法
  assert.throws(() => new Reader(w2.finish()).readTag(),
    (e: unknown) => isProtoError(e, 'INVALID_TAG'));
  assert.throws(() => new Writer().writeTag(0, WireType.Varint),
    (e: unknown) => isProtoError(e, 'INVALID_TAG'));
});

test('skip 安全跳过各类未知字段', () => {
  const w = new Writer();
  w.writeTag(1, WireType.Varint).writeVarint(42);
  w.writeTag(2, WireType.Fixed64).writeFixed64(9n);
  w.writeTag(3, WireType.LengthDelimited).writeString('skip me');
  w.writeTag(4, WireType.Fixed32).writeFixed32(7);
  const r = new Reader(w.finish());
  while (!r.done) {
    const { wireType } = r.readTag();
    r.skip(wireType);
  }
  assert.equal(r.remaining, 0);
});

test('截断防护：任意截断点要么合法解码要么 TRUNCATED，绝不抛 RangeError', () => {
  const w = new Writer();
  w.writeTag(1, WireType.Varint).writeVarint((1n << 60n) + 12345n);
  w.writeTag(2, WireType.LengthDelimited).writeString('truncation fuzz payload');
  w.writeTag(3, WireType.Fixed64).writeFixed64(0xaabbccddeeff0011n);
  w.writeTag(4, WireType.Fixed32).writeFixed32(0x12345678);
  const full = w.finish();

  const decodeAll = (buf: Uint8Array) => {
    const r = new Reader(buf);
    while (!r.done) {
      const { wireType } = r.readTag();
      r.skip(wireType);
    }
  };
  decodeAll(full); // 完整流必须可解
  for (let len = 0; len < full.length; len++) {
    const cut = full.subarray(0, len);
    try {
      decodeAll(cut); // 恰好切在字段边界时是合法前缀
    } catch (err) {
      assert.ok(err instanceof ProtoError, `len=${len} 抛出了非 ProtoError: ${String(err)}`);
      assert.equal(err.code, 'TRUNCATED', `len=${len} 应为 TRUNCATED，实际 ${err.code}`);
    }
  }
});

test('length-delimited 声明长度越界 -> TRUNCATED', () => {
  const w = new Writer();
  w.writeTag(1, WireType.LengthDelimited);
  w.writeVarint(1000); // 声明 1000 字节
  w.writeRawBytes(Uint8Array.from([1, 2, 3])); // 实际只有 3 字节
  const r = new Reader(w.finish());
  r.readTag();
  assert.throws(() => r.readBytes(), (e: unknown) => isProtoError(e, 'TRUNCATED'));
});

test('Reader 初始窗口越界被拒绝', () => {
  const buf = new Uint8Array(4);
  assert.throws(() => new Reader(buf, 3, 5), (e: unknown) => isProtoError(e, 'TRUNCATED'));
});
