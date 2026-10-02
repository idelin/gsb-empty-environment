# 逻辑删除转物理归档流式分流器

业务逻辑标记处理中间件：拦截表中将 `is_deleted` 置为 `1` 的 Update 事件，自动转换为
下游物理归档库（冷存储）的 Insert 与只读查询库（搜索索引）的 Delete，支持跨异构存储系统的路由分发，业务无感知。

## 架构

```
源库 SQL ──> CDC 事件流 ──> LogicalDeleteArchiveSplitter（中间件）
                                │ 命中 is_deleted: 0 -> 1 的 update
                                ├─> ArchivePackage ──> SinkRouter ──> ColdStorageSink（物理归档库，完整历史数据）
                                └─> PurgeCommand   ──> SinkRouter ──> SearchIndexSink（只读查询库，按主键删除）
                                │ 未命中
                                └─> 原样透传给业务下游
```

- `src/events.ts` — CDC 事件与分流产物（归档包 / 删除指令）类型
- `src/splitter.ts` — 分流中间件 `LogicalDeleteArchiveSplitter`
- `src/router.ts` — 异构存储路由分发器 `SinkRouter`（规则匹配 + 投递回执）
- `src/sinks/search-index.ts` — 只读查询库 Sink
- `src/sinks/cold-storage.ts` — 物理归档库 Sink
- `src/source-db.ts` — 模拟源库（发出 CDC 变更事件）
- `src/demo.ts` — 验收场景运行样例
- `test/splitter.test.ts` — Node.js 内置测试（`node:test`）

## 运行要求

Node.js >= 22.6（利用原生 TypeScript 类型擦除运行 `.ts`，无需编译与第三方依赖；推荐 >= 23.6）。

## 运行样例

```bash
npm run demo        # 等价于 node src/demo.ts
```

输出（验收场景：`UPDATE user SET is_deleted = 1 WHERE id = 100`）：

```
== 初始状态 ==
搜索索引文档数: 1
冷存储归档数  : 0

== 源库执行: UPDATE user SET is_deleted = 1 WHERE id = 100 ==

== 分流结果 ==
[搜索索引] 收到删除指令: [{"kind":"readstore-delete","table":"user","key":100}]
[搜索索引] 剩余文档数  : 0 (ID=100 已剔除)
[冷存储] 归档包 meta : {"archivedAt":1790912608609,"reason":"logical-delete","sourceEventTs":1790912608609}
[冷存储] 完整历史数据: {"id":100,"name":"Alice","email":"alice@example.com","is_deleted":1}

== 业务无感知校验 ==
业务下游收到的 update 事件数: 0 (应为 0)

✅ 验收通过：删除指令已达搜索索引，完整归档包已达冷存储，业务无感知。
```

## 测试结果

```bash
npm test            # 等价于 node --test test/splitter.test.ts
```

```
✔ 验收场景：UPDATE user SET is_deleted=1 WHERE id=100 被分流为索引删除 + 冷存储归档
✔ 普通 Update（非逻辑删除）原样透传给业务下游，不产生分流
✔ 重复删除（is_deleted 已为 1）不重复归档
✔ 未配置的表不被拦截
✔ update 只携带变更列时，rowLoader 回源补全完整历史数据
✔ insert/delete 事件不被拦截，直接透传
✔ 路由分发：归档包只进冷存储，删除指令只进搜索索引
ℹ tests 7  ℹ pass 7  ℹ fail 0
```

## 用法

```ts
import { LogicalDeleteArchiveSplitter } from './src/splitter.ts';
import { SinkRouter } from './src/router.ts';
import { SearchIndexSink } from './src/sinks/search-index.ts';
import { ColdStorageSink } from './src/sinks/cold-storage.ts';

const router = new SinkRouter()
  .registerSink(new SearchIndexSink())
  .registerSink(new ColdStorageSink())
  .addRule({ match: (c) => c.kind === 'archive-insert', sink: 'cold-storage' })
  .addRule({ match: (c) => c.kind === 'readstore-delete', sink: 'search-index' });

const splitter = new LogicalDeleteArchiveSplitter({
  tables: ['user'],                 // 只拦截 user 表
  router,
  rowLoader: (table, key) => db.get(table, key), // 回源补全完整历史数据
});

// 接入 CDC 事件流；next 为业务下游处理器
await splitter.handle(event, (e) => businessPipeline(e));
```

### 关键行为

- 仅拦截 `update` 且标记列由非 `1` 变为 `1` 的事件；重复删除幂等（不重复归档）。
- 归档包优先使用事件 `before/after` 镜像，缺失列时通过 `rowLoader` 回源补全，保证完整历史数据。
- 被拦截的原始 Update 不再透传，业务下游无感知。
- 标记列名可通过 `deletedFlag` 自定义；`tables` 缺省时拦截所有表。
