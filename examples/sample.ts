/**
 * 运行样例：
 *   node examples/sample.ts
 *
 * 场景：
 *   1. 确定性选路（低成本捷径胜出）并执行迁移
 *   2. 两条同优先路径 => AMBIGUOUS_PATH，拒绝
 *   3. 步骤失败 => 抛出定位错误，不发布新版本，原始输入不变
 *   4. 崩溃后断点恢复（从原始输入重放，不重复追加字段）
 *   5. 恢复时原始输入被改动 => CHECKPOINT_INPUT_MISMATCH
 */
import { buildShopGraph } from './shop-graph.ts';
import { buildGraph, resolvePath } from '../src/graph.ts';
import { Migrator } from '../src/migrator.ts';
import { InMemoryCheckpointStore, FileCheckpointStore } from '../src/store.ts';
import { MigrationError } from '../src/errors.ts';
import { canonicalHash, deepClone } from '../src/json.ts';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import type { Envelope } from '../src/migrate.ts';

const line = (title: string): void => {
  console.log(`\n=== ${title} ===`);
};

const input: Envelope<'v1'> = {
  version: 'v1',
  data: {
    version_marker: 'shop',
    user: { id: 7, name: 'Ada' },
    items: [{ sku: 'a-1', qty: 2 }],
  },
};

// 1. 确定性选路 + 执行
line('1. resolvePath 与 migrate：捷径成本更低，确定性选中');
{
  const graph = buildShopGraph();
  const plan = resolvePath(graph, 'v1', 'v4');
  console.log('versionPath =', plan.versionPath);
  console.log('edgePath    =', plan.edges.map((e) => e.id));
  console.log('cost        =', plan.cost);

  const store = new InMemoryCheckpointStore();
  const migrator = new Migrator(graph, store);
  const result = migrator.run(deepClone(input), 'v4');
  console.log('published   =', result.envelope.version);
  console.log('data        =', JSON.stringify(result.envelope.data));
  console.log('steps       =', result.executed.map((e) => `${e.edgeId}/${e.stepName}`));
  console.log('checkpoint cleared after success =', store.load() === null);
}

// 2. 歧义：两条同优先路径
line('2. 同优先路径歧义 => AMBIGUOUS_PATH（拒绝，不执行任何步骤）');
{
  const ambiguous = buildGraph([
    {
      id: 'direct',
      from: 'a',
      to: 'c',
      priority: 5,
      steps: [
        {
          name: 'noop',
          input: { type: 'object' },
          output: { type: 'object' },
          apply: (d) => d,
        },
      ],
    },
    {
      id: 'via-b',
      from: 'a',
      to: 'b',
      priority: 2,
      steps: [
        {
          name: 'noop',
          input: { type: 'object' },
          output: { type: 'object' },
          apply: (d) => d,
        },
      ],
    },
    {
      id: 'b-to-c',
      from: 'b',
      to: 'c',
      priority: 3,
      steps: [
        {
          name: 'noop',
          input: { type: 'object' },
          output: { type: 'object' },
          apply: (d) => d,
        },
      ],
    },
  ]);
  try {
    resolvePath(ambiguous, 'a', 'c');
    console.log('UNEXPECTED: resolved');
  } catch (err) {
    if (err instanceof MigrationError) {
      console.log('code        =', err.details.code);
      console.log('candidates  =', err.details.candidates);
      console.log('tiedPaths   =', err.details.tiedPaths);
      console.log('message     =', err.details.message);
    } else {
      throw err;
    }
  }
}

// 3. 步骤失败：输出 schema 违规；错误定位到数据路径；不发布
line('3. 迁移失败：定位到具体数据路径，不发布新版本，原始输入不被修改');
{
  const graph = buildGraph([
    {
      from: 'v1',
      to: 'v2',
      priority: 1,
      steps: [
        {
          name: 'add_score',
          input: {
            type: 'object',
            required: ['user'],
            additionalProperties: true,
            properties: { user: { type: 'object' } },
          },
          output: {
            type: 'object',
            required: ['user'],
            additionalProperties: true,
            properties: {
              user: {
                type: 'object',
                required: ['score'],
                additionalProperties: true,
                properties: { score: { type: 'integer', min: 0 } },
              },
            },
          },
          apply: (d: Record<string, unknown> & { user: Record<string, unknown> }) => ({
            ...d,
            user: { ...d.user, score: -1 },
          }),
        },
      ],
    },
  ]);
  const store = new InMemoryCheckpointStore();
  const migrator = new Migrator(graph, store);
  const before = JSON.stringify(input);
  try {
    migrator.run(deepClone(input), 'v2');
    console.log('UNEXPECTED: published');
  } catch (err) {
    if (err instanceof MigrationError) {
      console.log('code        =', err.details.code);
      console.log('path        =', err.details.path);
      console.log('edgeId      =', err.details.edgeId);
      console.log('issues      =', err.details.issues);
    } else {
      throw err;
    }
  }
  console.log('input unchanged =', JSON.stringify(input) === before);
  console.log('no published checkpoint =', store.load() === null);
}

// 4. 崩溃恢复：第二步后“崩溃”，recover 从原始输入重放
line('4. 断点恢复：崩溃后用同一原始输入恢复，不重复追加字段');
{
  const file = join(tmpdir(), `migration-cp-${process.pid}-${Date.now()}.json`);
  const store = new FileCheckpointStore(file);
  const graph = buildShopGraph();
  const migrator = new Migrator(graph, store);

  const persistedIndexes: number[] = [];
  try {
    migrator.run(deepClone(input), 'v4', {
      onPersist: (index) => {
        persistedIndexes.push(index);
        if (index === 1) {
          throw new Error('simulated crash after step index 1');
        }
      },
    });
  } catch (err) {
    console.log('crash       =', err instanceof Error ? err.message : String(err));
  }

  const crashed = store.load();
  console.log('persisted step indexes =', persistedIndexes);
  console.log('checkpoint status      =', crashed?.status);
  console.log('executed so far        =', crashed?.executed.map((e) => e.stepName));

  const recovered = migrator.recover(deepClone(input));
  console.log('recovered version      =', recovered.envelope.version);
  console.log('tags (no duplicates)   =', recovered.envelope.data.tags);
  console.log('tags length            =', (recovered.envelope.data.tags as unknown[]).length);
  console.log('user keys (email once) =', Object.keys(recovered.envelope.data.user as object));
  console.log('email present once     =', Object.values(recovered.envelope.data.user as object).filter((v) => v === 'user7@example.com').length === 1);
}

// 5. 恢复时原始输入被改动
line('5. 恢复校验：原始输入哈希不一致 => CHECKPOINT_INPUT_MISMATCH');
{
  const store = new InMemoryCheckpointStore();
  const graph = buildShopGraph();
  const migrator = new Migrator(graph, store);
  try {
    migrator.run(deepClone(input), 'v4', {
      onPersist: (index) => {
        if (index === 0) throw new Error('simulated crash');
      },
    });
  } catch {
    /* expected crash */
  }

  const tampered = deepClone(input);
  (tampered.data.user as { name: string }).name = 'Ada-Tampered';
  try {
    migrator.recover(tampered);
    console.log('UNEXPECTED: resumed tampered input');
  } catch (err) {
    if (err instanceof MigrationError) {
      console.log('code        =', err.details.code);
      console.log('path        =', err.details.path);
      console.log('expected    =', err.details.expected);
      console.log('actual      =', err.details.actual);
    } else {
      throw err;
    }
  }
}

// 6. 对象键无序 / 数组有序的哈希性质
line('6. 规范化哈希：对象键序无关，数组顺序有关');
{
  const a = canonicalHash({ x: 1, y: [1, 2, 3] } as never);
  const b = canonicalHash({ y: [1, 2, 3], x: 1 } as never);
  const c = canonicalHash({ x: 1, y: [3, 2, 1] } as never);
  console.log('object key reorder equal =', a === b);
  console.log('array reorder differs    =', a !== c);
}
