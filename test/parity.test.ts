import assert from 'node:assert/strict';
import { test } from 'node:test';
import { BinaryStreamDecoder } from '../src/binary.ts';
import { JsonStreamDecoder } from '../src/json.ts';
import {
  allCuts,
  buildCurrentGraph,
  buildEquivalentPayload,
  slices,
  tenant,
  utf8Bytes,
} from './fixtures.ts';
import { decodeBinaryReference, decodeJsonReference } from '../src/reference.ts';

const graph = buildCurrentGraph();
const payload = buildEquivalentPayload(graph);
const options = { tenant };

test('参考路径：JSON 与二进制生成相同 minimal 与 signatureKey', () => {
  const jsonResult = decodeJsonReference(graph, payload.json, options);
  const binaryResult = decodeBinaryReference(graph, payload.binary, options);
  assert.deepEqual(jsonResult.minimal, binaryResult.minimal);
  assert.equal(jsonResult.signatureKey, binaryResult.signatureKey);
  assert.equal(jsonResult.schemaDigest, graph.digest);
});

test('JSON 在每个字节切点分段解码都与参考一致', () => {
  const reference = decodeJsonReference(graph, payload.json, options);
  const data = utf8Bytes(payload.json);
  for (const cut of allCuts(data.length)) {
    const decoder = new JsonStreamDecoder(graph, options);
    const [head, tail] = slices(data, cut);
    decoder.push(head);
    decoder.push(tail);
    const result = decoder.end();
    assert.deepEqual(result.minimal, reference.minimal, `cut ${cut}`);
    assert.equal(result.signatureKey, reference.signatureKey, `cut ${cut}`);
  }
});

test('二进制在每个字节切点分段解码都与参考一致', () => {
  const reference = decodeBinaryReference(graph, payload.binary, options);
  for (const cut of allCuts(payload.binary.length)) {
    const decoder = new BinaryStreamDecoder(graph, options);
    const [head, tail] = slices(payload.binary, cut);
    decoder.push(head);
    decoder.push(tail);
    const result = decoder.end();
    assert.deepEqual(result.minimal, reference.minimal, `cut ${cut}`);
    assert.equal(result.signatureKey, reference.signatureKey, `cut ${cut}`);
  }
});

test('JSON 检查点恢复：在每个切点快照并续跑，结果一致', () => {
  const reference = decodeJsonReference(graph, payload.json, options);
  const data = utf8Bytes(payload.json);
  for (const cut of allCuts(data.length)) {
    const first = new JsonStreamDecoder(graph, options);
    const [head, tail] = slices(data, cut);
    first.push(head);
    const checkpoint = first.checkpoint();
    const resumed = new JsonStreamDecoder(graph, options);
    resumed.restore(checkpoint);
    resumed.push(tail);
    const result = resumed.end();
    assert.deepEqual(result.minimal, reference.minimal, `resume cut ${cut}`);
    assert.equal(result.signatureKey, reference.signatureKey, `resume cut ${cut}`);
  }
});

test('二进制检查点恢复：在每个切点快照并续跑，结果一致', () => {
  const reference = decodeBinaryReference(graph, payload.binary, options);
  for (const cut of allCuts(payload.binary.length)) {
    const first = new BinaryStreamDecoder(graph, options);
    const [head, tail] = slices(payload.binary, cut);
    first.push(head);
    const checkpoint = first.checkpoint();
    const resumed = new BinaryStreamDecoder(graph, options);
    resumed.restore(checkpoint);
    resumed.push(tail);
    const result = resumed.end();
    assert.deepEqual(result.minimal, reference.minimal, `resume cut ${cut}`);
    assert.equal(result.signatureKey, reference.signatureKey, `resume cut ${cut}`);
  }
});

test('授权回调只在完整校验通过后触发，失败时绝不触发', async () => {
  const { decodeJson, decodeBinary } = await import('../src/index.ts');
  let jsonCalls = 0;
  const jsonResult = decodeJson(graph, payload.json, options, () => {
    jsonCalls += 1;
  });
  assert.ok(jsonResult.signatureKey);
  assert.equal(jsonCalls, 1);

  let binaryCalls = 0;
  const binaryResult = decodeBinary(graph, payload.binary, options, () => {
    binaryCalls += 1;
  });
  assert.equal(binaryResult.signatureKey, jsonResult.signatureKey);
  assert.equal(binaryCalls, 1);

  let failedCalls = 0;
  assert.throws(() => {
    decodeJson(graph, '{"sid": "01", "profile":', options, () => {
      failedCalls += 1;
    });
  });
  assert.equal(failedCalls, 0);
});
