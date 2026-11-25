import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JsonStreamDecoder } from '../src/json.ts';
import { BinaryStreamDecoder } from '../src/binary.ts';
import { compileGraph } from '../src/schema.ts';
import { encodeRootMessage } from '../src/encode-binary.ts';
import type { TypeGraph } from '../src/types.ts';
import { allCuts, slices, tenant, utf8Bytes } from './fixtures.ts';

/** 自引用递归类型图：Node.children 是 Node 数组。 */
function buildRecursiveGraph() {
  const graph: TypeGraph = {
    version: 1,
    rootTypeNo: 1,
    types: {
      1: {
        kind: 'object',
        no: 1,
        fields: [
          { no: 1, names: ['id'], typeNo: 4, required: true },
          { no: 2, names: ['children'], typeNo: 3 },
        ],
      },
      2: {
        kind: 'object',
        no: 2,
        fields: [
          { no: 1, names: ['id'], typeNo: 4 },
          { no: 2, names: ['children'], typeNo: 3 },
        ],
      },
      3: { kind: 'array', no: 3, elementTypeNo: 2 },
      4: { kind: 'id64', no: 4 },
    },
    migrations: [],
  };
  return compileGraph(graph);
}

function nestedRecord(level: number, depth: number): Record<number, unknown> {
  if (level >= depth) {
    return { 1: BigInt(level + 2) };
  }
  return { 1: BigInt(level + 2), 2: [nestedRecord(level + 1, depth)] };
}

function nestedJson(level: number, depth: number): Record<string, unknown> {
  if (level >= depth) {
    return { id: String(level + 2) };
  }
  return { id: String(level + 2), children: [nestedJson(level + 1, depth)] };
}

test('深度递归 JSON：检查点绑定深度与每个未闭合对象的已见字段集合', () => {
  const graph = buildRecursiveGraph();
  const depth = 150;
  const json = JSON.stringify({ id: '1', children: [nestedJson(0, depth)] });
  const data = utf8Bytes(json);

  const probe = new JsonStreamDecoder(graph, { tenant });
  const cut = Math.floor(data.length / 2);
  probe.push(data.subarray(0, cut));
  const checkpoint = probe.checkpoint();
  assert.ok(checkpoint.depth > 10, `depth=${checkpoint.depth}`);
  assert.equal(checkpoint.frames.length, checkpoint.depth);
  assert.equal(checkpoint.schemaDigest, graph.digest);
  for (const frame of checkpoint.frames) {
    assert.ok(Array.isArray(frame.seen));
    if (frame.kind === 'object' && frame.node.kind === 'object' && frame.node.fields.length > 0) {
      const seenSorted = [...frame.seen].sort((a, b) => a - b);
      const fieldSorted = frame.node.fields.map((field) => field.no).sort((a, b) => a - b);
      assert.deepEqual(seenSorted, fieldSorted);
    }
  }

  const resumed = new JsonStreamDecoder(graph, { tenant });
  resumed.restore(checkpoint);
  resumed.push(data.subarray(cut));
  const result = resumed.end();

  const whole = new JsonStreamDecoder(graph, { tenant });
  whole.push(data);
  assert.deepEqual(whole.end().minimal, result.minimal);
});

test('深度递归二进制：全切点分段与检查点恢复都得到一致签名', () => {
  const graph = buildRecursiveGraph();
  const depth = 80;
  const bytes = encodeRootMessage(graph, {
    1: 1n,
    2: [nestedRecord(0, depth)],
  });

  const reference = new BinaryStreamDecoder(graph, { tenant });
  reference.push(bytes);
  const expected = reference.end();

  for (const cut of allCuts(bytes.length)) {
    const first = new BinaryStreamDecoder(graph, { tenant });
    const [head, tail] = slices(bytes, cut);
    first.push(head);
    const checkpoint = first.checkpoint();
    assert.equal(checkpoint.depth, checkpoint.frames.length);
    const resumed = new BinaryStreamDecoder(graph, { tenant });
    resumed.restore(checkpoint);
    resumed.push(tail);
    const result = resumed.end();
    assert.equal(result.signatureKey, expected.signatureKey, `cut ${cut}`);
  }
});
