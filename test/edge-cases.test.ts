import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JsonStreamDecoder } from '../src/json.ts';
import { BinaryStreamDecoder } from '../src/binary.ts';
import { compileGraph } from '../src/schema.ts';
import { DecodeError } from '../src/types.ts';
import { allCuts, buildCurrentGraph, slices, tenant, utf8Bytes } from './fixtures.ts';

const graph = buildCurrentGraph();
const options = { tenant };

test('UTF-8 四字节 emoji 跨任意字节切块（代理对边界）', () => {
  const emoji = '😀';
  const json = JSON.stringify({ sid: '7', profile: { dn: `x${emoji}y` } });
  const data = utf8Bytes(json);
  assert.ok(data.length > 4);
  for (const cut of allCuts(data.length)) {
    const decoder = new JsonStreamDecoder(graph, options);
    const [head, tail] = slices(data, cut);
    decoder.push(head);
    decoder.push(tail);
    const result = decoder.end();
    const profile = result.identity.kind === 'object'
      ? result.identity.fields.find((field) => field.no === 2)?.value
      : undefined;
    const name = profile?.kind === 'object'
      ? profile.fields.find((field) => field.no === 10)?.value
      : undefined;
    assert.equal(name?.kind === 'string' ? name.value : null, `x${emoji}y`, `cut ${cut}`);
  }
});

test('JSON \\u 代理对跨切块拼接；孤立代理拒绝', () => {
  const paired = '{"sid":"7","profile":{"dn":"a\\uD83D\\uDE00b"}}';
  const data = utf8Bytes(paired);
  for (const cut of allCuts(data.length)) {
    const decoder = new JsonStreamDecoder(graph, options);
    const [head, tail] = slices(data, cut);
    decoder.push(head);
    decoder.push(tail);
    const result = decoder.end();
    assert.ok(result.signatureKey, `cut ${cut}`);
  }

  for (const bad of [
    '{"sid":"7","profile":{"dn":"\\uD83D"}}',
    '{"sid":"7","profile":{"dn":"\\uDE00"}}',
    '{"sid":"7","profile":{"dn":"a\\uD83Dx"}}',
  ]) {
    const decoder = new JsonStreamDecoder(graph, options);
    assert.throws(() => {
      decoder.push(utf8Bytes(bad));
      decoder.end();
    }, (error: unknown) =>
      error instanceof DecodeError && error.code === 'LONE_SURROGATE',
    );
  }
});

test('不同深度的相同字段编号互不冲突，且按各自作用域进入身份', () => {
  const json = JSON.stringify({
    sid: '7',
    sameNumberEverywhere: 100,
    profile: { score: 200, displayName: 'nested' },
  });
  const result = new JsonStreamDecoder(graph, options);
  result.push(utf8Bytes(json));
  const decoded = result.end();
  assert.ok(decoded.identity.kind === 'object');
  const rootField5 = decoded.identity.fields.find((field) => field.no === 5);
  assert.equal(rootField5?.value.kind === 'int32' ? rootField5.value.value : null, 100);
  const profile = decoded.identity.fields.find((field) => field.no === 2)?.value;
  assert.ok(profile?.kind === 'object');
  const nestedScore = profile.fields.find((field) => field.no === 11);
  assert.equal(nestedScore?.value.kind === 'int32' ? nestedScore.value.value : null, 200);
});

test('超出安全整数的 id64：字符串保留精确值，数字词法拒绝，杜绝相等误判', () => {
  const big = '9007199254740993';
  const viaString = new JsonStreamDecoder(graph, options);
  viaString.push(utf8Bytes(JSON.stringify({ sid: big })));
  const stringResult = viaString.end();
  assert.ok(stringResult.identity.kind === 'object');
  const idField = stringResult.identity.fields.find((field) => field.no === 1);
  assert.equal(idField?.value.kind === 'id64' ? idField.value.value : null, big);

  const viaNumber = new JsonStreamDecoder(graph, options);
  assert.throws(() => {
    viaNumber.push(utf8Bytes(`{"sid":${big}}`));
    viaNumber.end();
  }, (error: unknown) =>
    error instanceof DecodeError && error.code === 'ID64_LEXEME',
  );

  // 相邻大整数若错误走 Number 会被判等；BigInt/词法路径必须区分。
  const other = new JsonStreamDecoder(graph, options);
  other.push(utf8Bytes(JSON.stringify({ sid: '9007199254740994' })));
  const otherResult = other.end();
  assert.notEqual(otherResult.signatureKey, stringResult.signatureKey);
});

test('两个别名映射同一字段：值相同也拒绝，值不同也拒绝', () => {
  for (const json of [
    JSON.stringify({ sid: '7', subjectId: '7' }),
    JSON.stringify({ sid: '7', subjectId: '8' }),
    JSON.stringify({ enabled: true, active: true }),
    JSON.stringify({ enabled: true, active: false }),
  ]) {
    const decoder = new JsonStreamDecoder(graph, options);
    assert.throws(() => {
      decoder.push(utf8Bytes(json));
      decoder.end();
    }, (error: unknown) =>
      error instanceof DecodeError && error.code === 'DUPLICATE_FIELD',
    );
  }
});

test('schema 编译期静态别名冲突直接拒绝', () => {
  const broken = buildCurrentGraph();
  const raw = structuredClone(broken.raw);
  raw.types[2].fields[0].names.push('score');
  assert.throws(
    () => compileGraph(raw),
    (error: unknown) => error instanceof DecodeError && error.code === 'ALIAS_COLLISION',
  );
});

test('二进制重复字段编号拒绝（不论载荷是否相同）', () => {
  const bytes = buildDuplicateBinary();
  const decoder = new BinaryStreamDecoder(graph, options);
  assert.throws(() => {
    decoder.push(bytes);
    decoder.end();
  }, (error: unknown) =>
    error instanceof DecodeError && error.code === 'DUPLICATE_FIELD',
  );
});

function buildDuplicateBinary(): Uint8Array {
  const parts: number[] = [];
  putU32(parts, 1);
  putU32(parts, 2);
  putU32(parts, 1);
  putU32(parts, 8);
  putI64(parts, 7n);
  putU32(parts, 1);
  putU32(parts, 8);
  putI64(parts, 7n);
  return Uint8Array.from(parts);
}

function putU32(parts: number[], value: number): void {
  const view = new DataView(new ArrayBuffer(4));
  view.setUint32(0, value, false);
  parts.push(...new Uint8Array(view.buffer));
}

function putI64(parts: number[], value: bigint): void {
  const view = new DataView(new ArrayBuffer(8));
  view.setBigUint64(0, value < 0n ? value + (1n << 64n) : value, false);
  parts.push(...new Uint8Array(view.buffer));
}
