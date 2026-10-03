import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MessageSchema, type MessageValue } from '../src/schema.ts';
import { ProtobufDecodeError } from '../src/errors.ts';
import { WireWriter } from '../src/writer.ts';
import { WIRE_VARINT, WIRE_FIXED64 } from '../src/reader.ts';

// 三层嵌套 + packed repeated 的测试 schema
const Point = new MessageSchema('Point', [
  { name: 'x', number: 1, type: 'sint32' },
  { name: 'y', number: 2, type: 'sint32' },
]);

const Track = new MessageSchema('Track', [
  { name: 'id', number: 1, type: 'uint64' },
  { name: 'points', number: 2, type: 'message', repeated: true, packed: false, messageType: Point },
  { name: 'deltas', number: 3, type: 'sint64', repeated: true, packed: true },
]);

const Session = new MessageSchema('Session', [
  { name: 'name', number: 1, type: 'string' },
  { name: 'version', number: 2, type: 'fixed32' },
  { name: 'checksum', number: 3, type: 'fixed64' },
  { name: 'track', number: 4, type: 'message', messageType: Track },
  { name: 'scores', number: 5, type: 'int32', repeated: true, packed: true },
  { name: 'flags', number: 6, type: 'bool', repeated: true, packed: true },
  { name: 'blob', number: 7, type: 'bytes' },
  { name: 'ratio', number: 8, type: 'double' },
]);

const sample: MessageValue = {
  name: '会话-α',
  version: 0x01020304,
  checksum: 0xffffffffffffffffn,
  track: {
    id: 18446744073709551615n,
    points: [
      { x: -5, y: 7 },
      { x: 0, y: -2147483648 },
      { x: 2147483647, y: 1 },
    ],
    deltas: [0n, -1n, 1n, 9223372036854775807n, -9223372036854775808n],
  },
  scores: [1, -2, 300, -2147483648, 2147483647],
  flags: [true, false, true, true],
  blob: new Uint8Array([0, 1, 2, 255]),
  ratio: Math.PI,
};

test('schema: 多层嵌套 + packed repeated 完整往返', () => {
  const buf = Session.encode(sample);
  const decoded = Session.decode(buf);
  assert.deepEqual(decoded, sample);
});

test('schema: 手工构造的裸二进制流可被准确还原', () => {
  // 手工拼装: name="ab"(field1), version=0x0A0B0C0D(field2), scores packed [1,300](field5)
  const w = new WireWriter();
  w.writeTag(1, 2).writeLengthDelimited(new TextEncoder().encode('ab'));
  w.writeTag(2, 5).writeFixed32(0x0a0b0c0d);
  const packed = new WireWriter();
  packed.writeVarint(1).writeVarint(300);
  w.writeTag(5, 2).writeLengthDelimited(packed.finish());
  const decoded = Session.decode(w.finish());
  assert.equal(decoded.name, 'ab');
  assert.equal(decoded.version, 0x0a0b0c0d);
  assert.deepEqual(decoded.scores, [1, 300]);
});

test('schema: packed 与 unpacked 两种编码均可解码同一 repeated 字段', () => {
  const packed = Session.encode({ scores: [7, 8, 9] });
  // 手工 unpacked 编码
  const w = new WireWriter();
  for (const v of [7, 8, 9]) w.writeTag(5, WIRE_VARINT).writeVarint(v);
  const unpacked = w.finish();
  assert.deepEqual(Session.decode(packed), Session.decode(unpacked));
});

test('schema: 未知字段被安全跳过', () => {
  const w = new WireWriter();
  w.writeTag(1, 2).writeLengthDelimited(new TextEncoder().encode('x'));
  w.writeTag(99, WIRE_VARINT).writeVarint(12345);      // 未知 varint
  w.writeTag(100, WIRE_FIXED64).writeFixed64(1n);      // 未知 fixed64
  w.writeTag(101, 2).writeLengthDelimited(new Uint8Array([1, 2, 3])); // 未知 bytes
  const decoded = Session.decode(w.finish());
  assert.deepEqual(decoded, { name: 'x' });
});

test('schema: 截断扫描 — 任意前缀只可能成功或抛 ProtobufDecodeError', () => {
  const buf = Session.encode(sample);
  let okCount = 0;
  let safeFailCount = 0;
  for (let len = 0; len < buf.length; len++) {
    const prefix = buf.subarray(0, len);
    try {
      Session.decode(prefix);
      okCount++; // 恰好落在字段边界的前缀可合法解码
    } catch (e) {
      assert.ok(e instanceof ProtobufDecodeError,
        `prefix ${len}: expected ProtobufDecodeError, got ${String(e)}`);
      assert.match((e as Error).message, /byte offset \d+/);
      safeFailCount++;
    }
  }
  assert.ok(safeFailCount > 0, '应当存在被安全拦截的截断');
  assert.equal(okCount + safeFailCount, buf.length);
});

test('schema: 嵌套消息内部截断同样被防护', () => {
  const full = Session.encode(sample);
  // 将最后一个字节去掉，使最内层 packed deltas 不完整
  assert.throws(() => Session.decode(full.subarray(0, full.length - 1)), ProtobufDecodeError);
  // 只含 track 字段的消息：tag 在字节 0，长度前缀在字节 1，篡改声明长度大于实际
  const nested = Session.encode({ track: { id: 1n } });
  assert.equal(nested[0], 0x22); // field 4, wire type 2
  const corrupted = nested.slice();
  corrupted[1] = 0x7f;
  assert.throws(() => Session.decode(corrupted), ProtobufDecodeError);
});

test('schema: wire type 不匹配被拒绝', () => {
  const w = new WireWriter();
  w.writeTag(1, WIRE_VARINT).writeVarint(1); // name 应为 length-delimited
  assert.throws(() => Session.decode(w.finish()), /Wire type mismatch/);
});

test('schema: 尾部多余字节被拒绝', () => {
  const buf = Session.encode({ name: 'a' });
  const longer = new Uint8Array(buf.length + 1);
  longer.set(buf);
  longer[buf.length] = 0x00; // 非法 tag
  assert.throws(() => Session.decode(longer), ProtobufDecodeError);
});

test('schema: 空消息与缺省字段', () => {
  assert.deepEqual(Session.decode(new Uint8Array(0)), {});
  assert.deepEqual(Session.encode({}), new Uint8Array(0));
});
