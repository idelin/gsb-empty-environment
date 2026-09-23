# versioned-state-store

零依赖的 TypeScript 状态存储（Node.js 原生类型擦除直接运行，无响应式框架）：

- **显式迁移**：schema 升级必须走注册的 `Migration` 链（BFS 解析最短链），未知链在触碰状态前即被拒绝（`UnknownMigrationChainError`）。
- **原子事务**：`transact` 基于私有草稿提交，`getState()` 在提交前始终返回旧快照，读者永远看不到事务中间态。
- **订阅暂停**：迁移期间订阅暂停、事务被拒绝；迁移完成后以完整新状态一次性刷新，订阅者看不到半状态。
- **失败回滚**：任一迁移抛错即整体回滚，旧 schema 与旧状态原样保留，并记入审计日志（`rolled-back`）。
- **字段重命名**：迁移声明的 `renames` 自动重绑选择器订阅路径（含前缀嵌套路径）。
- **可回放**：所有已提交 op 与迁移进入追加式日志，`replay()` 可从初始快照精确重建当前状态（`verifyReplay()` 校验）。
- **可审计**：`getAuditLog()` 记录每次迁移尝试的版本链、重命名映射与结果；`getVersionMap()` 列出已注册的版本边。

## 运行

```bash
npm run demo   # node src/demo.ts
npm test       # node --test（Node.js 内置测试运行器）
```

要求 Node.js >= 23.6（原生 TypeScript 类型擦除）。

## 文件

- `src/store.ts` — 核心存储：事务、订阅、迁移链、日志回放、审计
- `src/migrations.ts` — 示例迁移 v1→v2（重命名）、v2→v3（新增字段）、v3→v4（故意失败）
- `src/demo.ts` — 端到端演示
- `test/store.test.ts` — 12 个测试用例

## 运行样例（`npm run demo` 输出）

```
=== 2. Atomic transaction (reader sees only committed state) ===
  mid-transaction getState().user.age = 36 (still old)
  after commit: {"user":{"name":"Ada","age":37},"cart":["book"]}

=== 3. Migrate v1 -> v3 (explicit chain, subscriptions paused) ===
  schemaVersion = 3
  state = {"user":{"age":37,"fullName":"Ada"},"cart":["book"],"settings":{"theme":"light"}}
  notifications during migration: 0 (paused; flush happens once, only if the selected value changed)

=== 4. Selector rebound by field rename ===
  subscriber notified, user.name = "Ada Lovelace"

=== 5. Failed migration rolls back schema + state ===
  error: simulated migration failure
  schemaVersion = 3 (unchanged)

=== 6. Unknown migration chain rejected ===
  rejected: No migration chain from schema v3 to v99. Known versions: [1, 2, 3, 4]

=== 7. Replayable op log ===
  replay matches current state: true

=== 8. Auditable version mapping ===
  audit log:
    {"seq":1,...,"requestedFrom":1,"requestedTo":3,"chain":[1,2,3],"renames":{"user.name":"user.fullName"},"status":"committed"}
    {"seq":2,...,"requestedFrom":3,"requestedTo":4,"chain":[3,4],"status":"rolled-back","error":"simulated migration failure"}
    {"seq":3,...,"requestedFrom":3,"requestedTo":99,"status":"rejected","error":"Unknown migration chain v3 -> v99"}
```

## 测试结果（`npm test`）

```
✔ transaction commits atomically: readers never see intermediate state
✔ throwing transaction changes nothing and notifies nobody
✔ subscribers are paused during migration and flushed once with full state
✔ failed migration restores old schema and state, audits rollback
✔ unknown migration chain is rejected before touching state
✔ field rename rebinds selector subscriptions
✔ rename rebinds nested subscription paths by prefix
✔ op log replays to the exact current state across migrations
✔ audit log records committed chain and renames (version mapping)
✔ transactions are rejected while a migration is in progress
✔ subscriber is not notified when selected value is unchanged
✔ duplicate or backwards migrations are rejected at registration
ℹ tests 12  ℹ pass 12  ℹ fail 0
```
