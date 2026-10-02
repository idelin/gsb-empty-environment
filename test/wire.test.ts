import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ByteReader,
  ByteWriter,
  CodecError,
  MalformedVarintError,
  TruncatedFieldError,
  WireType,
  encodeVarint,
  zigzagDecode,
  zigzagEncode,
} from '../src/wire.ts';

test('varint: 已知向量 (Protocol Buffers 官方示例)', () => {
  // 1 -> 0x01
  assert.deepEqual([...encodeVarint(1)], [0x01]);
  // 127 -> 0x7F (单字节上限)
  assert.deepEqual([...encodeVarint(127)], [0x7f]);
  // 128 -> 0x80 0x01 (进入 7 位压缩续位)
  assert.deepEqual([...encodeVarint(128)], [0x80, 0x01]);
  // 300 -> 0xAC 0x02 (官方文档示例)
  assert.deepEqual([...encodeVarint(300)], [0xac, 0x02]);
  // 0 -> 0x00
  assert.deepEqual([...encodeVarint(0)], [0x00]);
});

test('varint: 64 位边界值往返', () => {
  const values = [
    0n,
    1n,
    127n,
    128n,
    (1n << 32n) - 1n,
    1n << 32n,
    (1n << 53n) - 1n, // Number.MAX_SAFE_INTEGER
    (1n << 63n) - 1n, // int64 max
    (1n << 64n) - 1n, // uint64 max
  ];
  for (const v of values) {
    const encoded = encodeVarint(v);
    assert.ok(encoded.length <= 10, 'varint 最多 10 字节');
    const r = new ByteReader(encoded);
    assert.equal(r.readVarint(), v);
    assert.ok(r.eof);
  }
  // uint64 max 必须恰好 10 字节
  assert.equal(encodeVarint((1n << 64n) - 1n).length, 10);
});

test('varint: 负数按 int64 补码编码为 10 字节', () => {
  const encoded = encodeVarint(-1);
  assert.equal(encoded.length, 10);
  assert.equal(encoded[0], 0xff);
  assert.equal(encoded[9], 0x01);
});

test('varint: 截断时抛 TruncatedFieldError（安全越界防护）', () => {
  // 0x80 设置了续位但流已结束
  const r = new ByteReader(Uint8Array.of(0x80));
  assert.throws(() => r.readVarint(), (e: unknown) => {
    assert.ok(e instanceof TruncatedFieldError);
    assert.equal((e as TruncatedFieldError).needed, 1);
    assert.equal((e as TruncatedFieldError).remaining, 0);
    return true;
  });
  // 多字节续位中途截断
  const r2 = new ByteReader(Uint8Array.of(0xff, 0xff, 0xff));
  assert.throws(() => r2.readVarint(), TruncatedFieldError);
});

test('varint: 超过 10 字节未终止抛 MalformedVarintError', () => {
  const r = new ByteReader(new Uint8Array(11).fill(0x80));
  assert.throws(() => r.readVarint(), MalformedVarintError);
});

test('zigzag: 映射表与往返', () => {
  const cases: Array<[bigint, bigint]> = [
    [0n, 0n],
    [-1n, 1n],
    [1n, 2n],
    [-2n, 3n],
    [2n, 4n],
    [2147483647n, 4294967294n],
    [-2147483648n, 4294967295n],
    [(1n << 63n) - 1n, (1n << 64n) - 2n],
    [-(1n << 63n), (1n << 64n) - 1n],
  ];
  for (const [signed, unsigned] of cases) {
    assert.equal(zigzagEncode(signed), unsigned);
    assert.equal(zigzagDecode(unsigned), signed);
  }
});

test('fixed32/fixed64: 小端序往返与边界', () => {
  const w = new ByteWriter();
  w.writeFixed32(0xdeadbeef).writeFixed64(0x0102030405060708n);
  const bytes = w.concat();
  assert.equal(bytes.length, 12);
  // 小端序验证
  assert.deepEqual([...bytes.slice(0, 4)], [0xef, 0xbe, 0xad, 0xde]);
  assert.deepEqual([...bytes.slice(4)], [8, 7, 6, 5, 4, 3, 2, 1]);
  const r = new ByteReader(bytes);
  assert.equal(r.readFixed32(), 0xdeadbeef);
  assert.equal(r.readFixed64(), 0x0102030405060708n);
  assert.ok(r.eof);
});

test('fixed32/fixed64: 截断防护', () => {
  assert.throws(() => new ByteReader(Uint8Array.of(1, 2, 3)).readFixed32(), (e: unknown) => {
    assert.ok(e instanceof TruncatedFieldError);
    assert.equal((e as TruncatedFieldError).needed, 4);
    assert.equal((e as TruncatedFieldError).remaining, 3);
    return true;
  });
  assert.throws(() => new ByteReader(new Uint8Array(7)).readFixed64(), TruncatedFieldError);
});

test('length-delimited: 长度前缀超过剩余字节时防护', () => {
  // 声明长度 100，实际只有 2 字节
  const r = new ByteReader(Uint8Array.of(100, 0xaa, 0xbb));
  assert.throws(() => r.readBytes(), (e: unknown) => {
    assert.ok(e instanceof TruncatedFieldError);
    assert.equal((e as TruncatedFieldError).needed, 100);
    assert.equal((e as TruncatedFieldError).remaining, 2);
    return true;
  });
});

test('tag: 解析与非法输入', () => {
  const w = new ByteWriter();
  w.writeTag(15, WireType.Fixed32);
  const r = new ByteReader(w.concat());
  assert.deepEqual(r.readTag(), [15, WireType.Fixed32]);
  // 字段号 0 非法
  assert.throws(() => new ByteReader(Uint8Array.of(0x00)).readTag(), CodecError);
  // wire type 3/4 (已废弃的 group) 不支持
  assert.throws(() => new ByteReader(Uint8Array.of(0x0b)).readTag(), CodecError);
});

test('ByteReader: 空缓冲区与零长度窗口安全', () => {
  const r = new ByteReader(new Uint8Array(0));
  assert.ok(r.eof);
  assert.equal(r.remaining, 0);
  assert.throws(() => r.readByte(), TruncatedFieldError);
  // 初始化窗口越界
  assert.throws(() => new ByteReader(new Uint8Array(2), 1, 5), TruncatedFieldError);
});

test('writer: 复合写出后完整往返', () => {
  const w = new ByteWriter();
  w.writeTag(1, WireType.Varint).writeVarint(150);
  w.writeTag(2, WireType.LengthDelimited).writeString('你好 protobuf');
  w.writeTag(3, WireType.Fixed64).writeFixed64(42n);
  w.writeTag(4, WireType.Fixed32).writeFixed32(7);
  const r = new ByteReader(w.concat());
  assert.deepEqual(r.readTag(), [1, WireType.Varint]);
  assert.equal(r.readVarint(), 150n);
  assert.deepEqual(r.readTag(), [2, WireType.LengthDelimited]);
  assert.equal(r.readString(), '你好 protobuf');
  assert.deepEqual(r.readTag(), [3, WireType.Fixed64]);
  assert.equal(r.readFixed64(), 42n);
  assert.deepEqual(r.readTag(), [4, WireType.Fixed32]);
  assert.equal(r.readFixed32(), 7);
  assert.ok(r.eof);
});
