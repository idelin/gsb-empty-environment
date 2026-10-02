import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  MaxDepthExceededError,
  SchemaError,
  decodeMessage,
  encodeMessage,
  type MessageSchema,
} from '../src/message.ts';
import { CodecError, TruncatedFieldError } from '../src/wire.ts';

// 三层嵌套：Person -> Address -> Geo
const Geo: MessageSchema = {
  name: 'Geo',
  fields: [
    { name: 'lat', number: 1, type: 'sint64' },
    { name: 'lng', number: 2, type: 'sint64' },
  ],
};

const Address: MessageSchema = {
  name: 'Address',
  fields: [
    { name: 'city', number: 1, type: 'string' },
    { name: 'zip', number: 2, type: 'fixed32' },
    { name: 'geo', number: 3, type: 'message', message: Geo },
  ],
};

const Person: MessageSchema = {
  name: 'Person',
  fields: [
    { name: 'id', number: 1, type: 'uint64' },
    { name: 'name', number: 2, type: 'string' },
    { name: 'email', number: 3, type: 'string' },
    { name: 'scores', number: 4, type: 'sint32', repeated: true, packed: true },
    { name: 'tags', number: 5, type: 'string', repeated: true },
    { name: 'address', number: 6, type: 'message', message: Address },
    { name: 'previous', number: 7, type: 'message', message: Address, repeated: true },
    { name: 'balance', number: 8, type: 'sfixed64' },
    { name: 'active', number: 9, type: 'bool' },
    { name: 'avatar', number: 10, type: 'bytes' },
  ],
};

const sample = {
  id: 9007199254740993n, // > 2^53，验证 bigint 精度
  name: '张三',
  email: 'zhangsan@example.com',
  scores: [-5, 0, 7, 300, -2147483648],
  tags: ['admin', '早期用户'],
  address: {
    city: '上海',
    zip: 200000,
    geo: { lat: 31123040n, lng: -121473700n },
  },
  previous: [
    { city: '北京', zip: 100000, geo: { lat: 39904200n, lng: 116407400n } },
    { city: '深圳', zip: 518000, geo: { lat: 22543100n, lng: 114057900n } },
  ],
  balance: -123456789012345n,
  active: true,
  avatar: Uint8Array.of(0x89, 0x50, 0x4e, 0x47),
};

test('多层嵌套 + packed repeated: 编码-解码完整往返', () => {
  const bytes = encodeMessage(Person, sample);
  const decoded = decodeMessage(Person, bytes) as typeof sample;
  assert.equal(decoded.id, sample.id);
  assert.equal(decoded.name, sample.name);
  assert.equal(decoded.email, sample.email);
  assert.deepEqual(decoded.scores, sample.scores);
  assert.deepEqual(decoded.tags, sample.tags);
  assert.deepEqual(decoded.address, sample.address);
  assert.deepEqual(decoded.previous, sample.previous);
  assert.equal(decoded.balance, sample.balance);
  assert.equal(decoded.active, sample.active);
  assert.deepEqual(decoded.avatar, sample.avatar);
});

test('packed repeated 与 unpacked 编码均可解码（兼容性）', () => {
  // 手工构造 unpacked 形式：field 4 (sint32) 逐元素 varint+tag
  // tag = (4<<3)|0 = 0x20; sint32 值 1 -> zigzag 2 -> 0x02; 值 -1 -> zigzag 1 -> 0x01
  const bytes = Uint8Array.of(0x20, 0x02, 0x20, 0x01);
  const decoded = decodeMessage(Person, bytes) as typeof sample;
  assert.deepEqual(decoded.scores, [1, -1]);
});

test('未知字段被安全跳过', () => {
  const bytes = encodeMessage(Person, sample);
  // 追加未知字段：field 99 varint=1, field 100 length-delimited
  const extra = Uint8Array.of(
    0xf8, 0x06, 0x01, // field 99, varint 1
    0xa2, 0x06, 0x03, 0x61, 0x62, 0x63, // field 100, "abc"
  );
  const merged = new Uint8Array(bytes.length + extra.length);
  merged.set(bytes);
  merged.set(extra, bytes.length);
  const decoded = decodeMessage(Person, merged) as typeof sample;
  assert.equal(decoded.name, sample.name);
  assert.equal((decoded as Record<string, unknown>)['99'], undefined);
});

test('字段截断: 顶层 varint 字段截断 -> TruncatedFieldError', () => {
  const bytes = encodeMessage(Person, sample);
  const truncated = bytes.subarray(0, bytes.length - 3); // 切掉尾部
  assert.throws(() => decodeMessage(Person, truncated), TruncatedFieldError);
});

test('字段截断: 嵌套消息声明长度超过实际 -> 安全越界防护并携带字段号', () => {
  const bytes = encodeMessage(Person, sample);
  // 找到 address (field 6, tag=0x32) 并篡改其长度前缀为超大值
  const idx = bytes.indexOf(0x32);
  assert.ok(idx > 0);
  const evil = bytes.slice(0, idx + 1);
  const padded = new Uint8Array(evil.length + 3);
  padded.set(evil);
  padded[idx + 1] = 0x7f; // 声明 127 字节，实际后面只有 2 字节
  assert.throws(
    () => decodeMessage(Person, padded),
    (e: unknown) => {
      assert.ok(e instanceof TruncatedFieldError);
      assert.equal((e as TruncatedFieldError).fieldNumber, 6);
      return true;
    },
  );
});

test('字段截断: packed 数组内部截断 -> TruncatedFieldError', () => {
  // field 4 packed: tag 0x22, len=5, 但只给 2 字节数据
  const bytes = Uint8Array.of(0x22, 0x05, 0x02, 0x04);
  assert.throws(() => decodeMessage(Person, bytes), TruncatedFieldError);
});

test('嵌套深度限制: 超限抛 MaxDepthExceededError', () => {
  // 自引用 schema 构造无限嵌套
  const Node: MessageSchema = { name: 'Node', fields: [] };
  Node.fields.push({ name: 'child', number: 1, type: 'message', message: Node });
  let bytes = new Uint8Array(0);
  for (let i = 0; i < 10; i++) {
    const next = new Uint8Array(bytes.length + 2);
    next[0] = 0x0a; // field 1, length-delimited
    next[1] = bytes.length;
    next.set(bytes, 2);
    bytes = next;
  }
  assert.throws(() => decodeMessage(Node, bytes, { maxDepth: 5 }), MaxDepthExceededError);
  // 足够深度下可正常解码
  assert.doesNotThrow(() => decodeMessage(Node, bytes, { maxDepth: 20 }));
});

test('消息大小限制: maxBytes 生效', () => {
  const bytes = encodeMessage(Person, sample);
  assert.throws(() => decodeMessage(Person, bytes, { maxBytes: 4 }), CodecError);
});

test('schema 校验: 重复字段号 / 非法 packed', () => {
  const bad: MessageSchema = {
    name: 'Bad',
    fields: [
      { name: 'a', number: 1, type: 'int32' },
      { name: 'b', number: 1, type: 'int32' },
    ],
  };
  assert.throws(() => encodeMessage(bad, {}), SchemaError);
  const badPacked: MessageSchema = {
    name: 'BadPacked',
    fields: [{ name: 's', number: 1, type: 'string', repeated: true, packed: true }],
  };
  assert.throws(() => encodeMessage(badPacked, { s: [] }), SchemaError);
});

test('wire type 不匹配抛 SchemaError', () => {
  // field 2 (name, string) 期望 LengthDelimited，但给 varint: tag=(2<<3)|0=0x10
  assert.throws(() => decodeMessage(Person, Uint8Array.of(0x10, 0x01)), SchemaError);
});

test('空消息与缺省字段', () => {
  const decoded = decodeMessage(Person, new Uint8Array(0)) as Record<string, unknown>;
  assert.deepEqual(decoded.scores, []);
  assert.deepEqual(decoded.tags, []);
  assert.equal(decoded.name, undefined);
});
