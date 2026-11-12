import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  ByteWriter,
  ThriftDecodeError,
  ThriftMessageType,
  ThriftType,
  decodeMessage,
  decodeStructBytes,
  encodeMessage,
  encodeStruct,
  getRequiredField,
  utf8Encode,
} from '../index.ts';
import type { FieldTable } from '../index.ts';

const TENANT_ID = 1;

const requestFieldTable: FieldTable = new Map([
  [TENANT_ID, { type: ThriftType.I32 }],
  [2, { type: ThriftType.STRING }],
  [3, {
    type: ThriftType.STRUCT,
    fields: new Map([
      [1, { type: ThriftType.LIST, element: { type: ThriftType.I32 } }],
      [2, { type: ThriftType.MAP, key: { type: ThriftType.STRING }, value: { type: ThriftType.I64 } }],
    ]),
  }],
  [4, { type: ThriftType.LIST, element: { type: ThriftType.I16 } }],
]);

/** 手工拼 strict binary envelope，方便在载荷里注入攻击字节。 */
function envelope(payload: Uint8Array, method = 'RpcCall', seq = 7): Uint8Array {
  const name = utf8Encode(method);
  const writer = new ByteWriter();
  writer.writeI32(0x80010000 | ThriftMessageType.CALL);
  writer.writeI32(name.length);
  writer.writeBytes(name);
  writer.writeI32(seq);
  writer.writeBytes(payload);
  return writer.toBytes();
}

function stopOnly(): Uint8Array {
  const writer = new ByteWriter();
  writer.writeByte(ThriftType.STOP);
  return writer.toBytes();
}

function i32Field(id: number, value: number): Uint8Array {
  const writer = new ByteWriter();
  writer.writeByte(ThriftType.I32);
  writer.writeI16(id);
  writer.writeI32(value);
  return writer.toBytes();
}

function concat(...chunks: Uint8Array[]): Uint8Array {
  const total = chunks.reduce((sum, chunk) => sum + chunk.length, 0);
  const out = new Uint8Array(total);
  let cursor = 0;
  for (const chunk of chunks) {
    out.set(chunk, cursor);
    cursor += chunk.length;
  }
  return out;
}

function expectReject(
  bytes: Uint8Array,
  code: string,
  table: FieldTable = requestFieldTable,
): void {
  assert.throws(
    () => decodeMessage(bytes, table),
    (error: unknown) => {
      assert.ok(error instanceof ThriftDecodeError);
      assert.equal(error.code, code);
      assert.ok(Number.isInteger(error.offset));
      return true;
    },
  );
}

test('合法请求完整解码并输出规范字节，字段按 id 排序', () => {
  const writer = new ByteWriter();
  const payloadText = utf8Encode('op');
  writer.writeByte(ThriftType.STRING);
  writer.writeI16(2);
  writer.writeI32(payloadText.length);
  writer.writeBytes(payloadText);
  // 故意把高位 id 字段放在前面，验证重编码会按 id 重排。
  const valid = concat(writer.toBytes(), i32Field(TENANT_ID, 9001), stopOnly());

  const decoded = decodeMessage(envelope(valid), requestFieldTable);
  assert.equal(decoded.methodName, 'RpcCall');
  assert.equal(decoded.sequenceId, 7);
  assert.equal(decoded.messageType, ThriftMessageType.CALL);
  assert.equal(getRequiredField(decoded.root, TENANT_ID).value.type, ThriftType.I32);
  assert.equal(decoded.unknownBytes, 0);

  // 规范字节可再次严格解码，字段顺序按 id 升序。
  const recoded = decodeMessage(decoded.canonical, requestFieldTable);
  assert.deepEqual(recoded.canonical, decoded.canonical);
  const ids = recoded.root.type === ThriftType.STRUCT
    ? recoded.root.fields.map((field) => field.id)
    : [];
  assert.deepEqual(ids, [TENANT_ID, 2]);
});

test('攻击1：tenant 被写成字符串，线上类型不一致整体拒绝', () => {
  const attacker = new ByteWriter();
  attacker.writeByte(ThriftType.STRING);
  attacker.writeI16(TENANT_ID);
  const tenantText = utf8Encode('tenant-9001');
  attacker.writeI32(tenantText.length);
  attacker.writeBytes(tenantText);
  expectReject(envelope(concat(attacker.toBytes(), stopOnly())), 'FIELD_TYPE_MISMATCH');
});

test('攻击2：同一 tenant 字段重复出现直接拒绝，不静默取最后一个', () => {
  const duplicate = concat(i32Field(TENANT_ID, 1), i32Field(TENANT_ID, 2), stopOnly());
  expectReject(envelope(duplicate), 'DUPLICATE_FIELD');
});

test('攻击3a：list 声明负长度被拒绝', () => {
  const malicious = new ByteWriter();
  malicious.writeByte(ThriftType.LIST);
  malicious.writeI16(4);
  malicious.writeByte(ThriftType.I16);
  malicious.writeI32(-1);
  expectReject(envelope(concat(malicious.toBytes(), stopOnly())), 'INVALID_LENGTH');
});

test('攻击3b：list 夸大声明长度，超过剩余字节最小元素数时分配前拒绝', () => {
  const malicious = new ByteWriter();
  malicious.writeByte(ThriftType.LIST);
  malicious.writeI16(4);
  malicious.writeByte(ThriftType.I16);
  malicious.writeI32(1_000_000);
  // 故意只给一个真实元素。
  malicious.writeI16(42);
  expectReject(envelope(concat(malicious.toBytes(), stopOnly())), 'CONTAINER_LENGTH_TOO_LARGE');
});

test('攻击3c：map 夸大条目数，按 key+value 最小字节之和预校验', () => {
  const malicious = new ByteWriter();
  malicious.writeByte(ThriftType.MAP);
  // 字段表中 field 3 是 STRUCT，field 2 的嵌套规格里才有 STRING->I64 的 map；
  // 这里改用独立的 map 字段表，保证先通过字段类型检查再命中长度预校验。
  malicious.writeI16(10);
  malicious.writeByte(ThriftType.STRING);
  malicious.writeByte(ThriftType.I64);
  malicious.writeI32(500_000);
  const mapTable: FieldTable = new Map([
    [10, { type: ThriftType.MAP, key: { type: ThriftType.STRING }, value: { type: ThriftType.I64 } }],
  ]);
  expectReject(envelope(concat(malicious.toBytes(), stopOnly())), 'CONTAINER_LENGTH_TOO_LARGE', mapTable);
});

test('攻击3e：set 夸大声明长度同样按最小元素字节预校验拒绝', () => {
  const setTable: FieldTable = new Map([
    [11, { type: ThriftType.SET, element: { type: ThriftType.BYTE } }],
  ]);
  const malicious = new ByteWriter();
  malicious.writeByte(ThriftType.SET);
  malicious.writeI16(11);
  malicious.writeByte(ThriftType.BYTE);
  malicious.writeI32(2_000_000);
  expectReject(envelope(concat(malicious.toBytes(), stopOnly())), 'CONTAINER_LENGTH_TOO_LARGE', setTable);
});

test('容器元素线上类型与字段表声明不一致时整体拒绝', () => {
  const malicious = new ByteWriter();
  malicious.writeByte(ThriftType.LIST);
  malicious.writeI16(4);
  malicious.writeByte(ThriftType.STRING); // 字段表声明元素是 I16
  malicious.writeI32(0);
  expectReject(envelope(concat(malicious.toBytes(), stopOnly())), 'FIELD_TYPE_MISMATCH');
});

test('bool 线值不是 0/1 时拒绝，杜绝后端宽松归一化造成的解析分叉', () => {
  const boolTable: FieldTable = new Map([[12, { type: ThriftType.BOOL }]]);
  const malicious = new ByteWriter();
  malicious.writeByte(ThriftType.BOOL);
  malicious.writeI16(12);
  malicious.writeByte(2);
  expectReject(envelope(concat(malicious.toBytes(), stopOnly())), 'INVALID_TYPE', boolTable);
});

test('未知字段内部的深层嵌套同样受 maxDepth 约束', () => {
  expectReject(envelope(nestedList(20)), 'NESTED_TOO_DEEP');
});

test('攻击3d：字符串负长度同样拒绝，避免长度绕过', () => {
  const malicious = new ByteWriter();
  malicious.writeByte(ThriftType.STRING);
  malicious.writeI16(2);
  malicious.writeI32(-9);
  expectReject(envelope(concat(malicious.toBytes(), stopOnly())), 'INVALID_LENGTH');
});

function nestedList(depth: number): Uint8Array {
  const head = new ByteWriter();
  head.writeByte(ThriftType.LIST);
  head.writeI16(9);
  for (let level = 0; level < depth; level += 1) {
    head.writeByte(ThriftType.LIST);
    head.writeI32(1);
  }
  head.writeByte(ThriftType.BYTE);
  head.writeI32(1);
  head.writeByte(1);
  head.writeByte(ThriftType.STOP);
  return head.toBytes();
}

test('攻击4：嵌套炸弹超过深度上限被拒绝，浅嵌套正常通过', () => {
  // 根结构体深度 1，list 逐层加深，默认上限 16。
  expectReject(envelope(nestedList(20)), 'NESTED_TOO_DEEP');

  const shallow = decodeMessage(envelope(nestedList(3)), requestFieldTable, {
    maxDepth: 8,
  });
  assert.equal(shallow.unknownBytes > 0, true);
});

test('未知字段原始字节总量超过上限时拒绝，未超限时保留并规范转发', () => {
  const unknown = new ByteWriter();
  unknown.writeByte(ThriftType.STRING);
  unknown.writeI16(99);
  unknown.writeI32(100);
  unknown.writeBytes(new Uint8Array(100).fill(0x61));
  const bytes = envelope(concat(unknown.toBytes(), stopOnly()));

  assert.throws(
    () => decodeMessage(bytes, requestFieldTable, { maxUnknownBytes: 64 }),
    (error: unknown) => error instanceof ThriftDecodeError && error.code === 'UNKNOWN_BYTES_EXCEEDED',
  );

  const decoded = decodeMessage(bytes, requestFieldTable, { maxUnknownBytes: 128 });
  assert.equal(decoded.unknownBytes, 104);
  const again = decodeMessage(decoded.canonical, requestFieldTable, { maxUnknownBytes: 128 });
  assert.deepEqual(again.canonical, decoded.canonical);
});

test('未知字段内部的重复字段仍然被拒绝，深度检查覆盖任意未知嵌套', () => {
  const inner = concat(i32Field(7, 1), i32Field(7, 2), stopOnly());
  const outer = new ByteWriter();
  outer.writeByte(ThriftType.STRUCT);
  outer.writeI16(98);
  outer.writeBytes(inner);
  expectReject(envelope(concat(outer.toBytes(), stopOnly())), 'DUPLICATE_FIELD');
});

test('截断输入、非法类型和非 strict envelope 一律拒绝', () => {
  // envelope 头部完整，tenant 的 i32 值故意少给 2 字节。
  const head = envelope(new Uint8Array(0));
  const truncated = concat(head, i32Field(TENANT_ID, 5).subarray(0, 5));
  expectReject(truncated, 'TRUNCATED_INPUT');

  const badType = new ByteWriter();
  badType.writeByte(99);
  badType.writeI16(1);
  expectReject(envelope(concat(badType.toBytes(), stopOnly())), 'INVALID_TYPE');

  const legacy = new ByteWriter();
  // 旧版协议以名称长度（正整数）开头，而非 0x80010000 版本前缀。
  legacy.writeI32(7);
  legacy.writeBytes(utf8Encode('RpcCall'));
  expectReject(legacy.toBytes(), 'INVALID_MESSAGE');
});

test('结构体载荷接口与消息接口行为一致，STOP 后有尾随字节拒绝', () => {
  const payload = concat(i32Field(TENANT_ID, 5), stopOnly());
  const decoded = decodeStructBytes(payload, requestFieldTable);
  assert.deepEqual(decoded.canonical, payload);

  assert.throws(
    () => decodeStructBytes(concat(payload, new Uint8Array([1])), requestFieldTable),
    (error: unknown) => error instanceof ThriftDecodeError && error.code === 'TRAILING_BYTES',
  );
});

test('编码输出为 strict 格式，方法名与 sequenceId 完整保留', () => {
  const decoded = decodeMessage(envelope(stopOnly(), 'TenantRpc', 42), requestFieldTable);
  const canonical = encodeMessage(
    decoded.methodName,
    decoded.messageType,
    decoded.sequenceId,
    decoded.root,
  );
  assert.deepEqual(canonical, decoded.canonical);
  assert.deepEqual(encodeStruct(decoded.root), stopOnly());
});
