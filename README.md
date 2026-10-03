# ARIES 崩溃恢复引擎（TypeScript）

数据库经典 **ARIES**（Algorithm for Recovery and Isolation Exploiting Semantics）
协议的完整实现：预写日志（WAL）、三阶段崩溃恢复（Analysis / Redo / Undo）、
基于 CLR（补偿日志记录）的反向补偿与事务回滚状态编排。

纯 TypeScript 编写，零第三方依赖，使用 Node.js 内置 `node:test` 测试框架验证。

## 环境要求

- Node.js **>= 22.6**（依赖原生 TypeScript type-stripping 与 `node:test`）
- `npm run sample` / `npm test` 会通过 `scripts/run.sh` 自动选择满足版本要求的
  Node 解释器（包括 `~/.nvm/versions/node/*` 中已安装的版本）

## 快速开始

```bash
npm run sample   # 运行崩溃恢复演示样例
npm test         # 运行全部 29 个测试（node:test）
```

## 架构

```
src/
  types.ts        日志记录类型：UPDATE / CLR / COMMIT / ABORT / END / 检查点
  wal.ts          预写日志：JSONL 追加写 + 同步落盘，支持按 LSN 随机访问
  storage.ts      磁盘存储 + LRU 缓冲池（淘汰时校验 WAL 先写协议）
  lockManager.ts  页级排他锁（FIFO 队列、事务级可重入、严格 2PL）
  transaction.ts  事务管理器：前向处理 + ABORT 反向补偿（CLR 链）
  recovery.ts     ARIES 恢复引擎：Analysis → Redo → Undo
  system.ts       系统门面：检查点、刷盘、崩溃模拟、恢复入口
sample/
  crash-recovery-demo.ts  端到端演示（5 事务 + 检查点 + 崩溃 + 恢复）
tests/            29 个测试，覆盖三阶段与全部边界条件
```

## 三阶段恢复

1. **Analysis**：从最近的模糊检查点（END_CHECKPOINT 携带事务表/脏页表快照）
   出发前向扫描，重建 TransactionTable 与 DirtyPageTable，识别 Winner
   （已提交）与 Loser（未提交/中止中）事务，确定 Redo 起点（最小 recLSN）。
2. **Redo（历史重复）**：从 redoLSN 前向扫描，重放所有 UPDATE/CLR 的后镜像。
   以页上 `pageLSN` 判定跳过已持久化/已重放的记录 —— **幂等**。
3. **Undo（反向补偿）**：以最大堆对所有 Loser 的日志链做全局逆序扫描，
   每条 UPDATE 写一条 CLR（after = 前镜像，`undoNextLSN` 指向链上下一个
   待处理 LSN），CLR 沿 `undoNextLSN` 跳过已补偿部分；链空写 END 收尾。

## 失败逆向补偿与幂等性

- **长流程失败补偿**：事务中止（`abort`）与崩溃恢复 Undo 共用同一条 CLR
  补偿逻辑，严格按执行逆序撤销，逐步恢复前镜像。
- **崩溃可重入**：每条 UPDATE 至多被一条 CLR 补偿；崩溃发生在中止/恢复
  中途时，重启后经 `undoNextLSN` 续扫，**不会重复补偿**。
- **恢复幂等**：重复执行恢复不追加任何日志、不改变任何页（测试断言）。

## 验收场景（tests/recovery.test.ts）

- 3 个未提交事务 + 2 个已提交事务，模糊检查点之后崩溃；
- 三阶段扫描后：已提交数据持久化（P1=A1, P2=B1, P3=C2），
  未提交修改全部回滚（P4/P5/P6 回到前镜像）；
- 严格无泄漏：事务表清空、锁全部释放、脏页表刷盘后无残留、
  每个事务以 END 收尾、每条 loser UPDATE 恰好一条 CLR；
- 崩溃注入覆盖：ABORT 中途崩溃、恢复 Undo 中途崩溃、COMMIT 后 END 前崩溃。

## 并发安全

页级排他锁 + 严格 2PL（锁持有至提交/回滚），FIFO 等待队列保证公平互斥；
测试覆盖 20/50 事务高竞争递增（无丢失更新）、提交/中止混合、
高并发负载中崩溃后恢复的最终一致性。
