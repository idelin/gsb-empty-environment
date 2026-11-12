import { test } from 'node:test';
import assert from 'node:assert/strict';
import { preflight, assertPreflight, PreflightError, type ClassDef } from './index.ts';

function varint(value: number): number[] {
  assert.ok(Number.isSafeInteger(value) && value >= 0, 'varint value must be a non-negative integer');
  const out: number[] = [];
  let rest = value;
  do {
    let byte = rest % 128;
    rest = Math.floor(rest / 128);
    if (rest > 0) byte |= 0x80;
    out.push(byte);
  } while (rest > 0);
  return out;
}

function stream(...parts: number[][]): Uint8Array {
  return Uint8Array.from(parts.flat());
}

const registry: ReadonlyMap<number, ClassDef> = new Map([
  [0, { kind: 'scalar', byteLength: 4 }],
  [1, { kind: 'collection' }],
  [2, { kind: 'reference' }],
]);

const TAG_NULL = 0;
const TAG_CLASS_NAME = 1;
const TAG_INT_BOX = 0 + 2;
const TAG_LIST = 1 + 2;
const TAG_REF = 2 + 2;

test('合法流：标量、集合、空值与引用全部通过', () => {
  const data = stream(
    [TAG_INT_BOX, 0xde, 0xad, 0xbe, 0xef],
    [TAG_LIST, 2], [TAG_INT_BOX, 1, 2, 3, 4], [TAG_NULL],
    [TAG_REF, 0],
  );
  const result = preflight(data, registry);
  assert.ok(result.ok);
  assert.deepEqual(result.stats, { objects: 3, references: 1, nulls: 1, bytesRead: data.length });
});

test('合法流：注册号边界 0 与 5 字节变长整数均可接受', () => {
  const fiveByteTag = [TAG_INT_BOX | 0x80, 0x80, 0x80, 0x80, 0x00];
  assert.equal(fiveByteTag.length, 5);
  const data = stream(fiveByteTag, [9, 9, 9, 9]);
  const result = preflight(data, registry);
  assert.ok(result.ok);
  assert.equal(result.stats.objects, 1);
});

test('注册号错位：标记减 2 后不在注册表中一律拒绝', () => {
  const result = preflight(stream([TAG_REF + 1]), registry);
  assert.ok(!result.ok);
  assert.equal(result.error.code, 'UNREGISTERED_CLASS');
  assert.match(result.error.message, /registration id 3/);
});

test('注册号错位：集合元素中夹带未注册号同样拒绝', () => {
  const data = stream([TAG_LIST, 2], [TAG_INT_BOX, 1, 2, 3, 4], [TAG_REF + 1]);
  const result = preflight(data, registry);
  assert.ok(!result.ok);
  assert.equal(result.error.code, 'UNREGISTERED_CLASS');
});

test('类名写法夹带：顶层或集合内出现标记 1 一律拒绝', () => {
  const topLevel = preflight(stream([TAG_CLASS_NAME, 4, 0x61, 0x62, 0x63, 0x64]), registry);
  assert.ok(!topLevel.ok);
  assert.equal(topLevel.error.code, 'CLASS_NAME_NOT_ALLOWED');

  const nested = preflight(stream([TAG_LIST, 1], [TAG_CLASS_NAME, 1, 0x58]), registry);
  assert.ok(!nested.ok);
  assert.equal(nested.error.code, 'CLASS_NAME_NOT_ALLOWED');
});

test('截断的变长整数：缓冲区在变长整数中间结束', () => {
  const bare = preflight(stream([0x80]), registry);
  assert.ok(!bare.ok);
  assert.equal(bare.error.code, 'TRUNCATED_VARINT');

  const afterValidNode = preflight(stream([TAG_INT_BOX, 1, 2, 3, 4], [0x80, 0x80]), registry);
  assert.ok(!afterValidNode.ok);
  assert.equal(afterValidNode.error.code, 'TRUNCATED_VARINT');
  assert.equal(afterValidNode.error.offset, 5);
});

test('变长整数超过 5 字节拒绝', () => {
  const result = preflight(stream([0x80, 0x80, 0x80, 0x80, 0x80, 0x01]), registry);
  assert.ok(!result.ok);
  assert.equal(result.error.code, 'VARINT_TOO_LONG');
});

test('集合长度超预算拒绝', () => {
  const overDefault = preflight(stream([TAG_LIST, ...varint(1025)]), registry);
  assert.ok(!overDefault.ok);
  assert.equal(overDefault.error.code, 'COLLECTION_TOO_LARGE');

  const overCustom = preflight(stream([TAG_LIST, 3], [TAG_NULL], [TAG_NULL], [TAG_NULL]), registry, { maxCollectionLength: 2 });
  assert.ok(!overCustom.ok);
  assert.equal(overCustom.error.code, 'COLLECTION_TOO_LARGE');

  const withinCustom = preflight(stream([TAG_LIST, 2], [TAG_NULL], [TAG_NULL]), registry, { maxCollectionLength: 2 });
  assert.ok(withinCustom.ok);
});

test('引用标记指向不存在的对象拒绝', () => {
  const firstObject = preflight(stream([TAG_REF, 0]), registry);
  assert.ok(!firstObject.ok);
  assert.equal(firstObject.error.code, 'DANGLING_REFERENCE');

  const aheadOfSeen = preflight(stream([TAG_INT_BOX, 1, 2, 3, 4], [TAG_REF, 1]), registry);
  assert.ok(!aheadOfSeen.ok);
  assert.equal(aheadOfSeen.error.code, 'DANGLING_REFERENCE');

  const valid = preflight(stream([TAG_INT_BOX, 1, 2, 3, 4], [TAG_REF, 0]), registry);
  assert.ok(valid.ok);
});

test('标量载荷截断拒绝', () => {
  const result = preflight(stream([TAG_INT_BOX, 1, 2]), registry);
  assert.ok(!result.ok);
  assert.equal(result.error.code, 'TRUNCATED_PAYLOAD');
});

test('嵌套深度超预算拒绝', () => {
  const data = stream([TAG_LIST, 1], [TAG_LIST, 1], [TAG_NULL]);
  const result = preflight(data, registry, { maxDepth: 1 });
  assert.ok(!result.ok);
  assert.equal(result.error.code, 'DEPTH_EXCEEDED');
});

test('assertPreflight 在拒绝时抛出 PreflightError', () => {
  assert.throws(() => assertPreflight(stream([TAG_CLASS_NAME]), registry), (err: unknown) => {
    assert.ok(err instanceof PreflightError);
    assert.equal(err.code, 'CLASS_NAME_NOT_ALLOWED');
    return true;
  });
});
