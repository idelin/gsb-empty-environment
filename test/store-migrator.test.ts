import { test } from 'node:test';
import assert from 'node:assert/strict';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { writeFileSync, existsSync } from 'node:fs';
import { Migrator } from '../src/migrator.ts';
import { FileCheckpointStore, InMemoryCheckpointStore } from '../src/store.ts';
import { buildShopGraph } from '../examples/shop-graph.ts';
import { MigrationError } from '../src/errors.ts';
import { deepClone } from '../src/json.ts';
import type { Envelope } from '../src/migrate.ts';

const input: Envelope<'v1'> = {
  version: 'v1',
  data: {
    version_marker: 'shop',
    user: { id: 7, name: 'Ada' },
    items: [],
  },
};

test('FileCheckpointStore：原子写入、加载、清除', () => {
  const path = join(tmpdir(), `cp-${process.pid}-${Date.now()}-${Math.random().toString(36).slice(2)}.json`);
  const store = new FileCheckpointStore(path);
  assert.equal(store.load(), null);

  const graph = buildShopGraph();
  const migrator = new Migrator(graph, store);
  const result = migrator.run(deepClone(input), 'v4');
  assert.equal(result.envelope.version, 'v4');
  assert.equal(existsSync(path), false);
});

test('崩溃 -> recover：从原始输入重放，不重复追加字段', () => {
  const path = join(tmpdir(), `cp-${process.pid}-${Date.now()}-${Math.random().toString(36).slice(2)}.json`);
  const store = new FileCheckpointStore(path);
  const migrator = new Migrator(buildShopGraph(), store);

  try {
    migrator.run(deepClone(input), 'v4', {
      onPersist: (index) => {
        if (index === 1) throw new Error('crash');
      },
    });
    assert.fail();
  } catch (err) {
    assert.match((err as Error).message, /crash/);
  }

  const cp = store.load();
  assert.ok(cp);
  assert.equal(cp!.status, 'in-progress');
  assert.equal(cp!.executed.length, 2);

  const result = migrator.recover(deepClone(input));
  assert.equal(result.envelope.version, 'v4');
  assert.deepEqual(result.envelope.data.tags, ['customer']);
  assert.equal(existsSync(path), false);
});

test('recover 要求与最初完全相同的原始输入', () => {
  const store = new InMemoryCheckpointStore();
  const migrator = new Migrator(buildShopGraph(), store);
  try {
    migrator.run(deepClone(input), 'v4', {
      onPersist: (index) => {
        if (index === 0) throw new Error('crash');
      },
    });
  } catch {
    /* ignore */
  }
  const tampered = deepClone(input);
  (tampered.data.user as { name: string }).name = 'Bob';
  assert.throws(
    () => migrator.recover(tampered),
    (e: unknown) => e instanceof MigrationError && e.details.code === 'CHECKPOINT_INPUT_MISMATCH',
  );
});

test('无断点 recover => CHECKPOINT_NOT_FOUND', () => {
  const migrator = new Migrator(buildShopGraph(), new InMemoryCheckpointStore());
  assert.throws(
    () => migrator.recover(deepClone(input)),
    (e: unknown) => e instanceof MigrationError && e.details.code === 'CHECKPOINT_NOT_FOUND',
  );
});

test('已有 in-progress 断点时 run 被拒绝（防止覆盖恢复点）', () => {
  const store = new InMemoryCheckpointStore();
  const migrator = new Migrator(buildShopGraph(), store);
  try {
    migrator.run(deepClone(input), 'v4', {
      onPersist: (index) => {
        if (index === 0) throw new Error('crash');
      },
    });
  } catch {
    /* ignore */
  }
  assert.throws(
    () => migrator.run(deepClone(input), 'v4'),
    (e: unknown) => e instanceof MigrationError && e.details.code === 'CHECKPOINT_EXECUTION_MISMATCH',
  );
});

test('最后一步回调抛错：completed 断点已落盘，recover 直接取终态不重跑', () => {
  const store = new InMemoryCheckpointStore();
  const migrator = new Migrator(buildShopGraph(), store);
  try {
    migrator.run(deepClone(input), 'v4', {
      onPersist: (index) => {
        if (index === 3) throw new Error('crash right after final persist');
      },
    });
    assert.fail();
  } catch (err) {
    assert.match((err as Error).message, /crash right after final persist/);
  }
  const cp = store.load();
  assert.equal(cp!.status, 'completed');

  // 不提供继续回调 => 恢复过程中不执行任何 apply，直接重算出终态
  const result = migrator.recover(deepClone(input));
  assert.equal(result.envelope.version, 'v4');
  assert.deepEqual(result.envelope.data.tags, ['customer']);
  assert.equal(store.load(), null);
});

test('损坏的断点文件 => CHECKPOINT_CORRUPT 并带位置', () => {
  const path = join(tmpdir(), `cp-${process.pid}-${Date.now()}-${Math.random().toString(36).slice(2)}.json`);
  writeFileSync(path, '{not-json', 'utf8');
  const store = new FileCheckpointStore(path);
  try {
    store.load();
    assert.fail();
  } catch (err) {
    assert.ok(err instanceof MigrationError);
    assert.equal(err.details.code, 'CHECKPOINT_CORRUPT');
    assert.equal(err.details.location, path);
  }
});

test('迁移失败不发布：失败后无成功断点，输入不变', () => {
  const path = join(tmpdir(), `cp-${process.pid}-${Date.now()}-${Math.random().toString(36).slice(2)}.json`);
  const store = new FileCheckpointStore(path);
  const migrator = new Migrator(buildShopGraph(), store);
  const snapshot = JSON.stringify(input);
  assert.throws(() => migrator.run({ version: 'v1', data: { wrong: true } }, 'v4'), MigrationError);
  assert.equal(JSON.stringify(input), snapshot);
});
