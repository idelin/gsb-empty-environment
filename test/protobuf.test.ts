import { test } from 'node:test';
import assert from 'node:assert/strict';

import { WireError, DecodeError } from '../src/protobuf/errors.ts';
import { encodeVarint, decodeVarint, zigzagEncode, zigzagDecode } from '../src/protobuf/varint.ts';
import { WireReader, WireType } from '../src/protobuf/reader.ts';
import { WireWriter } from '../src/protobuf/writer.ts';
import { decodeMessage, validateMessage, type MessageSchema } from '../src/protobuf/schema.ts';

const Address: MessageSchema = {
  name: 'Address',
  fields: {
    1: { name: 'city', type: 'string' },
    2: { name: 'zip', type: 'sint32' },
  },
};

const Person: MessageSchema = {
  name: 'Person',
  fields: {
    1: { name: 'name', type: 'string' },
    2: { name: 'id', type: 'int32' },
    3: { name: 'email', type: 'string' },
    4: { name: 'address', type: Address },
    5: { name: 'scores', type: 'sint32', repeated: true, packed: true },
    6: { name: 'lucky', type: 'fixed32', repeated: true, packed: true },
    7: { name: 'active', type: 'bool' },
    8: { name: 'balance', type: 'int64' },
    9: { name: 'avatar', type: 'bytes' },
    10: { name: 'ratio', type: 'double' },
    11: { name: 'previous', type: Address, repeated: true },
  },
};

function encodeAddress(city: string, zip: number): Uint8Array {
  const w = new WireWriter();
  w.writeTag(1, WireType.LengthDelimited).writeLengthDelimited(new TextEncoder().encode(city));
  w.writeTag(2, WireType.Varint).writeVarint(zigzagEncode(zip));
  return w.finish();
}

function encodePerson(): Uint8Array {
  const w = new WireWriter();
  const enc = new TextEncoder();
  w.writeTag(1, WireType.LengthDelimited).writeLengthDelimited(enc.encode('Alice'));
  w.writeTag(2, WireType.Varint).writeVarint(123);
  w.writeTag(3, WireType.LengthDelimited).writeLengthDelimited(enc.encode('alice@example.com'));
  w.writeTag(4, WireType.LengthDelimited).writeLengthDelimited(encodeAddress('Shanghai', -200000));
  const packed = new WireWriter();
  for (const s of [1, -2, 3, -4, 5000]) packed.writeVarint(zigzagEncode(s));
  w.writeTag(5, WireType.LengthDelimited).writeLengthDelimited(packed.finish());
  const packedFixed = new WireWriter();
  for (const n of [7, 8, 4294967295]) packedFixed.writeFixed32(n);
  w.writeTag(6, WireType.LengthDelimited).writeLengthDelimited(packedFixed.finish());
  w.writeTag(7, WireType.Varint).writeVarint(1);
  w.writeTag(8, WireType.Varint).writeVarint(-5n);
  w.writeTag(9, WireType.LengthDelimited).writeLengthDelimited(Uint8Array.from([1, 2, 3, 255]));
  w.writeTag(10, WireType.Fixed64).writeDouble(3.141592653589793);
  w.writeTag(11, WireType.LengthDelimited).writeLengthDelimited(encodeAddress('Beijing', 100000));
  w.writeTag(11, WireType.LengthDelimited).writeLengthDelimited(encodeAddress('Hangzhou', 310000));
  return w.finish();
}

test('varint: 7 位压缩编码往返（含边界值）', () => {
  const cases: bigint[] = [0n, 1n, 127n, 128n, 300n, 16384n, 4294967295n, 4294967296n, 2n ** 64n - 1n];
  for (const v of cases) {
    const encoded = encodeVarint(v);
    const { value, next } = decodeVarint(encoded, 0, encoded.length);
    assert.equal(value, v);
    assert.equal(next, encoded.length);
  }
  assert.equal(encodeVarint(127).length, 1);
  assert.equal(encodeVarint(128).length, 2);
  assert.equal(encodeVarint(2n ** 64n - 1n).length, 10);
});

test('varint: 负数按 64 位符号扩展编码为 10 字节', () => {
  const encoded = encodeVarint(-1);
  assert.equal(encoded.length, 10);
  const { value } = decodeVarint(encoded, 0, encoded.length);
  assert.equal(value, 2n ** 64n - 1n);
});

test('varint: 截断时触发安全越界防护', () => {
  assert.throws(() => decodeVarint(Uint8Array.from([0x80]), 0, 1), (err: unknown) => {
    assert.ok(err instanceof WireError);
    assert.match((err as Error).message, /截断.*越界防护/);
    return true;
  });
  assert.throws(
    () => decodeVarint(Uint8Array.from([0xff, 0xff, 0xff]), 0, 3),
    /截断/,
  );
});

test('varint: 超过 64 位上限（含第 11 字节）被拒绝', () => {
  const tooLong = new Uint8Array(11).fill(0x80);
  tooLong[10] = 0x00;
  assert.throws(() => decodeVarint(tooLong, 0, tooLong.length), /64 位/);
  const overflow = new Uint8Array(10).fill(0xff);
  overflow[9] = 0x7f;
  assert.throws(() => decodeVarint(overflow, 0, overflow.length), /64 位/);
});

test('zigzag: 有符号整数映射往返', () => {
  assert.equal(zigzagEncode(0), 0n);
  assert.equal(zigzagEncode(-1), 1n);
  assert.equal(zigzagEncode(1), 2n);
  assert.equal(zigzagEncode(-2), 3n);
  assert.equal(zigzagEncode(2), 4n);
  const extremes = [0n, 1n, -1n, 2n ** 63n - 1n, -(2n ** 63n), 123456789012345n, -987654321098765n];
  for (const v of extremes) {
    assert.equal(zigzagDecode(zigzagEncode(v)), v);
  }
});

test('fixed32/fixed64: 小端编码往返与截断防护', () => {
  const w = new WireWriter();
  w.writeFixed32(0xdeadbeef).writeFixed64(0x0102030405060708n);
  const buf = w.finish();
  const r = new WireReader(buf);
  assert.equal(r.readFixed32(), 0xdeadbeef);
  assert.equal(r.readFixed64(), 0x0102030405060708n);
  assert.ok(r.done);
  assert.throws(() => new WireReader(Uint8Array.from([1, 2])).readFixed32(), /截断.*越界防护/);
  assert.throws(() => new WireReader(Uint8Array.from([1, 2, 3, 4, 5])).readFixed64(), /截断/);
});

test('float/double: IEEE754 小端往返', () => {
  const w = new WireWriter();
  w.writeFloat(1.5).writeDouble(-273.15);
  const r = new WireReader(w.finish());
  assert.equal(r.readFloat(), 1.5);
  assert.equal(r.readDouble(), -273.15);
});

test('嵌套消息 + Packed Repeated：完整结构还原', () => {
  const decoded = decodeMessage(Person, encodePerson()) as Record<string, any>;
  assert.equal(decoded.name, 'Alice');
  assert.equal(decoded.id, 123);
  assert.equal(decoded.email, 'alice@example.com');
  assert.deepEqual(decoded.address, { city: 'Shanghai', zip: -200000 });
  assert.deepEqual(decoded.scores, [1, -2, 3, -4, 5000]);
  assert.deepEqual(decoded.lucky, [7, 8, 4294967295]);
  assert.equal(decoded.active, true);
  assert.equal(decoded.balance, -5n);
  assert.deepEqual(Array.from(decoded.avatar as Uint8Array), [1, 2, 3, 255]);
  assert.equal(decoded.ratio, 3.141592653589793);
  assert.deepEqual(decoded.previous, [
    { city: 'Beijing', zip: 100000 },
    { city: 'Hangzhou', zip: 310000 },
  ]);
});

test('packed 字段同时兼容非 packed（unpacked）编码', () => {
  const w = new WireWriter();
  for (const s of [10, -20, 30]) {
    w.writeTag(5, WireType.Varint).writeVarint(zigzagEncode(s));
  }
  const decoded = decodeMessage(Person, w.finish()) as Record<string, any>;
  assert.deepEqual(decoded.scores, [10, -20, 30]);
});

test('三层嵌套消息逐层还原', () => {
  const Level3: MessageSchema = { name: 'L3', fields: { 1: { name: 'v', type: 'uint32' } } };
  const Level2: MessageSchema = { name: 'L2', fields: { 1: { name: 'l3', type: Level3 } } };
  const Level1: MessageSchema = { name: 'L1', fields: { 1: { name: 'l2', type: Level2 } } };
  const inner = new WireWriter().writeTag(1, WireType.Varint).writeVarint(42).finish();
  const mid = new WireWriter().writeTag(1, WireType.LengthDelimited).writeLengthDelimited(inner).finish();
  const outer = new WireWriter().writeTag(1, WireType.LengthDelimited).writeLengthDelimited(mid).finish();
  const decoded = decodeMessage(Level1, outer) as Record<string, any>;
  assert.deepEqual(decoded, { l2: { l3: { v: 42 } } });
});

test('未知字段被安全跳过（varint/fixed32/fixed64/length-delimited）', () => {
  const w = new WireWriter();
  w.writeTag(100, WireType.Varint).writeVarint(999);
  w.writeTag(101, WireType.Fixed32).writeFixed32(1);
  w.writeTag(102, WireType.Fixed64).writeFixed64(2n);
  w.writeTag(103, WireType.LengthDelimited).writeLengthDelimited(Uint8Array.from([9, 9, 9]));
  w.writeTag(2, WireType.Varint).writeVarint(7);
  const decoded = decodeMessage(Person, w.finish()) as Record<string, any>;
  assert.deepEqual(decoded, { id: 7 });
});

test('strict 模式下未知字段报错', () => {
  const w = new WireWriter();
  w.writeTag(100, WireType.Varint).writeVarint(1);
  assert.throws(() => decodeMessage(Person, w.finish(), { strict: true }), (err: unknown) => {
    assert.ok(err instanceof DecodeError);
    assert.match((err as Error).message, /未知字段号 100/);
    return true;
  });
});

test('字段截断：length-delimited 声明长度超过剩余字节', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.LengthDelimited).writeVarint(100);
  w.writeLengthDelimited(new TextEncoder().encode('ab'));
  const buf = w.finish().subarray(0, 4);
  assert.throws(() => decodeMessage(Person, buf), (err: unknown) => {
    assert.ok(err instanceof DecodeError);
    assert.match((err as Error).message, /越界防护/);
    assert.match((err as Error).message, /Person\.name/);
    return true;
  });
});

test('字段截断：消息在 varint 中途结束', () => {
  const buf = Uint8Array.from([0x10, 0x80]);
  assert.throws(() => decodeMessage(Person, buf), /截断/);
});

test('字段截断：tag 自身被截断', () => {
  const buf = Uint8Array.from([0x80]);
  assert.throws(() => decodeMessage(Person, buf), /截断/);
});

test('非法 wire type（6/7）与废弃 group 被拒绝', () => {
  assert.throws(() => decodeMessage(Person, Uint8Array.from([0x0f, 0x00])), /非法 wire type 7/);
  assert.throws(() => decodeMessage(Person, Uint8Array.from([0x0e, 0x00])), /非法 wire type 6/);
  assert.throws(() => decodeMessage(Person, Uint8Array.from([0x0b, 0x00])), /group/);
});

test('wire type 与 schema 字段类型不匹配时报错', () => {
  const w = new WireWriter();
  w.writeTag(2, WireType.Fixed32).writeFixed32(1);
  assert.throws(() => decodeMessage(Person, w.finish()), /不匹配/);
});

test('string 字段非法 UTF-8 序列被拒绝', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.LengthDelimited).writeLengthDelimited(Uint8Array.from([0xff, 0xfe]));
  assert.throws(() => decodeMessage(Person, w.finish()), /UTF-8/);
});

test('packed fixed32 长度不是 4 的倍数时触发越界防护', () => {
  const w = new WireWriter();
  w.writeTag(6, WireType.LengthDelimited).writeLengthDelimited(Uint8Array.from([1, 2, 3, 4, 5]));
  assert.throws(() => decodeMessage(Person, w.finish()), /截断|越界/);
});

test('嵌套深度上限防止资源耗尽', () => {
  const Node: MessageSchema = { name: 'Node', fields: {} };
  (Node.fields as Record<number, unknown>)[1] = { name: 'child', type: Node };
  let buf = new Uint8Array(0);
  for (let i = 0; i < 10; i++) {
    buf = new WireWriter().writeTag(1, WireType.LengthDelimited).writeLengthDelimited(buf).finish();
  }
  assert.throws(() => decodeMessage(Node, buf, { maxDepth: 3 }), /嵌套深度超过上限 3/);
  assert.doesNotThrow(() => decodeMessage(Node, buf, { maxDepth: 20 }));
});

test('validateMessage 返回结构化校验结果', () => {
  const good = validateMessage(Person, encodePerson());
  assert.equal(good.ok, true);
  assert.deepEqual(good.errors, []);
  const bad = validateMessage(Person, Uint8Array.from([0x11, 0x01]));
  assert.equal(bad.ok, false);
  assert.equal(bad.errors.length, 1);
  assert.match(bad.errors[0], /Person/);
});

test('空消息与空缓冲区合法', () => {
  const decoded = decodeMessage(Person, new Uint8Array(0));
  assert.deepEqual(decoded, {});
});
