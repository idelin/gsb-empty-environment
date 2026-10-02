import { LogicalDeleteArchiveSplitter } from './splitter.ts';
import { SinkRouter } from './router.ts';
import { SearchIndexSink } from './sinks/search-index.ts';
import { ColdStorageSink } from './sinks/cold-storage.ts';
import { SourceDb } from './source-db.ts';
import type { ChangeEvent } from './events.ts';

/**
 * 验收场景：
 *   源库收到  UPDATE user SET is_deleted = 1 WHERE id = 100
 *   分流器捕获后：
 *     - 向搜索索引发送删除 ID=100 的指令
 *     - 向冷存储发送包含完整历史数据的归档包
 *   业务下游无感知（收不到原始 Update）。
 */

// 1. 组装异构存储 Sink 与路由
const searchIndex = new SearchIndexSink();
const coldStorage = new ColdStorageSink();
const router = new SinkRouter()
  .registerSink(searchIndex)
  .registerSink(coldStorage)
  .addRule({ match: (c) => c.kind === 'archive-insert', sink: 'cold-storage' })
  .addRule({ match: (c) => c.kind === 'readstore-delete', sink: 'search-index' });

// 2. 源库与业务下游
const db = new SourceDb();
const businessSeen: ChangeEvent[] = [];

// 3. 分流中间件（rowLoader 用于补全完整历史数据）
const splitter = new LogicalDeleteArchiveSplitter({
  tables: ['user'],
  router,
  rowLoader: (table, key) => db.get(table, key),
});

db.onChange(async (event) => {
  await splitter.handle(event, (e) => {
    businessSeen.push(e); // 业务下游：只应收到非逻辑删除事件
  });
});

// 4. 准备数据：用户 100 已同步到搜索索引
db.insert('user', 100, { id: 100, name: 'Alice', email: 'alice@example.com', is_deleted: 0 });
searchIndex.documents.set(100, { id: 100, name: 'Alice', email: 'alice@example.com' });

console.log('== 初始状态 ==');
console.log('搜索索引文档数:', searchIndex.documents.size);
console.log('冷存储归档数  :', coldStorage.archives.length);

// 5. 业务执行逻辑删除：UPDATE user SET is_deleted = 1 WHERE id = 100
console.log('\n== 源库执行: UPDATE user SET is_deleted = 1 WHERE id = 100 ==');
await db.update('user', 100, { is_deleted: 1 });

// 6. 验证分流结果
console.log('\n== 分流结果 ==');
console.log('[搜索索引] 收到删除指令:', JSON.stringify(searchIndex.executedDeletes));
console.log('[搜索索引] 剩余文档数  :', searchIndex.documents.size, '(ID=100 已剔除)');

const archive = coldStorage.find('user', 100);
console.log('[冷存储] 归档包 meta :', JSON.stringify(archive?.meta));
console.log('[冷存储] 完整历史数据:', JSON.stringify(archive?.record));

console.log('\n== 业务无感知校验 ==');
console.log('业务下游收到的 update 事件数:', businessSeen.filter((e) => e.kind === 'update').length, '(应为 0)');

if (
  searchIndex.executedDeletes.length === 1 &&
  searchIndex.executedDeletes[0].key === 100 &&
  searchIndex.documents.size === 0 &&
  archive?.record['name'] === 'Alice' &&
  archive.record['is_deleted'] === 1 &&
  businessSeen.every((e) => e.kind !== 'update')
) {
  console.log('\n✅ 验收通过：删除指令已达搜索索引，完整归档包已达冷存储，业务无感知。');
} else {
  console.error('\n❌ 验收失败');
  process.exitCode = 1;
}
