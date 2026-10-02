import { test } from 'node:test';
import assert from 'node:assert/strict';

import { LogicalDeleteArchiveSplitter } from '../src/splitter.ts';
import { SinkRouter } from '../src/router.ts';
import { SearchIndexSink } from '../src/sinks/search-index.ts';
import { ColdStorageSink } from '../src/sinks/cold-storage.ts';
import { SourceDb } from '../src/source-db.ts';
import type { ChangeEvent } from '../src/events.ts';

function makeHarness(options: { tables?: string[] } = {}) {
  const searchIndex = new SearchIndexSink();
  const coldStorage = new ColdStorageSink();
  const router = new SinkRouter()
    .registerSink(searchIndex)
    .registerSink(coldStorage)
    .addRule({ match: (c) => c.kind === 'archive-insert', sink: 'cold-storage' })
    .addRule({ match: (c) => c.kind === 'readstore-delete', sink: 'search-index' });
  const db = new SourceDb();
  const splitter = new LogicalDeleteArchiveSplitter({
    tables: options.tables,
    router,
    rowLoader: (table, key) => db.get(table, key),
  });
  const businessSeen: ChangeEvent[] = [];
  db.onChange(async (event) => {
    await splitter.handle(event, (e) => businessSeen.push(e));
  });
  return { searchIndex, coldStorage, splitter, db, businessSeen };
}

test('验收场景：UPDATE user SET is_deleted=1 WHERE id=100 被分流为索引删除 + 冷存储归档', async () => {
  const { searchIndex, coldStorage, db, businessSeen } = makeHarness({ tables: ['user'] });

  db.insert('user', 100, { id: 100, name: 'Alice', email: 'alice@example.com', is_deleted: 0 });
  searchIndex.documents.set(100, { id: 100, name: 'Alice' });

  await db.update('user', 100, { is_deleted: 1 });

  // 搜索索引收到删除 ID=100 的指令，文档被剔除
  assert.equal(searchIndex.executedDeletes.length, 1);
  assert.deepEqual(searchIndex.executedDeletes[0], { kind: 'readstore-delete', table: 'user', key: 100 });
  assert.equal(searchIndex.documents.has(100), false);

  // 冷存储收到包含完整历史数据的归档包
  const archive = coldStorage.find('user', 100);
  assert.ok(archive);
  assert.equal(archive.kind, 'archive-insert');
  assert.equal(archive.meta.reason, 'logical-delete');
  assert.deepEqual(archive.record, { id: 100, name: 'Alice', email: 'alice@example.com', is_deleted: 1 });

  // 业务无感知：原始 Update 未透传到业务下游
  assert.equal(businessSeen.filter((e) => e.kind === 'update').length, 0);
});

test('普通 Update（非逻辑删除）原样透传给业务下游，不产生分流', async () => {
  const { searchIndex, coldStorage, db, businessSeen } = makeHarness();

  db.insert('user', 1, { id: 1, name: 'Bob', is_deleted: 0 });
  businessSeen.length = 0; // 忽略 insert

  await db.update('user', 1, { name: 'Bobby' });

  assert.equal(businessSeen.length, 1);
  assert.equal(businessSeen[0].kind, 'update');
  assert.equal(searchIndex.executedDeletes.length, 0);
  assert.equal(coldStorage.archives.length, 0);
});

test('重复删除（is_deleted 已为 1）不重复归档', async () => {
  const { searchIndex, coldStorage, db } = makeHarness();

  db.insert('user', 7, { id: 7, is_deleted: 1 });
  await db.update('user', 7, { name: 'still deleted' });

  assert.equal(searchIndex.executedDeletes.length, 0);
  assert.equal(coldStorage.archives.length, 0);
});

test('未配置的表不被拦截', async () => {
  const { searchIndex, coldStorage, db, businessSeen } = makeHarness({ tables: ['user'] });

  db.insert('order', 9, { id: 9, is_deleted: 0 });
  businessSeen.length = 0;
  await db.update('order', 9, { is_deleted: 1 });

  assert.equal(businessSeen.length, 1); // 透传
  assert.equal(searchIndex.executedDeletes.length, 0);
  assert.equal(coldStorage.archives.length, 0);
});

test('update 只携带变更列时，rowLoader 回源补全完整历史数据', async () => {
  const searchIndex = new SearchIndexSink();
  const coldStorage = new ColdStorageSink();
  const router = new SinkRouter()
    .registerSink(searchIndex)
    .registerSink(coldStorage)
    .addRule({ match: (c) => c.kind === 'archive-insert', sink: 'cold-storage' })
    .addRule({ match: (c) => c.kind === 'readstore-delete', sink: 'search-index' });
  const db = new SourceDb();
  db.insert('user', 42, { id: 42, name: 'Carol', email: 'carol@example.com', is_deleted: 0 });

  const splitter = new LogicalDeleteArchiveSplitter({
    router,
    rowLoader: (table, key) => db.get(table, key),
  });

  // 模拟只带变更列的增量 update 事件（before 不含全量列）
  const intercepted = await splitter.handle(
    { kind: 'update', table: 'user', key: 42, before: { is_deleted: 0 }, after: { is_deleted: 1 }, ts: Date.now() },
    () => {},
  );

  assert.equal(intercepted, true);
  const archive = coldStorage.find('user', 42);
  assert.ok(archive);
  assert.deepEqual(archive.record, { id: 42, name: 'Carol', email: 'carol@example.com', is_deleted: 1 });
});

test('insert/delete 事件不被拦截，直接透传', async () => {
  const { splitter } = makeHarness();
  const seen: ChangeEvent[] = [];
  const next = (e: ChangeEvent) => seen.push(e);

  assert.equal(await splitter.handle({ kind: 'insert', table: 'user', key: 1, after: { is_deleted: 1 }, ts: 0 }, next), false);
  assert.equal(await splitter.handle({ kind: 'delete', table: 'user', key: 1, before: { is_deleted: 0 }, ts: 0 }, next), false);
  assert.equal(seen.length, 2);
});

test('路由分发：归档包只进冷存储，删除指令只进搜索索引', async () => {
  const { searchIndex, coldStorage, db } = makeHarness();

  db.insert('user', 5, { id: 5, is_deleted: 0 });
  await db.update('user', 5, { is_deleted: 1 });

  assert.equal(coldStorage.archives.length, 1);
  assert.equal(coldStorage.archives[0].kind, 'archive-insert');
  assert.equal(searchIndex.executedDeletes.length, 1);
  assert.equal(searchIndex.executedDeletes[0].kind, 'readstore-delete');
});
