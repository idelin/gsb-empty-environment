import assert from 'node:assert/strict';
import { test } from 'node:test';
import { BinaryStreamDecoder } from '../src/binary.ts';
import { JsonStreamDecoder } from '../src/json.ts';
import { compileGraph } from '../src/schema.ts';
import { DecodeError } from '../src/types.ts';
import {
  allCuts,
  buildCurrentGraph,
  buildEquivalentPayload,
  buildRolloverGraph,
  slices,
  tenant,
  utf8Bytes,
} from './fixtures.ts';

const graph = buildCurrentGraph();
const options = { tenant };

test('JSON 未知字段被跳过且不吞掉相邻字段，字节不进入身份', () => {
  const json = JSON.stringify({
    sid: '7',
    unknownThing: { nested: ['x', { deep: 1 }], keep: true },
    profile: { dn: 'Ada', future: 'ignored' },
    tags: ['t1'],
  });
  const data = utf8Bytes(json);
  for (const cut of allCuts(data.length)) {
    const decoder = new JsonStreamDecoder(graph, options);
    const [head, tail] = slices(data, cut);
    decoder.push(head);
    decoder.push(tail);
    const result = decoder.end();
    assert.ok(result.identity.kind === 'object');
    const fieldNos = result.identity.fields.map((field) => field.no).sort((a, b) => a - b);
    assert.deepEqual(fieldNos, [1, 2, 3]);
    const profile = result.identity.fields.find((field) => field.no === 2)?.value;
    assert.ok(profile?.kind === 'object');
    assert.deepEqual(profile.fields.map((field) => field.no), [10]);
  }
});

test('strictUnknown 下未知字段直接报错', () => {
  const decoder = new JsonStreamDecoder(graph, { ...options, strictUnknown: true });
  assert.throws(() => {
    decoder.push(utf8Bytes(JSON.stringify({ sid: '7', nope: 1 })));
    decoder.end();
  }, (error: unknown) =>
    error instanceof DecodeError && error.code === 'UNKNOWN_FIELD',
  );
});

test('二进制未知字段按 length 跳过，相邻字段完整保留', () => {
  const bytes = buildBinaryWithUnknown();
  for (const cut of allCuts(bytes.length)) {
    const decoder = new BinaryStreamDecoder(graph, options);
    const [head, tail] = slices(bytes, cut);
    decoder.push(head);
    decoder.push(tail);
    const result = decoder.end();
    assert.ok(result.identity.kind === 'object');
    const fieldNos = result.identity.fields.map((field) => field.no).sort((a, b) => a - b);
    assert.deepEqual(fieldNos, [1, 5], `cut ${cut}`);
    const score = result.identity.fields.find((field) => field.no === 5)?.value;
    assert.equal(score?.kind === 'int32' ? score.value : null, 99);
  }
});

function buildBinaryWithUnknown(): Uint8Array {
  const parts: number[] = [];
  putU32(parts, 1);
  putU32(parts, 3);
  putU32(parts, 1);
  putU32(parts, 8);
  putI64(parts, 7n);
  const unknownPayload = utf8Bytes(JSON.stringify({ swallow: [1, 2, 3], adjacent: 'no-leak' }));
  putU32(parts, 7777);
  putU32(parts, unknownPayload.length);
  parts.push(...unknownPayload);
  putU32(parts, 5);
  putU32(parts, 4);
  putI32(parts, 99);
  return Uint8Array.from(parts);
}

test('旧版本 JSON 根字段批量迁移到新嵌套位置', () => {
  const oldJson = JSON.stringify({
    sid: '7',
    ownerId: '555',
    nick: 'Moved',
    tags: ['z'],
  });
  const decoder = new JsonStreamDecoder(graph, { ...options, inputVersion: 1 });
  decoder.push(utf8Bytes(oldJson));
  const result = decoder.end();
  const profile = result.identity.kind === 'object'
    ? result.identity.fields.find((field) => field.no === 2)?.value
    : undefined;
  assert.ok(profile?.kind === 'object');
  const owner = profile.fields.find((field) => field.no === 13)?.value;
  assert.equal(owner?.kind === 'id64' ? owner.value : null, '555');
  const name = profile.fields.find((field) => field.no === 10)?.value;
  assert.equal(name?.kind === 'string' ? name.value : null, 'Moved');
});

test('旧版本二进制根字段按编号迁移，两入口签名一致', () => {
  const oldJson = JSON.stringify({ sid: '7', ownerId: '555', nick: 'Moved' });
  const jsonResult = new JsonStreamDecoder(graph, { ...options, inputVersion: 1 });
  jsonResult.push(utf8Bytes(oldJson));
  const jsonDecoded = jsonResult.end();

  const oldBinary = buildOldBinary();
  const binaryResult = new BinaryStreamDecoder(graph, { ...options, inputVersion: 1 });
  binaryResult.push(oldBinary);
  const binaryDecoded = binaryResult.end();

  assert.deepEqual(binaryDecoded.minimal, jsonDecoded.minimal);
  assert.equal(binaryDecoded.signatureKey, jsonDecoded.signatureKey);
});

function buildOldBinary(): Uint8Array {
  const parts: number[] = [];
  putU32(parts, 1);
  putU32(parts, 3);
  putU32(parts, 1);
  putU32(parts, 8);
  putI64(parts, 7n);
  putU32(parts, 90);
  putU32(parts, 8);
  putI64(parts, 555n);
  const nick = utf8Bytes('Moved');
  putU32(parts, 91);
  putU32(parts, nick.length);
  parts.push(...nick);
  return Uint8Array.from(parts);
}

test('多段别名冲突：同一迁移字段重复出现被拒绝', () => {
  const duplicated = JSON.stringify({
    sid: '7',
    ownerId: '1',
    ownerIdAlias: '2',
  });
  // 旧图没有 ownerIdAlias，默认当未知字段跳过；要测多段冲突，构造双迁移名字。
  const graphTwoMoves = buildGraphWithTwoMigrationNames();
  const decoder = new JsonStreamDecoder(graphTwoMoves, { ...options, inputVersion: 1 });
  assert.throws(() => {
    decoder.push(utf8Bytes(duplicated));
    decoder.end();
  }, (error: unknown) =>
    error instanceof DecodeError &&
    (error.code === 'DUPLICATE_FIELD' || error.code === 'MIGRATION_CONFLICT'),
  );
});

function buildGraphWithTwoMigrationNames() {
  const raw = structuredClone(graph.raw);
  raw.migrations = [
    {
      fromVersion: 1,
      moves: [
        { fromFieldNo: 90, fromName: 'ownerId', toPath: [2, 13] },
        { fromFieldNo: 92, fromName: 'ownerIdAlias', toPath: [2, 13] },
      ],
    },
  ];
  return compileGraph(raw);
}

test('迁移目标与当前消息已有字段冲突时拒绝', () => {
  const conflict = JSON.stringify({
    sid: '7',
    profile: { legacyOwnerId: '9' },
    ownerId: '8',
  });
  const decoder = new JsonStreamDecoder(graph, { ...options, inputVersion: 1 });
  assert.throws(() => {
    decoder.push(utf8Bytes(conflict));
    decoder.end();
  }, (error: unknown) =>
    error instanceof DecodeError && error.code === 'MIGRATION_CONFLICT',
  );
});

test('恢复时类型图换版：digest 不匹配拒绝检查点', () => {
  const payload = buildEquivalentPayload(graph);
  const data = utf8Bytes(payload.json);
  const cut = Math.floor(data.length / 2);
  const first = new JsonStreamDecoder(graph, options);
  first.push(data.subarray(0, cut));
  const checkpoint = first.checkpoint();

  const rolled = buildRolloverGraph();
  assert.notEqual(rolled.digest, graph.digest);
  const resumed = new JsonStreamDecoder(rolled, options);
  assert.throws(() => resumed.restore(checkpoint), (error: unknown) =>
    error instanceof DecodeError && error.code === 'CHECKPOINT_DIGEST',
  );
});

test('二进制恢复时类型图换版同样拒绝', () => {
  const payload = buildEquivalentPayload(graph);
  const cut = Math.floor(payload.binary.length / 2);
  const first = new BinaryStreamDecoder(graph, options);
  first.push(payload.binary.subarray(0, cut));
  const checkpoint = first.checkpoint();
  const rolled = buildRolloverGraph();
  const resumed = new BinaryStreamDecoder(rolled, options);
  assert.throws(() => resumed.restore(checkpoint), (error: unknown) =>
    error instanceof DecodeError && error.code === 'CHECKPOINT_DIGEST',
  );
});

function putU32(parts: number[], value: number): void {
  const view = new DataView(new ArrayBuffer(4));
  view.setUint32(0, value, false);
  parts.push(...new Uint8Array(view.buffer));
}

function putI32(parts: number[], value: number): void {
  const view = new DataView(new ArrayBuffer(4));
  view.setInt32(0, value, false);
  parts.push(...new Uint8Array(view.buffer));
}

function putI64(parts: number[], value: bigint): void {
  const view = new DataView(new ArrayBuffer(8));
  view.setBigUint64(0, value < 0n ? value + (1n << 64n) : value, false);
  parts.push(...new Uint8Array(view.buffer));
}
