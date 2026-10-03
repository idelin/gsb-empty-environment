# ARIES 崩溃恢复引擎 + 多节点并发冲突仲裁器（TypeScript）

基于数据库经典 **ARIES 协议**的预写日志（WAL）崩溃恢复实现，以及保障多执行体并发
原子性与线性一致性的冲突仲裁器。纯 TypeScript，零依赖，使用 Node.js 内置测试运行器
（Node ≥ 22.6，利用原生类型擦除直接运行 `.ts`）。

## 架构

```
src/
├── log/
│   ├── records.ts        # WAL 记录类型：BEGIN/UPDATE/COMMIT/ABORT/END/CLR/CHECKPOINT
│   └── logManager.ts     # LSN 分配、日志刷盘（force-at-commit）、WAL 读取
├── storage/
│   ├── disk.ts           # 页式磁盘（原子 rename 落盘），页带 pageLSN
│   └── bufferPool.ts     # 缓冲池：steal + no-force 策略
├── txn/
│   └── transactionManager.ts  # 正常处理：严格 WAL、模糊检查点
├── recovery/
│   └── recoveryEngine.ts # ARIES 三阶段恢复
└── concurrency/
    └── arbiter.ts        # 多节点并发竞争与冲突仲裁器
```

## ARIES 三阶段恢复

1. **Analysis**：从最近的 `END_CHECKPOINT` 快照（事务表 + 脏页表）出发向前扫描，
   重建崩溃瞬间的 TransactionTable 与 DirtyPageTable，确定败者事务与 redoLSN。
2. **Redo（重复历史）**：从 DPT 最小 recLSN 起扫描，对 UPDATE 与 CLR 一律重做；
   通过 `pageLSN >= lsn` 判断跳过，保证幂等（对已落盘/被偷取的脏页安全）。
3. **Undo（反向扫描）**：按各败者事务 lastLSN 的最大堆逆序回滚，每补偿一条
   UPDATE 就写入 CLR（`undoNextLSN` 指向下一待回滚记录），恢复中途再次崩溃
   也能沿 CLR 链跳过已补偿部分，绝不重复回滚。

## 并发冲突仲裁器

- **无死锁**：节点注册时获得唯一递增时间戳，采用 wound-wait 纪律——年长者击伤
  年轻持有者，年轻者只能等待；等待图只存在“年轻→年长”的边，结构上无环。
- **原子性**：所有仲裁状态迁移在同步临界区完成（check-and-act 之间无 await），
  被击伤节点的存活句柄立即失效，其进行中事务在提交前校验处被拦截、修改被丢弃。
- **线性一致**：每次成功提交获得全局单调序号（线性化点），完整事件历史可回放校验。
- **无泄漏**：FIFO 等待队列防插队；`assertQuiescent()` 断言锁表/等待队列/句柄全清空。
- 支持共享/排他锁、重入引用计数、SHARED→EXCLUSIVE 升级（升级死锁由 wound-wait 仲裁）。

## 运行

```bash
npm test    # 或 node --test
npm run demo
```

## 测试覆盖（19 项）

**恢复（test/recovery.test.ts）**
- 验收场景：2 已提交 + 3 未提交事务，checkpoint 后崩溃；三阶段扫描后
  已提交数据持久化、未提交修改全部回滚，CLR 链完整
- steal：未提交脏页崩溃前已落盘，恢复仍能正确回滚
- 恢复中途再次崩溃：CLR 的 undoNextLSN 保证幂等
- 重复执行完整恢复结果一致

**仲裁器（test/arbiter.test.ts）**
- 共享兼容 / 排他互斥 / 共享-排他冲突 / FIFO 公平
- wound-wait 双向：年长者击伤年轻者、年轻者等待年长者
- 交叉持锁死锁场景必然被打破；等待中止传播；双方同时升级循环仲裁
- 重入引用计数、重复释放防御
- 8 节点 × 50 次高并发递增：无丢失更新、序号严格递增、严格无泄漏
