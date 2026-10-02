import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  decodeMessage, encodeMessage,
  WireReader, WireWriter, WireType, TruncationError, MalformedError,
} from '../src/index.ts';
import type { MessageDescriptor, DecodedMessage } from '../src/index.ts';

const Address: MessageDescriptor = {
  typeName: 'Address',
  fields: [
    { name: 'city', number: 1, kind: 'string' },
    { name: 'zip', number: 2, kind: 'fixed32' },
  ],
};

const Item: MessageDescriptor = {
  typeName: 'Item',
  fields: [
    { name: 'sku', number: 1, kind: 'string' },
    { name: 'qty', number: 2, kind: 'sint32' },
  ],
};

const Order: MessageDescriptor = {
  typeName: 'Order',
  fields: [
    { name: 'id', number: 1, kind: 'uint64', id: true },
    { name: 'userId', number: 2, kind: 'uint64', ref: 'User' },
    { name: 'note', number: 3, kind: 'string' },
    { name: 'amounts', number: 4, kind: 'sint32', repeated: true, packed: true },
    { name: 'tags', number: 5, kind: 'uint32', repeated: true, packed: true },
    { name: 'address', number: 6, kind: 'message', message: Address },
    { name: 'items', number: 7, kind: 'message', message: Item, repeated: true },
    { name: 'score', number: 8, kind: 'double' },
    { name: 'paid', number: 9, kind: 'bool' },
  ],
};

const sampleOrder = {
  id: 9007199254740993n, // > 2^53，验证 64 位精度
  userId: 42n,
  note: '嵌套×压缩 héllo',
  amounts: [0, -1, 1, -2147483648, 2147483647],
  tags: [7, 8, 9, 4294967295],
  address: { city: '上海', zip: 200001 },
  items: [
    { sku: 'A-1', qty: -3 },
    { sku: 'B-2', qty: 300 },
  ],
  score: 99.5,
  paid: true,
};

test('message: 多层嵌套 + packed repeated 全量往返', () => {
  const buf = encodeMessage(Order, sampleOrder);
  const decoded = decodeMessage(Order, buf) as Record<string, unknown>;
  assert.equal(decoded.id, 9007199254740993n);
  assert.equal(decoded.userId, 42n);
  assert.equal(decoded.note, '嵌套×压缩 héllo');
  assert.deepEqual(decoded.amounts, [0, -1, 1, -2147483648, 2147483647]);
  assert.deepEqual(decoded.tags, [7, 8, 9, 4294967295]);
  const addr = decoded.address as Record<string, unknown>;
  assert.equal(addr.$type, 'Address');
  assert.equal(addr.city, '上海');
  assert.equal(addr.zip, 200001);
  const items = decoded.items as Record<string, unknown>[];
  assert.equal(items.length, 2);
  assert.equal(items[0].sku, 'A-1');
  assert.equal(items[0].qty, -3);
  assert.equal(items[1].qty, 300);
  assert.equal(decoded.score, 99.5);
  assert.equal(decoded.paid, true);
  assert.deepEqual(decoded.$unknown, []);
});

test('message: packed 与 unpacked 两种形态均可解码', () => {
  const packed = encodeMessage(Order, sampleOrder);
  // 手工构造 unpacked 形态：tag(4, varint) + 逐元素 zigzag
  const w = new WireWriter();
  for (const v of [0, -1, 1]) {
    w.writeTag(4, WireType.Varint);
    w.writeVarint(BigInt((v << 1) ^ (v >> 31)) & 0xffffffffn);
  }
  const unpacked = w.finish();
  const a = decodeMessage(Order, packed) as Record<string, unknown>;
  const b = decodeMessage(Order, unpacked) as Record<string, unknown>;
  assert.deepEqual((a.amounts as number[]).slice(0, 3), [0, -1, 1]);
  assert.deepEqual(b.amounts, [0, -1, 1]);
});

test('message: 未知字段被保留并可无损回写', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.Varint).writeVarint(7n); // id
  w.writeTag(50, WireType.Varint).writeVarint(123456n); // 未知 varint
  w.writeTag(51, WireType.Fixed32).writeFixed32(0xabcd); // 未知 fixed32
  w.writeTag(52, WireType.LengthDelimited).writeLengthDelimited(new Uint8Array([1, 2, 3]));
  w.writeTag(53, WireType.Fixed64).writeFixed64(0xfeedfacecafen);
  const decoded = decodeMessage(Order, w.finish()) as DecodedMessage;
  assert.equal(decoded.$unknown.length, 4);
  const reencoded = encodeMessage(Order, decoded);
  const decoded2 = decodeMessage(Order, reencoded) as DecodedMessage;
  assert.deepEqual(
    decoded2.$unknown.map((u) => [u.fieldNumber, u.wireType]),
    decoded.$unknown.map((u) => [u.fieldNumber, u.wireType]),
  );
});

test('message: 字段截断 → TruncationError，且绝不越界读取', () => {
  const full = encodeMessage(Order, sampleOrder);
  // 在每个可能截断点截断，解码必须安全失败而非崩溃/越界
  let truncationSeen = 0;
  for (let cut = 1; cut < full.length; cut++) {
    const sliced = full.slice(0, cut);
    try {
      decodeMessage(Order, sliced);
    } catch (e) {
      assert.ok(e instanceof TruncationError || e instanceof MalformedError, `cut=${cut} got ${e}`);
      truncationSeen++;
    }
  }
  assert.ok(truncationSeen > 0, '至少一个截断点触发安全失败');
  // 明确验证：length-delimited 声明长度越过缓冲末尾
  const w = new WireWriter();
  w.writeTag(3, WireType.LengthDelimited).writeVarint(1000).writeBytes(new Uint8Array([0x41]));
  assert.throws(() => decodeMessage(Order, w.finish()), TruncationError);
});

test('message: 嵌套消息内部截断同样被防护', () => {
  const inner = encodeMessage(Address, { city: '上海', zip: 200001 });
  const w = new WireWriter();
  w.writeTag(6, WireType.LengthDelimited).writeLengthDelimited(inner.slice(0, inner.length - 2));
  assert.throws(() => decodeMessage(Order, w.finish()), TruncationError);
});

test('message: wire type 不匹配与非法 UTF-8 抛 MalformedError', () => {
  const w1 = new WireWriter();
  w1.writeTag(3, WireType.Varint).writeVarint(1n); // note 应为 length-delimited
  assert.throws(() => decodeMessage(Order, w1.finish()), MalformedError);
  const w2 = new WireWriter();
  w2.writeTag(3, WireType.LengthDelimited).writeLengthDelimited(new Uint8Array([0xff, 0xfe]));
  assert.throws(() => decodeMessage(Order, w2.finish()), MalformedError);
});

test('message: 嵌套深度限制防止栈耗尽', () => {
  const Node: MessageDescriptor = { typeName: 'Node', fields: [] };
  Node.fields.push({ name: 'next', number: 1, kind: 'message', message: Node });
  let buf = new Uint8Array(0);
  for (let i = 0; i < 200; i++) {
    const w = new WireWriter();
    w.writeTag(1, WireType.LengthDelimited).writeLengthDelimited(buf);
    buf = w.finish();
  }
  assert.throws(() => decodeMessage(Node, buf), MalformedError);
});

test('message: 空缓冲解码为全默认消息', () => {
  const decoded = decodeMessage(Order, new Uint8Array(0)) as Record<string, unknown>;
  assert.equal(decoded.id, undefined);
  assert.deepEqual(decoded.amounts, []);
  assert.deepEqual(decoded.items, []);
});
