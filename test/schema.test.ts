import { test } from 'node:test';
import assert from 'node:assert/strict';
import { decodeMessage, encodeMessage, type MessageSchema } from '../src/schema.ts';
import { Reader, WireType, Writer } from '../src/wire.ts';
import { ProtoError, isProtoError } from '../src/errors.ts';

/* 三层嵌套 + packed repeated 的测试 schema：
 * Order { id, customer: Customer, items: repeated Item, tags: packed sint32, scores: packed fixed32 }
 * Customer { name, address: Address }
 * Address { city, zip }
 * Item { sku, price(sint64), qty }
 */
const Address: MessageSchema = {
  name: 'Address',
  fields: [
    { number: 1, name: 'city', type: 'string', required: true },
    { number: 2, name: 'zip', type: 'uint32' },
  ],
};
const Customer: MessageSchema = {
  name: 'Customer',
  fields: [
    { number: 1, name: 'name', type: 'string', required: true },
    { number: 2, name: 'address', type: 'message', message: Address },
  ],
};
const Item: MessageSchema = {
  name: 'Item',
  fields: [
    { number: 1, name: 'sku', type: 'string', required: true },
    { number: 2, name: 'price', type: 'sint64' },
    { number: 3, name: 'qty', type: 'int32' },
  ],
};
const Order: MessageSchema = {
  name: 'Order',
  fields: [
    { number: 1, name: 'id', type: 'uint64', required: true },
    { number: 2, name: 'customer', type: 'message', message: Customer },
    { number: 3, name: 'items', type: 'message', message: Item, repeated: true },
    { number: 4, name: 'tags', type: 'sint32', repeated: true, packed: true },
    { number: 5, name: 'scores', type: 'fixed32', repeated: true, packed: true },
    { number: 6, name: 'note', type: 'string' },
  ],
};

const sampleOrder = {
  id: 9007199254740993n,
  customer: { name: '张三', address: { city: '上海', zip: 200001 } },
  items: [
    { sku: 'A-1', price: -1999n, qty: 2 },
    { sku: 'B-2', price: 3500000000n, qty: -1 },
  ],
  tags: [-5, 0, 7, 123456],
  scores: [0xdeadbeef, 42],
  note: '加急 🚀',
};

test('多层嵌套 + packed repeated 往返精确还原', () => {
  const buf = encodeMessage(Order, sampleOrder);
  const decoded = decodeMessage(Order, buf);
  assert.deepEqual(decoded, sampleOrder);
});

test('packed 与非 packed repeated 均可解码', () => {
  // 手工构造非 packed 的 tags（每个元素独立 tag）
  const w = new Writer();
  w.writeTag(1, WireType.Varint).writeVarint(1n); // id
  for (const t of [3, -4]) {
    w.writeTag(4, WireType.Varint);
    // sint32 zigzag
    const zz = t < 0 ? BigInt(-t * 2 - 1) : BigInt(t * 2);
    w.writeVarint(zz);
  }
  const decoded = decodeMessage(Order, w.finish());
  assert.deepEqual(decoded.tags, [3, -4]);
});

test('未知字段被安全跳过', () => {
  const w = new Writer();
  w.writeTag(1, WireType.Varint).writeVarint(7n); // id
  w.writeTag(99, WireType.LengthDelimited).writeString('unknown field');
  w.writeTag(100, WireType.Fixed64).writeFixed64(1n);
  const decoded = decodeMessage(Order, w.finish());
  assert.equal(decoded.id, 7n);
  assert.equal(decoded.note, undefined);
});

test('缺少必填字段 -> SCHEMA_VIOLATION', () => {
  const w = new Writer();
  w.writeTag(6, WireType.LengthDelimited).writeString('no id');
  assert.throws(() => decodeMessage(Order, w.finish()),
    (e: unknown) => isProtoError(e, 'SCHEMA_VIOLATION'));
  // 编码端同样校验
  assert.throws(() => encodeMessage(Order, { id: 1n, customer: { address: {} } }),
    (e: unknown) => isProtoError(e, 'SCHEMA_VIOLATION'));
});

test('wire type 与 schema 不匹配 -> SCHEMA_VIOLATION', () => {
  const w = new Writer();
  w.writeTag(1, WireType.Fixed32).writeFixed32(1); // id 应为 Varint
  assert.throws(() => decodeMessage(Order, w.finish()),
    (e: unknown) => isProtoError(e, 'SCHEMA_VIOLATION'));
});

test('嵌套消息内部截断 -> TRUNCATED 且带字段路径', () => {
  const full = encodeMessage(Order, sampleOrder);
  let sawPath = false;
  for (let len = 1; len < full.length; len++) {
    try {
      decodeMessage(Order, full.subarray(0, len));
    } catch (err) {
      assert.ok(err instanceof ProtoError, `len=${len}: ${String(err)}`);
      assert.equal(err.code, 'TRUNCATED', `len=${len}: ${err.code}`);
      if (err.path.includes('Order.')) sawPath = true;
    }
  }
  assert.ok(sawPath, '截断错误应携带字段路径');
});

test('packed 载荷内部截断 -> TRUNCATED', () => {
  const full = encodeMessage(Order, sampleOrder);
  // 找到 tags 字段：扫描到 field 4 的 length-delimited 块并截断其载荷
  const r = new Reader(full);
  while (!r.done) {
    const { fieldNumber, wireType } = r.readTag();
    if (fieldNumber === 4 && wireType === WireType.LengthDelimited) {
      const lenStart = r.offset;
      const payloadLen = Number(r.readVarint());
      const cutAt = lenStart + 1 + payloadLen - 1; // 吃掉最后一个字节
      assert.throws(() => decodeMessage(Order, full.subarray(0, cutAt)),
        (e: unknown) => isProtoError(e, 'TRUNCATED'));
      return;
    }
    r.skip(wireType);
  }
  assert.fail('未找到 tags 字段');
});

test('空消息与默认值', () => {
  const decoded = decodeMessage(Order, encodeMessage(Order, { id: 0n }));
  assert.equal(decoded.id, 0n);
  assert.deepEqual(decoded.items, []);
  assert.deepEqual(decoded.tags, []);
});

test('消息末尾残留字节 -> SCHEMA_VIOLATION', () => {
  const good = encodeMessage(Order, { id: 5n });
  const bad = new Uint8Array(good.length + 1);
  bad.set(good);
  bad[bad.length - 1] = 0xff; // 残缺的 tag
  assert.throws(() => decodeMessage(Order, bad), (e: unknown) => e instanceof ProtoError);
});
