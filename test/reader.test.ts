import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WireReader, WireWriter, WireType, TruncationError, MalformedError } from '../src/index.ts';

test('reader: fixed32/fixed64 小端读取', () => {
  const w = new WireWriter();
  w.writeFixed32(0xdeadbeef).writeFixed64(0x0123456789abcdefn);
  const r = new WireReader(w.finish());
  assert.equal(r.readFixed32(), 0xdeadbeef);
  assert.equal(r.readFixed64(), 0x0123456789abcdefn);
  assert.ok(r.eof);
});

test('reader: float/double 往返', () => {
  const w = new WireWriter();
  w.writeFloat(3.5).writeDouble(-1.25e300);
  const r = new WireReader(w.finish());
  assert.equal(r.readFloat(), 3.5);
  assert.equal(r.readDouble(), -1.25e300);
});

test('reader: 每种读取在截断时安全失败', () => {
  assert.throws(() => new WireReader(new Uint8Array(3)).readFixed32(), TruncationError);
  assert.throws(() => new WireReader(new Uint8Array(7)).readFixed64(), TruncationError);
  assert.throws(() => new WireReader(new Uint8Array(2)).readFloat(), TruncationError);
  assert.throws(() => new WireReader(new Uint8Array(5)).readDouble(), TruncationError);
  assert.throws(() => new WireReader(new Uint8Array(2)).readBytes(4), TruncationError);
  // length-delimited 声明长度超过剩余
  const w = new WireWriter();
  w.writeVarint(100).writeBytes(new Uint8Array(3));
  assert.throws(() => new WireReader(w.finish()).readLengthDelimited(), TruncationError);
});

test('reader: readBytes 返回拷贝，不悬挂引用输入缓冲', () => {
  const src = new Uint8Array([1, 2, 3, 4]);
  const r = new WireReader(src);
  const out = r.readBytes(4);
  src[0] = 0xff;
  assert.equal(out[0], 1);
});

test('reader: 非法 tag 被拒绝', () => {
  const w0 = new WireWriter();
  w0.writeVarint(0); // field number 0 非法
  assert.throws(() => new WireReader(w0.finish()).readTag(), MalformedError);
  const wGroup = new WireWriter();
  wGroup.writeVarint((1 << 3) | 3); // group 已废弃
  assert.throws(() => new WireReader(wGroup.finish()).readTag(), MalformedError);
});

test('reader: skip 覆盖全部 wire type', () => {
  const w = new WireWriter();
  w.writeVarint(12345);
  w.writeFixed64(0xfffffffffffffffen);
  w.writeLengthDelimited(new Uint8Array([9, 9, 9]));
  w.writeFixed32(7);
  w.writeVarint(0x2a); // tag: field 5, varint
  w.writeVarint(1);
  const r = new WireReader(w.finish());
  r.skip(WireType.Varint);
  r.skip(WireType.Fixed64);
  r.skip(WireType.LengthDelimited);
  r.skip(WireType.Fixed32);
  const tag = r.readTag();
  assert.equal(tag.fieldNumber, 5);
  assert.equal(r.readVarint(), 1n);
  assert.ok(r.eof);
});

test('reader: 窗口边界校验', () => {
  assert.throws(() => new WireReader(new Uint8Array(4), 2, 10), RangeError);
  assert.throws(() => new WireReader(new Uint8Array(4), 3, 2), RangeError);
  const r = new WireReader(new Uint8Array([0, 0, 5, 0]), 2, 3);
  assert.equal(r.remaining, 1);
  assert.throws(() => r.readFixed32(), TruncationError);
});
