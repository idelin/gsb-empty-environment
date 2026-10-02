import { once } from 'node:events';
import { LogicalDeleteArchiveMiddleware } from '../src/logicalDeleteMiddleware.ts';
import { ConsoleSink } from '../src/sinks.ts';
import { SimulatedSourceDB } from '../src/source.ts';
import { Splitter } from '../src/splitter.ts';
import type { ChangeEvent, DispatchReceipt } from '../src/types.ts';

// ---- 装配：源库 -> 分流器(中间件) -> 异构下游 ----
const source = new SimulatedSourceDB('mysql-main');
source.seed('user', 'id', [
  { id: 100, name: 'Alice', email: 'alice@example.com', is_deleted: 0 },
  { id: 101, name: 'Bob', email: 'bob@example.com', is_deleted: 0 },
]);

const splitter = new Splitter()
  .registerSink(new ConsoleSink('cold-storage', '冷存储/物理归档库'))
  .registerSink(new ConsoleSink('search-index', '搜索索引'))
  .registerSink(new ConsoleSink('readonly-query', '只读查询库'))
  .use(new LogicalDeleteArchiveMiddleware());

const receipts: DispatchReceipt[] = [];
source.on('cdc', async (event: ChangeEvent) => {
  receipts.push(...(await splitter.process(event)));
});

// ---- 验收场景：业务侧只执行一次普通 UPDATE，对分流完全无感知 ----
console.log('业务执行: UPDATE user SET is_deleted = 1 WHERE id = 100;');
const affected = source.executeUpdate('UPDATE user SET is_deleted = 1 WHERE id = 100');
console.log(`业务收到响应: ${affected} row affected（业务无感知，分流异步进行）\n`);

await once(source, 'cdc'); // 等待 CDC 事件发出
await new Promise((resolve) => setImmediate(resolve)); // 等待分流完成

console.log('\n分流回执:');
for (const r of receipts) {
  const target =
    r.action.type === 'insert'
      ? `${r.action.sink}.${r.action.table} (归档包)`
      : `${r.action.sink}.${r.action.table} id=${r.action.id}`;
  console.log(`  ${r.status === 'ok' ? '✓' : '✗'} ${r.action.type.toUpperCase()} -> ${target}`);
}

// 对照：普通更新不触发归档分流
console.log('\n业务执行: UPDATE user SET email = \'bob@new.com\' WHERE id = 101;');
source.executeUpdate("UPDATE user SET email = 'bob@new.com' WHERE id = 101");
await once(source, 'cdc');
await new Promise((resolve) => setImmediate(resolve));
console.log(`普通更新产生的分流动作数: ${receipts.length - 3}（预期 0，直接透传不拦截）`);
