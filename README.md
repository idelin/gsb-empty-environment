# SS2PL 内核：严格两阶段锁与收缩期管理器

TypeScript 实现的严格两阶段锁（Strict 2PL / SS2PL）并发控制内核，零第三方依赖，
使用 Node.js 内置测试运行器（node:test）验证。

## 核心保证

- **严格两阶段加锁**：事务在扩张期（growing phase）只加锁、不释放；
  收缩期（shrinking phase）被压缩为提交/回滚的单一原子点——此刻一次性释放全部锁。
  Transaction 不暴露任何单锁释放 API，提前释放局部行锁在结构上不可能发生。
- **提交时间戳可串行化**：所有锁持有至提交点，因此调度冲突等价于按
  commitTimestamp 排序的串行调度（TransactionManager.commitOrder()）。
- **严格性（Strictness）**：未提交写入对其它事务不可见（写锁阻塞读），
  级联回滚不可能发生。
- **崩溃恢复**：注入崩溃时安全撤销事务持有的全部锁、逆序回滚（undo）其全部修改，
  并将其从所有等待队列中摘除，被阻塞的事务随即安全继续。
- **死锁检测**：基于等待图（wait-for graph）的环检测，闭合环路的请求方被选为
  牺牲者并自动回滚。

## 目录结构

- src/lock-manager.ts — 锁表：共享/排他锁、FIFO 公平队列、锁升级、等待图死锁检测、审计事件
- src/shrinking-phase-manager.ts — 收缩期管理器：仅在 committed/aborted 状态放行 releaseAll
- src/transaction.ts — 事务：扩张期只加锁；commit/abort 进入收缩点；undo 日志回滚
- src/transaction-manager.ts — 事务管理器：提交时间戳分配、提交日志、崩溃注入
- src/lock-monitor.ts — 锁监控器：订阅审计流，发现任何 growing 期的释放即判违规
- src/store.ts — 内存数据存储（行 = 键）
- tests/ — Node 内置测试（锁管理器 / SS2PL 性质 / 崩溃恢复）
- examples/demo.ts — 可运行样例：并发转账 + 崩溃注入

## 运行

要求 Node.js >= 22.6（使用原生 TypeScript 类型擦除运行 .ts，无需编译；在 v23.9 上验证）。

    npm test        # node --test（自动发现 tests/ 下的 *.test.ts）
    npm run demo    # node examples/demo.ts

## 测试覆盖（17 项）

- **锁管理器**：共享锁兼容、排他锁阻塞、FIFO 公平、锁升级、互升级死锁、
  跨资源死锁（等待图）、releaseAll 原子释放、等待取消
- **SS2PL 性质**：扩张期零释放（监控器全程校验）、收缩期管理器拒绝非法释放、
  未提交写不可见、回滚一次性放锁、并发调度与提交时间戳序等价
  （按提交序重放写操作，最终库状态逐键一致）
- **崩溃恢复**：注入崩溃后锁全部撤销、修改回滚（含未提交插入的删除）、
  被阻塞读恢复并读到回滚后的值、等待中的事务崩溃后从队列摘除、
  死锁牺牲者安全回滚且幸存者正常提交

## 样例输出（npm run demo）

    --- Scenario 1: concurrent transfers over contended accounts ---
    txn 1: transfer 30 from alice to bob (attempt 1)
      [lock] acquire txn=1 shared    on [alice] (txn state: growing)
      [lock] wait    txn=1 exclusive on [bob] (txn state: growing)
      [lock] wait    txn=2 exclusive on [bob] (txn state: growing)
      [lock] release txn=2 shared    on [bob] (txn state: aborted)
    txn 2: chosen as deadlock victim -> aborted, writes rolled back, retrying
      [lock] release txn=1 exclusive on [alice] (txn state: committed)
      [lock] release txn=1 exclusive on [bob] (txn state: committed)
    txn 1: committed with commitTimestamp=1
    commit order (serialization order): [ 1, 3, 4 ]
    final balances: { alice: 80, bob: 110, carol: 110 }

    --- Scenario 2: injected crash mid-transaction ---
    txn 5: wrote alice=5, dave=500 (uncommitted), holding: [ 'alice', 'dave' ]
    txn 6: read of alice is blocked behind txn 5
    >>> injecting crash into txn 5
      [lock] release txn=5 exclusive on [alice] (txn state: aborted)
      [lock] release txn=5 exclusive on [dave] (txn state: aborted)
    txn 5: state=aborted reason=crash, locks left: 0
    txn 6: read of alice unblocked, value = 80
    store after recovery: { alice: 80, bob: 110, carol: 110 }

    OK: no lock was ever released before commit/abort (SS2PL verified).

注意审计日志中所有 release 事件的事务状态均为 committed/aborted——
锁监控器（LockMonitor）在全部测试与样例中断言：growing 期释放事件数为零。
