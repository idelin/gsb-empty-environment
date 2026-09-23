# state-schema-migration

零依赖的 TypeScript 应用状态库：手写的事务化 Store + 显式 schema 迁移器。
不使用任何现成响应式框架，仅依赖 Node.js 内置模块（`node:test`、`structuredClone`）。

要求 Node.js >= 23.6（原生 TypeScript 类型擦除，无需编译步骤）。

## 运行

```sh
npm test    # node --test "test/*.test.ts"
npm run demo
```

## 设计保证

- **显式迁移**：schema 升级只通过 `SchemaRegistry` 中注册的 `Migration` 边执行；
  `Migrator` 用 BFS 在迁移图中寻找从当前版本到目标版本的链并逐条执行。
- **读者隔离**：事务在私有草稿上操作，提交时原子替换已冻结的快照；
  读者（`getState` / `get` / `select`）永远只能看到最近一次提交，看不到中间态。
- **订阅暂停**：迁移期间 `pauseSubscriptions()` 挂起通知，完整新状态发布后才
  `resumeSubscriptions()`，订阅者只收到一次最终通知，看不到半状态。
- **失败回滚**：整条迁移链先在草稿上执行，全部成功后才一次性替换状态、
  重映射选择器绑定并写入日志；任一步抛错则旧 schema、旧状态、旧绑定原样保留。
- **可回放**：所有写操作以 op 日志（`set` / `delete`）和迁移快照（`replace`）
  记入 `Journal`；`replayJournal(genesis, entries)` 可完整重建任意时刻的状态。
- **字段重命名**：迁移声明 `renames: { oldPath: newPath }`，`Store.remapBindings`
  会把既有 `bind(path, cb)` 选择器绑定（含子路径前缀）改绑到新字段。
- **未知链拒绝**：目标版本不可达时抛 `UnknownMigrationChainError`，状态与日志不变。
- **可审计**：`AuditLog` 按序记录每一步迁移（含 renames）、提交、失败与拒绝。

## 结构

- `src/store.ts` — 事务、快照、订阅/字段绑定、暂停/恢复
- `src/schema.ts` — 版本注册表与迁移图（BFS 寻路、未知链检测）
- `src/migrator.ts` — 两阶段迁移编排（草稿执行 → 原子发布 / 回滚）
- `src/journal.ts` — 操作日志与回放
- `src/audit.ts` — 版本映射审计日志
- `examples/demo.ts` — 端到端样例
- `test/` — `node:test` 测试（13 个用例）
