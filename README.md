# ARIES 崩溃恢复引擎 + 无锁并发原语（Go，仅标准库）

实现数据库经典 ARIES 崩溃恢复协议的三阶段扫描，并以无锁数据结构
与 CAS 原子操作替代粗粒度全局互斥锁，提升并发吞吐。

## 组件

- `wal.go` — 预写日志：UPDATE/COMMIT/ABORT/END/CLR/CHECKPOINT 记录的二进制
  编码、追加、全量扫描；LSN 由无锁 CAS 分配器发放，追加路径无全局锁竞争。
- `aries.go` — 恢复引擎：
  - **Analysis**：从最近的 Checkpoint 重建 TransactionTable 与
    DirtyPageTable（recLSN），确定 redo 起点与 loser 事务集合；
  - **Redo**：历史重复，从最小 recLSN 正向扫描，按
    `pageLSN < rec.LSN` 判定跳过已持久化更新（幂等）；
  - **Undo**：以最大堆按 LSN 全局逆序回滚 loser 事务，每个被撤销的
    更新写入 CLR（含 undoNextLSN），重复崩溃时跳过已补偿记录，
    事务结束时写 END。
- `lockfree.go` — Michael-Scott 无锁 MPMC 队列（`atomic.Pointer` CAS，
  Go GC 下无 ABA 问题）与 CAS LSN 分配器，全程无互斥锁。
- `workload.go` — 验收场景构造器：3 个未提交事务（T3/T4 运行中、
  T5 中止中）+ 2 个已提交事务（T1/T2），Checkpoint 之后崩溃。
- `cmd/ariesdemo` — 可运行演示：构造 WAL → 崩溃 → 三阶段恢复 → 校验。

## 运行

```sh
go test -race -v ./...   # 全部测试（含竞态检测）
go run ./cmd/ariesdemo   # 恢复演示
```

## 测试覆盖

- `aries_test.go`：验收场景端到端恢复（Analysis 表内容、Redo 起点与
  跳过已刷盘页、Undo 的 5 条 CLR 与 END、最终页数据精确匹配）、
  恢复幂等性（二次恢复零 CLR）、无 Checkpoint 路径、WAL 编解码往返
  与 LSN 断点续分配。
- `lockfree_test.go`：空队列出队、单元素、FIFO 顺序、交替入出队、
  SPSC/MPMC 高并发不丢不重、严格无泄漏（Len==0 且 Dequeue 返回
  false）、CAS LSN 分配器并发唯一性与连续性。
