import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WireReader, WIRE_VARINT, WIRE_LENGTH_DELIMITED, WIRE_FIXED32 } from '../src/reader.ts';
import { WireWriter } from '../src/writer.ts';
import { ProtobufDecodeError, ProtobufEncodeError } from '../src/errors.ts';

test('writer/reader: fixed32/fixed64/float/double 往返', () => {
  const w = new WireWriter();
  w.writeFixed32(0xdeadbeef);
  w.writeFixed64(0x0102030405060708n);
  w.writeFloat(3.5);
  w.writeDouble(-1.25e300);
  const r = new WireReader(w.finish());
  assert.equal(r.readFixed32(), 0xdeadbeef);
  assert.equal(r.readFixed64(), 0x0102030405060708n);
  assert.equal(r.readFloat(), 3.5);
  assert.equal(r.readDouble(), -1.25e300);
  assert.ok(r.eof);
});

test('reader: 越界读取全部安全失败', () => {
  const cases: Array<(r: WireReader) => void> = [
    (r) => r.readFixed32(),
    (r) => r.readFixed64(),
    (r) => r.readFloat(),
    (r) => r.readDouble(),
    (r) => r.readVarint(),
    (r) => r.readTag(),
    (r) => r.readBytes(1),
    (r) => r.readLengthDelimited(),
  ];
  for (const fn of cases) {
    const r = new WireReader(new Uint8Array([0x08, 0x01])); // 仅 2 字节
    r.readBytes(2);
    assert.throws(() => fn(r), ProtobufDecodeError);
  }
});

test('reader: 声明长度超过剩余字节时报告截断', () => {
  const w = new WireWriter();
  w.writeTag(1, WIRE_LENGTH_DELIMITED);
  w.writeVarint(100); // 声明 100 字节，实际只有 2
  w.writeRaw(new Uint8Array([1, 2]));
  const r = new WireReader(w.finish());
  r.readTag();
  assert.throws(() => r.readLengthDelimited(), (e: unknown) => {
    assert.ok(e instanceof ProtobufDecodeError);
    assert.match((e as Error).message, /Truncated length-delimited/);
    return true;
  });
});

test('reader: 非法 tag（字段号 0 / 保留 wire type）被拒绝', () => {
  assert.throws(() => new WireReader(new Uint8Array([0x00])).readTag(), /field number 0/);
  assert.throws(() => new WireReader(new Uint8Array([0x0b])).readTag(), /Unsupported wire type 1|3/);
  assert.throws(() => new WireReader(new Uint8Array([0x0f])).readTag(), /Unsupported wire type/);
});

test('reader: skipField 跳过未知字段且不越界', () => {
  const w = new WireWriter();
  w.writeTag(1, WIRE_VARINT).writeVarint(150);
  w.writeTag(2, WIRE_LENGTH_DELIMITED).writeLengthDelimited(new Uint8Array([9, 9, 9]));
  w.writeTag(3, WIRE_FIXED32).writeFixed32(7);
  const r = new WireReader(w.finish());
  for (let i = 0; i < 3; i++) {
    const tag = r.readTag();
    r.skipField(tag.wireType);
  }
  assert.ok(r.eof);
  // 截断的 skip 目标
  const r2 = new WireReader(new Uint8Array([0x12, 0x05, 0x01]));
  const t2 = r2.readTag();
  assert.throws(() => r2.skipField(t2.wireType), ProtobufDecodeError);
});

test('writer: 容量自动增长且数据完整', () => {
  const w = new WireWriter(4);
  const payload = new Uint8Array(10000).map((_, i) => i & 0xff);
  w.writeRaw(payload);
  assert.deepEqual(w.finish(), payload);
});

test('writer: 非法字段号抛编码错误', () => {
  assert.throws(() => new WireWriter().writeTag(0, WIRE_VARINT), ProtobufEncodeError);
});
