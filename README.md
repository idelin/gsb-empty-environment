# 逻辑删除转物理归档流式分流器

业务逻辑标记处理中间件：拦截表中将 `is_deleted` 置为 `1` 的 Update 事件，自动转换为
下游物理归档库的 Insert 事件与只读查询库 / 搜索索引的 Delete 事件，支持跨异构存储
系统的路由分发，业务侧完全无感知。

## 架构

```
源库 SQL ──> CDC 事件 ──> Splitter（中间件链）──> RoutedAction ──> 异构 Sink
                             │                        ├─ Insert ──> 冷存储/物理归档库（归档包）
                             │                        ├─ Delete ──> 搜索索引
                             │                        └─ Delete ──> 只读查询库
```

- `src/types.ts` — ChangeEvent / RoutedAction / Sink / Middleware 等核心类型
- `src/logicalDeleteMiddleware.ts` — 逻辑删除标记中间件，仅响应 `is_deleted` 非 1 → 1 的跃迁，
  生成携带完整历史数据的归档包（含 `_archive_meta` 溯源元数据）
- `src/splitter.ts` — 流式分流器：中间件链编排、Sink 注册、逐动作分发、错误隔离，
  并提供 object-mode `Transform` 流接口接入任意 CDC 管道
- `src/sinks.ts` — Sink 适配器（内存记录型 / 控制台型），新增异构存储只需实现 `Sink` 接口
- `src/source.ts` — 模拟源库：执行 UPDATE SQL 并异步发出 CDC 事件

## 运行

要求 Node.js ≥ 22.6（利用内置 TypeScript 类型擦除，零依赖；≥ 23.6 默认开启）。

```bash
npm test    # node --test，运行 Node.js 内置测试
npm run demo
```

## 运行样例（`npm run demo`）

```
业务执行: UPDATE user SET is_deleted = 1 WHERE id = 100;
业务收到响应: 1 row affected（业务无感知，分流异步进行）

  [冷存储/物理归档库] INSERT INTO archive_user: {"id":100,"name":"Alice","email":"alice@example.com","is_deleted":1,"_archive_meta":{"source":"mysql-main","sourceTable":"user","keyColumn":"id","primaryKey":100,"deleteKind":"logical",...}}
  [搜索索引] DELETE user WHERE id = 100
  [只读查询库] DELETE user WHERE id = 100

分流回执:
  ✓ INSERT -> cold-storage.archive_user (归档包)
  ✓ DELETE -> search-index.user id=100
  ✓ DELETE -> readonly-query.user id=100

业务执行: UPDATE user SET email = 'bob@new.com' WHERE id = 101;
普通更新产生的分流动作数: 0（预期 0，直接透传不拦截）
```

## 测试结果（`npm test`，Node.js 内置 `node:test`，8/8 通过）

```
✔ is_deleted 0->1 的 Update 被转换为归档 Insert + 只读侧 Delete
✔ 未置 is_deleted 的普通 Update 不触发分流
✔ is_deleted 已是 1 的重复更新不重复归档（仅拦截 0->1 跃迁）
✔ Insert / Delete 事件不被中间件拦截
✔ 单个 Sink 故障被隔离，其余 Sink 正常分发并给出失败回执
✔ 路由到未注册 Sink 时返回失败回执而非抛错
✔ 流式接口：Transform 管道中事件进、回执出
✔ 验收边界：源库执行 UPDATE user SET is_deleted=1 WHERE id=100，业务无感知完成分流
ℹ tests 8  ℹ pass 8  ℹ fail 0
```

## 验收边界对应

源库收到 `UPDATE user SET is_deleted = 1 WHERE id = 100` 后：

1. 分流器捕获该 Update 事件（`is_deleted` 0 → 1 跃迁）；
2. 向搜索索引发送删除 `ID=100` 的指令；
3. 向冷存储系统发送包含完整历史数据的归档包（整行数据 + 溯源元数据）；
4. 业务仅收到 `1 row affected`，对分流全程无感知。
