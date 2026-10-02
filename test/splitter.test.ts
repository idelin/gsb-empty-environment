import assert from 'node:assert/strict';
import { once } from 'node:events';
import { test } from 'node:test';
import { LogicalDeleteArchiveMiddleware } from '../src/logicalDeleteMiddleware.ts';
import { InMemorySink } from '../src/sinks.ts';
import { SimulatedSourceDB } from '../src/source.ts';
import { Splitter } from '../src/splitter.ts';
import type { ChangeEvent, DispatchReceipt } from '../src/types.ts';

function makeEvent(overrides: Partial<ChangeEvent> = {}): ChangeEvent {
  return {
    kind: 'update',
    table: 'user',
    keyColumn: 'id',
    primaryKey: 100,
    before: { id: 100, name: 'Alice', is_deleted: 0 },
    after: { id: 100, name: 'Alice', is_deleted: 1 },
    source: 'test',
    occurredAt: new Date().toISOString(),
    ...overrides,
  };
}

function makeSplitter() {
  const coldStorage = new InMemorySink('cold-storage');
  const searchIndex = new InMemorySink('search-index');
  const readonlyQuery = new InMemorySink('readonly-query');
  const splitter = new Splitter()
    .registerSink(coldStorage)
    .registerSink(searchIndex)
    .registerSink(readonlyQuery)
    .use(new LogicalDeleteArchiveMiddleware());
  return { splitter, coldStorage, searchIndex, readonlyQuery };
}

test('is_deleted 0->1 的 Update 被转换为归档 Insert + 只读侧 Delete', async () => {
  const { splitter, coldStorage, searchIndex, readonlyQuery } = makeSplitter();
  const receipts = await splitter.process(makeEvent());

  assert.equal(receipts.length, 3);
  assert.ok(receipts.every((r) => r.status === 'ok'));

  // 物理归档库收到 Insert，归档包含完整历史数据
  assert.equal(coldStorage.calls.length, 1);
  const archive = coldStorage.calls[0];
  assert.equal(archive.op, 'insert');
  assert.equal(archive.table, 'archive_user');
  assert.deepEqual(
    { id: archive.record!.id, name: archive.record!.name, is_deleted: archive.record!.is_deleted },
    { id: 100, name: 'Alice', is_deleted: 1 },
  );
  const meta = archive.record!._archive_meta as Record<string, unknown>;
  assert.equal(meta.deleteKind, 'logical');
  assert.equal(meta.sourceTable, 'user');
  assert.equal(meta.primaryKey, 100);

  // 搜索索引与只读查询库收到 Delete id=100
  assert.deepEqual(searchIndex.calls, [{ op: 'delete', table: 'user', id: 100 }]);
  assert.deepEqual(readonlyQuery.calls, [{ op: 'delete', table: 'user', id: 100 }]);
});

test('未置 is_deleted 的普通 Update 不触发分流', async () => {
  const { splitter, coldStorage, searchIndex } = makeSplitter();
  const receipts = await splitter.process(
    makeEvent({
      before: { id: 100, name: 'Alice', is_deleted: 0 },
      after: { id: 100, name: 'Alicia', is_deleted: 0 },
    }),
  );
  assert.equal(receipts.length, 0);
  assert.equal(coldStorage.calls.length, 0);
  assert.equal(searchIndex.calls.length, 0);
});

test('is_deleted 已是 1 的重复更新不重复归档（仅拦截 0->1 跃迁）', async () => {
  const { splitter, coldStorage } = makeSplitter();
  const receipts = await splitter.process(
    makeEvent({
      before: { id: 100, name: 'Alice', is_deleted: 1 },
      after: { id: 100, name: 'Alice', is_deleted: 1 },
    }),
  );
  assert.equal(receipts.length, 0);
  assert.equal(coldStorage.calls.length, 0);
});

test('Insert / Delete 事件不被中间件拦截', async () => {
  const { splitter, coldStorage } = makeSplitter();
  assert.equal((await splitter.process(makeEvent({ kind: 'insert' }))).length, 0);
  assert.equal((await splitter.process(makeEvent({ kind: 'delete' }))).length, 0);
  assert.equal(coldStorage.calls.length, 0);
});

test('单个 Sink 故障被隔离，其余 Sink 正常分发并给出失败回执', async () => {
  const { splitter, coldStorage, searchIndex, readonlyQuery } = makeSplitter();
  searchIndex.failNext = true;
  const receipts = await splitter.process(makeEvent());

  assert.equal(receipts.filter((r) => r.status === 'ok').length, 2);
  const failed = receipts.find((r) => r.status === 'failed');
  assert.ok(failed);
  assert.match(failed.error!, /injected failure/);
  assert.equal(coldStorage.calls.length, 1);
  assert.equal(readonlyQuery.calls.length, 1);
});

test('路由到未注册 Sink 时返回失败回执而非抛错', async () => {
  const splitter = new Splitter().use(
    new LogicalDeleteArchiveMiddleware({ archiveSink: 'missing-sink', readSinks: [] }),
  );
  const receipts = await splitter.process(makeEvent());
  assert.equal(receipts.length, 1);
  assert.equal(receipts[0].status, 'failed');
  assert.match(receipts[0].error!, /unknown sink/);
});

test('流式接口：Transform 管道中事件进、回执出', async () => {
  const { splitter, coldStorage, searchIndex } = makeSplitter();
  const stream = splitter.stream();
  const outputs: DispatchReceipt[] = [];
  stream.on('data', (receipt) => outputs.push(receipt));

  stream.write(makeEvent());
  stream.write(makeEvent({ primaryKey: 101, after: { id: 101, is_deleted: 1 } }));
  stream.end();
  await once(stream, 'end');

  assert.equal(outputs.length, 6);
  assert.equal(coldStorage.calls.length, 2);
  assert.deepEqual(
    searchIndex.calls.map((c) => c.id),
    [100, 101],
  );
});

test('验收边界：源库执行 UPDATE user SET is_deleted=1 WHERE id=100，业务无感知完成分流', async () => {
  const source = new SimulatedSourceDB('mysql-main');
  source.seed('user', 'id', [
    { id: 100, name: 'Alice', email: 'alice@example.com', is_deleted: 0 },
  ]);
  const { splitter, coldStorage, searchIndex } = makeSplitter();
  const done = once(source, 'cdc');
  source.on('cdc', (event: ChangeEvent) => splitter.process(event));

  // 业务视角：一次普通 UPDATE，同步返回影响行数
  const affected = source.executeUpdate('UPDATE user SET is_deleted = 1 WHERE id = 100');
  assert.equal(affected, 1);
  assert.equal(source.get('user', 100)!.is_deleted, 1);

  await done;
  await new Promise((resolve) => setImmediate(resolve));

  // 搜索索引收到删除 ID=100 的指令
  assert.deepEqual(searchIndex.calls, [{ op: 'delete', table: 'user', id: 100 }]);
  // 冷存储收到包含完整历史数据的归档包
  assert.equal(coldStorage.calls.length, 1);
  const record = coldStorage.calls[0].record!;
  assert.equal(record.id, 100);
  assert.equal(record.name, 'Alice');
  assert.equal(record.email, 'alice@example.com');
  assert.equal(record.is_deleted, 1);
  assert.ok(record._archive_meta);
});
