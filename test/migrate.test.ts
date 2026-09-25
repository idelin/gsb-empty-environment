import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildGraph } from '../src/graph.ts';
import type { MigrationGraph } from '../src/graph.ts';
import { addField, appendUnique } from '../src/ops.ts';
import {
  migrate,
  resume,
  graphFingerprint,
} from '../src/migrate.ts';
import type { Envelope, Checkpoint } from '../src/migrate.ts';
import { MigrationError } from '../src/errors.ts';
import { deepClone, canonicalHash } from '../src/json.ts';

/**
 * v1 --(cost10)--> v2 --(cost10)--> v3
 * v1 ----------------(cost25)----> v3
 * 链路成本 20 < 25 => 确定选链路。
 */
function buildTestGraph(): MigrationGraph {
  return buildGraph([
    {
      id: 'e1',
      from: 'v1',
      to: 'v2',
      priority: 10,
      steps: [
        {
          name: 'add_email',
          input: {
            type: 'object',
            required: ['user'],
            additionalProperties: true,
            properties: {
              user: {
                type: 'object',
                required: ['id'],
                additionalProperties: true,
                properties: { id: { type: 'integer' } },
              },
            },
          },
          output: {
            type: 'object',
            required: ['user'],
            additionalProperties: true,
            properties: {
              user: {
                type: 'object',
                required: ['id', 'email'],
                additionalProperties: true,
                properties: { id: { type: 'integer' }, email: { type: 'string' } },
              },
            },
          },
          apply: (d: { user: { id: number } } & Record<string, unknown>) => ({
            ...d,
            user: addField(d.user, 'email', `u${d.user.id}@x.io`),
          }),
        },
      ],
    },
    {
      id: 'e2',
      from: 'v2',
      to: 'v3',
      priority: 10,
      steps: [
        {
          name: 'add_tags',
          input: {
            type: 'object',
            additionalProperties: true,
            properties: {
              user: { type: 'object', additionalProperties: true },
            },
          },
          output: {
            type: 'object',
            required: ['tags'],
            additionalProperties: true,
            properties: { tags: { type: 'array', items: { type: 'string' } } },
          },
          apply: (d) => addField(d, 'tags', []),
        },
        {
          name: 'tag_vip',
          input: {
            type: 'object',
            required: ['tags'],
            additionalProperties: true,
            properties: { tags: { type: 'array', maxItems: 0, items: { type: 'string' } } },
          },
          output: {
            type: 'object',
            required: ['tags'],
            additionalProperties: true,
            properties: { tags: { type: 'array', items: { type: 'string' } } },
          },
          apply: (d) => appendUnique(d, 'tags', 'vip'),
        },
      ],
    },
    {
      id: 'direct',
      from: 'v1',
      to: 'v3',
      priority: 25,
      steps: [
        {
          name: 'direct_upgrade',
          input: { type: 'object', additionalProperties: true },
          output: { type: 'object', required: ['tags'], additionalProperties: true },
          apply: (d) => ({ tags: ['vip'], ...d }),
        },
      ],
    },
  ]);
}

const input: Envelope<'v1'> = {
  version: 'v1',
  data: { user: { id: 1, name: 'Ada' } },
};

test('迁移：确定性选链路、逐步执行、发布 v3、journal 完整', () => {
  const graph = buildTestGraph();
  const result = migrate(graph, deepClone(input), 'v3');
  assert.deepEqual(result.versionPath, ['v1', 'v2', 'v3']);
  assert.deepEqual(result.executed.map((j) => `${j.edgeId}:${j.stepName}`), [
    'e1:add_email',
    'e2:add_tags',
    'e2:tag_vip',
  ]);
  assert.equal(result.envelope.version, 'v3');
  assert.deepEqual(result.envelope.data.tags, ['vip']);
  assert.equal(result.envelope.data.user.email, 'u1@x.io');
  assert.equal(result.checkpoint.status, 'completed');
});

test('原始输入不被修改；输入 schema 违规在执行前被拒', () => {
  const graph = buildTestGraph();
  const snapshot = JSON.stringify(input);
  migrate(graph, deepClone(input), 'v3');
  assert.equal(JSON.stringify(input), snapshot);

  const bad: Envelope<'v1'> = { version: 'v1', data: { user: { name: 'no-id' } } as never };
  try {
    migrate(graph, bad, 'v3');
    assert.fail();
  } catch (err) {
    assert.ok(err instanceof MigrationError);
    assert.equal(err.details.code, 'INPUT_SCHEMA_VIOLATION');
    assert.equal(err.details.path, '/user');
    assert.equal(err.details.edgeId, 'e1');
  }
});

test('输出 schema 违规：错误定位到具体数据路径与步骤', () => {
  const graph = buildGraph([
    {
      from: 'v1',
      to: 'v2',
      priority: 1,
      steps: [
        {
          name: 'bad_output',
          input: { type: 'object', additionalProperties: true },
          output: {
            type: 'object',
            required: ['n'],
            additionalProperties: false,
            properties: { n: { type: 'integer', min: 10 } },
          },
          apply: () => ({ n: 3 }),
        },
      ],
    },
  ]);
  try {
    migrate(graph, { version: 'v1', data: {} }, 'v2');
    assert.fail();
  } catch (err) {
    assert.ok(err instanceof MigrationError);
    assert.equal(err.details.code, 'OUTPUT_SCHEMA_VIOLATION');
    assert.equal(err.details.path, '/n');
    assert.equal(err.details.issues![0]!.reason, 'below minimum');
    assert.equal(err.details.causeLocation, '/edges[v1->v2]/steps[bad_output]');
  }
});

test('步骤抛错包装为 STEP_FAILED 且带来源位置；不产生结果', () => {
  const graph = buildGraph([
    {
      from: 'v1',
      to: 'v2',
      priority: 1,
      steps: [
        {
          name: 'boom',
          input: { type: 'object', additionalProperties: true },
          output: { type: 'object', additionalProperties: true },
          apply: () => {
            throw new Error('disk exploded at /payload/items/2');
          },
        },
      ],
    },
  ]);
  try {
    migrate(graph, { version: 'v1', data: {} }, 'v2');
    assert.fail();
  } catch (err) {
    assert.ok(err instanceof MigrationError);
    assert.equal(err.details.code, 'STEP_FAILED');
    assert.match(err.details.cause!, /disk exploded/);
    assert.equal(err.details.causeLocation, '/edges[v1->v2]/steps[boom]');
  }
});

test('歧义路径：迁移直接拒绝，不执行任何步骤', () => {
  const graph = buildGraph([
    { id: 'd', from: 'a', to: 'c', priority: 5, steps: [{ name: 's', input: { type: 'object' }, output: { type: 'object' }, apply: (x) => x }] },
    { id: 'a-b', from: 'a', to: 'b', priority: 2, steps: [{ name: 's', input: { type: 'object' }, output: { type: 'object' }, apply: (x) => x }] },
    { id: 'b-c', from: 'b', to: 'c', priority: 3, steps: [{ name: 's', input: { type: 'object' }, output: { type: 'object' }, apply: (x) => x }] },
  ]);
  let applied = 0;
  const counting = buildGraph([
    { id: 'd', from: 'a', to: 'c', priority: 5, steps: [{ name: 's', input: { type: 'object' }, output: { type: 'object' }, apply: (x) => { applied++; return x; } }] },
    { id: 'a-b', from: 'a', to: 'b', priority: 2, steps: [{ name: 's', input: { type: 'object' }, output: { type: 'object' }, apply: (x) => { applied++; return x; } }] },
    { id: 'b-c', from: 'b', to: 'c', priority: 3, steps: [{ name: 's', input: { type: 'object' }, output: { type: 'object' }, apply: (x) => { applied++; return x; } }] },
  ]);
  assert.throws(() => migrate(graph, { version: 'a', data: {} }, 'c'), (e: unknown) => e instanceof MigrationError && e.details.code === 'AMBIGUOUS_PATH');
  assert.throws(() => migrate(counting, { version: 'a', data: {} }, 'c'));
  assert.equal(applied, 0);
});

test('断点恢复：从原始输入重放，字段不重复追加，结果与一次跑完一致', () => {
  const graph = buildTestGraph();
  let captured: Checkpoint | null = null;
  try {
    migrate(graph, deepClone(input), 'v3', {
      onCheckpoint: (cp, index) => {
        if (index === 1) {
          captured = deepClone(cp);
          throw new Error('crash');
        }
      },
    });
    assert.fail();
  } catch (err) {
    assert.match((err as Error).message, /crash/);
  }
  assert.ok(captured);
  assert.equal(captured!.status, 'in-progress');
  assert.equal(captured!.executed.length, 2);

  const result = resume(graph, captured!, deepClone(input));
  assert.equal(result.envelope.version, 'v3');
  assert.deepEqual(result.envelope.data.tags, ['vip']);
  assert.equal(result.checkpoint.status, 'completed');

  // 与一次性迁移的最终结果逐字节一致
  const oneShot = migrate(graph, deepClone(input), 'v3');
  assert.equal(canonicalHash(result.envelope), canonicalHash(oneShot.envelope));
});

test('重复恢复（completed 断点再恢复）是幂等的，不二次追加', () => {
  const graph = buildTestGraph();
  const oneShot = migrate(graph, deepClone(input), 'v3');
  const again = resume(graph, oneShot.checkpoint, deepClone(input));
  assert.deepEqual(again.envelope.data.tags, ['vip']);
  assert.equal(canonicalHash(again.envelope), canonicalHash(oneShot.envelope));
});

test('恢复校验：原始输入哈希不一致 => CHECKPOINT_INPUT_MISMATCH', () => {
  const graph = buildTestGraph();
  const cp = migrate(graph, deepClone(input), 'v3').checkpoint;
  const tampered = deepClone(input);
  (tampered.data.user as { name: string }).name = 'X';
  try {
    resume(graph, cp, tampered);
    assert.fail();
  } catch (err) {
    assert.ok(err instanceof MigrationError);
    assert.equal(err.details.code, 'CHECKPOINT_INPUT_MISMATCH');
  }
});

test('恢复校验：图定义变化 => CHECKPOINT_GRAPH_MISMATCH', () => {
  const graph = buildTestGraph();
  const cp = migrate(graph, deepClone(input), 'v3').checkpoint;
  // 额外增加一条孤立边，图指纹变化但 v1->v3 的解析不变
  const changed = buildGraph([
    ...buildTestGraph().edges.map((e) => ({ ...e, steps: [...e.steps] })),
    {
      id: 'extra-unreachable',
      from: 'zz',
      to: 'yy',
      priority: 1,
      steps: [{ name: 'n', input: { type: 'object' }, output: { type: 'object' }, apply: (x) => x }],
    },
  ]);
  assert.notEqual(graphFingerprint(changed), graphFingerprint(graph));
  assert.throws(
    () => resume(changed, cp, deepClone(input)),
    (e: unknown) => e instanceof MigrationError && e.details.code === 'CHECKPOINT_GRAPH_MISMATCH',
  );
});

test('恢复校验：已执行步骤被篡改（outputHash）=> CHECKPOINT_EXECUTION_MISMATCH', () => {
  const graph = buildTestGraph();
  const cp = migrate(graph, deepClone(input), 'v3').checkpoint;
  cp.executed[0]!.outputHash = 'deadbeef';
  try {
    resume(graph, cp, deepClone(input));
    assert.fail();
  } catch (err) {
    assert.ok(err instanceof MigrationError);
    assert.equal(err.details.code, 'CHECKPOINT_EXECUTION_MISMATCH');
    assert.equal(err.details.edgeId, 'e1');
  }
});

test('恢复校验：版本路径变化 => CHECKPOINT_TARGET_MISMATCH', () => {
  const graph = buildTestGraph();
  const cp = migrate(graph, deepClone(input), 'v3').checkpoint;
  // 让 direct 边成本更低 => 路径变成 [v1,v3]；伪造 graphHash 以越过第一道校验
  const cheaperDirect = buildGraph(
    buildTestGraph().edges.map((e) => (e.id === 'direct' ? { ...e, priority: 1 } : e)),
  );
  cp.graphHash = graphFingerprint(cheaperDirect);
  try {
    resume(cheaperDirect, cp, deepClone(input));
    assert.fail();
  } catch (err) {
    assert.ok(err instanceof MigrationError);
    assert.equal(err.details.code, 'CHECKPOINT_TARGET_MISMATCH');
    assert.deepEqual(err.details.versionPath, ['v1', 'v3']);
  }
});

test('无路径/错误目标版本失败', () => {
  const graph = buildTestGraph();
  assert.throws(
    () => migrate(graph, deepClone(input), 'v9'),
    (e: unknown) => e instanceof MigrationError && e.details.code === 'NO_PATH',
  );
});
