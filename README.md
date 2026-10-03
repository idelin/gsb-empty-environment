# ARIES 崩溃恢复引擎 + 多节点并发冲突仲裁器（TypeScript）

基于数据库经典 ARIES 协议实现的预写日志（WAL）分析与崩溃恢复 Redo/Undo 引擎，
以及多执行体并发竞争下的冲突仲裁器（wound-wait 锁管理）。纯 TypeScript，
零依赖，使用 Node.js 内置测试运行器（Node ≥ 23.6，原生类型擦除直接运行 `.ts`）。

## 运行

```bash
npm test        # node --test "tests/*.test.ts" —— 22 个测试
npm run demo    # 端到端演示：崩溃恢复三阶段 + 多节点并发转账
```

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/log-record.ts` | WAL 记录模型：BEGIN/UPDATE/COMMIT/ABORT/END/CLR/CHECKPOINT，LSN 与 prevLSN 链 |
| `src/log-manager.ts` | 日志缓冲与强制刷盘（force-at-commit、WAL 规则），WAL 文件读写（JSONL） |
| `src/storage.ts` | 缓冲池 + 持久磁盘；pageLSN/recLSN；刷页前强制日志（WAL 规则）；`crash()` 丢弃缓冲池 |
| `src/lock-manager.ts` | 冲突仲裁器：S/X 锁、FIFO 公平队列、锁升级、wound-wait 死锁预防、wait-for 环检测 |
| `src/transaction.ts` | 事务管理器：WAL 写入、严格 2PL、运行时回滚（逐条 CLR）、非模糊 Checkpoint |
| `src/recovery.ts` | ARIES 三阶段：Analysis（重建事务表+脏页表）、Redo（历史重复）、Undo（反向扫描+CLR 补偿） |
| `src/scenario.ts` | 验收场景：3 未提交 + 2 已提交事务，Checkpoint 之后崩溃的 WAL |

## ARIES 三阶段

- **Analysis**：从最近的 CHECKPOINT 合并快照后前向扫描，重建 TransactionTable
  （lastLSN/status）与 DirtyPageTable（pageId → recLSN）；END 记录将事务移出表。
- **Redo**：从 DPT 最小 recLSN 起重复历史。仅当记录在 DPT 中、`lsn ≥ recLSN`
  且 `pageLSN < lsn` 时才重放 after-image——已持久化的页自动跳过。
- **Undo**：对所有 loser（崩溃时 RUNNING/ABORTING）按全局 LSN 降序反向扫描
  prevLSN 链，每个被撤销的 UPDATE 写一条 CLR（`undoNextLSN` 指向下一待撤销
  记录，崩溃重启幂等、绝不重复撤销），链尾写 END。

## 并发正确性

- **线性一致**：仲裁器全部状态迁移在单线程事件循环内同步完成，授权顺序即调用
  顺序，构成单一可线性化历史（测试用事件日志回放验证互斥区间不重叠）。
- **无死锁**：wound-wait 预防——年老请求者击伤（wound）年轻持锁者，年轻请求者
  只能等待年老者，wait-for 边恒为 年轻→年老，图无环（构造性保证 + 测试断言）。
  关键细节：击伤导致的锁释放可能唤醒队列中的新年轻持锁者，仲裁器会循环补伤，
  杜绝"年老者等待年轻者"的漏洞（见 `src/lock-manager.ts` 的 wound 循环）。
- **原子性**：提交即强制刷日志（force-at-commit）；中止沿 prevLSN 链写 CLR 回滚；
  被击伤事务由仲裁器同步中止，执行体观察到 `LockAbortedError`/`TransactionAbortedError`。
- **无泄漏**：所有测试结束断言锁表空、等待队列空、wait-for 图空、活动事务为零、
  日志缓冲全部落盘。

## 验收场景（`src/scenario.ts`）

5 个交错事务（T1–T5），中途 Checkpoint，T1/T2 提交，T3/T4/T5 未提交时崩溃：

- 页 1（含已提交与未提交混合数据）崩溃前已刷盘 → Undo 必须在磁盘数据上补偿；
- 页 2、3 仅存在于丢失的缓冲池 → Redo 必须重放（含未提交修改，历史重复），
  再由 Undo 剥离 loser 的修改。

恢复后断言：T1/T2 全部修改持久化，T3/T4/T5 全部修改回滚为 before-image，
WAL 文件追加 5 条 CLR 与 3 条 END，二次重启恢复幂等（零新增 CLR）。

## 测试覆盖（22 个用例）

- `tests/recovery.test.ts`：Analysis 表重建、Redo 选择性重放、Undo/CLR/END 计数、
  恢复幂等、CLR `undoNextLSN` 跳转（崩溃于回滚中途）、无 Checkpoint 全扫描、空 WAL。
- `tests/arbiter.test.ts`：S 兼容/X 互斥、FIFO 防插队、可重入、锁升级、wound-wait
  双向（年老击伤年轻 / 年轻等待年老）、受害者挂起请求拒绝、非法释放报错、
  releaseAll 唤醒、授权历史可线性化（互斥区间不重叠）。
- `tests/concurrency.test.ts`：8 节点 × 30 事务转账不变量、6 节点工作负载崩溃中
  恢复（按 WAL 重放已提交事务独立核对每一账户）、10 节点随机多锁无序获取
  无死锁终止性；全部断言严格无泄漏。

## 测试结果

```text
ℹ tests 22
ℹ pass 22
ℹ fail 0
```
