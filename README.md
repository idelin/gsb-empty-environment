# entity-store

一个零依赖的归一化实体缓存（TypeScript，Node.js 内置测试运行器验证）。

## 能力

- **归一化**：嵌套响应按 `__typename` + `id` 拆成实体表，嵌套实体替换为共享引用 `{ $ref }`；两个父级引用同一实体时同步看到更新。
- **部分响应**：只写回包中出现的字段，未返回字段保持不变；缺字段（`undefined`/不存在）与显式 `null` 严格区分。
- **反向索引**：字段写入时同步维护 `target -> source -> fields`；显式删除实体时清理反向索引并把存活方的引用置空（列表则过滤）；支持引用环的整体删除。
- **查询租约**：持有租约的实体删除被延迟（标记 `pendingDelete`），租约释放后才真正移除并清理引用。
- **分页关系**：`applyPage` 合并分页连接，按引用去重、保序，保留最新 `pageInfo`。
- **乐观叠加层**：`store.optimistic()` 的修改立即对读者可见，`commit()` 原子落库，`rollback()` 只丢弃本层变更。
- **字段版本**：逻辑时钟为每个字段定版；携带 `baseVersion` 的旧服务器回包不会覆盖用户随后提交的更新版本。
- **身份合并**：`mergeIdentity` 将重复实体合并，字段按版本裁决，反向索引与租约原子迁移。
- **无 ID 嵌套对象**：作为内嵌值按父级独立存放，结构相同也不会误合并；重建整体替换，缺失键与显式 `null` 可区分。
- **事务与回放**：事务在工作副本上应用，提交时原子换入，读者永远看不到中间态；所有状态变更记录为可序列化操作日志，`Store.replay(ops)` 可完整重建状态。

## 运行

需要 Node.js >= 22.6（`--experimental-strip-types`）。

```sh
npm test    # node --experimental-strip-types --test test/*.test.ts
npm run demo
```

## 结构

- `src/types.ts` — 值模型（Ref / Connection / Embedded）、操作日志类型
- `src/state.ts` — 实体表、反向索引、租约、`applyOp`（删除 / 合并 / 租约释放）
- `src/normalize.ts` — 响应归一化（实体抽取、内嵌对象、缺省/null 语义）
- `src/store.ts` — `Store` / `Transaction` / `OptimisticLayer`、读路径与反规范化
