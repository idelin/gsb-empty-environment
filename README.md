# ARIES 崩溃恢复引擎 + 高性能无锁并发队列（Go）

基于经典 ARIES 协议的数据库崩溃恢复引擎，WAL 追加路径完全去除粗粒度全局互斥锁，
改用 CAS 原子操作与无锁 MPMC 队列加速。仅使用 Go 标准库。

## 结构

```
lfqueue/    有界 MPMC 无锁队列（Vyukov 序列号环形缓冲，全程 CAS，无互斥锁）
wal/        预写日志：记录格式（UPDATE/COMMIT/ABORT/END/CLR/CHECKPOINT）、
            二进制编解码、CAS 分配 LSN 的无锁 Append、撕裂写容错扫描
recovery/   ARIES 三阶段恢复引擎：Analysis / Redo / Undo
cmd/ariesdemo/  可运行样例：构造 WAL → 崩溃 → 三阶段恢复
```

## 无锁设计

- **LSN 分配**：`atomic.Uint64.Add`（CAS 加速），无全局锁，并发 Append 各自获得唯一 LSN。
- **日志缓冲**：Vyukov 有界 MPMC 队列，每个槽位一个序列号，head/tail 游标仅通过
  CAS 推进；生产者和消费者互不阻塞，满/空时立即返回错误而非阻塞。
- **缓存行填充**：head/tail/槽位分离到不同 cache line，消除 false sharing。
- **Flush 路径**：唯一使用互斥锁的地方是落盘 `Flush` 自身（串行化 fsync），
  与 Append 热路径完全解耦。

基准（Apple M1 Pro）：无锁 MPMC 约 **19 ns/op**，互斥锁队列约 **435 ns/op**（~22×）。

## ARIES 三阶段

1. **Analysis**：从最近的 Checkpoint（含 TT/DPT 快照）正向扫描，重建
   TransactionTable（Running/Committed/Aborting，END 时移除）与 DirtyPageTable
   （每页 recLSN），确定崩溃时的 loser 事务与 redo 起点（最小 recLSN）。
2. **Redo（历史重复）**：从最小 recLSN 起重放所有 UPDATE/CLR；仅当页在 DPT 中、
   记录 LSN ≥ recLSN 且 `pageLSN < LSN` 时才应用，保证幂等。
3. **Undo（反向补偿）**：以最大堆按 LSN 逆序处理所有 loser 的日志链；对每个
   UPDATE 写 CLR（`undoNext = prevLSN`）并应用 before-image；遇到 CLR 沿
   `undoNext` 跳过已补偿记录；链尾写 END。崩溃中断的 abort 可正确续跑。

## 运行样例

```sh
go run ./cmd/ariesdemo
```

构造 2 个已提交事务（T1、T2）+ 3 个未提交事务（T3、T4、T5），在 Checkpoint
之后崩溃；恢复后已提交修改持久化（P1="T1-P1"、P2="T1-P2"、P3="T2-P3"），
未提交修改全部回滚（P4、P5 恢复初始值），WAL 追加 5 条 CLR 与 3 条 END。

## 测试

```sh
go test ./...          # 全部单元测试
go test -race ./...    # 竞态检测（并发安全）
go test ./lfqueue/ -bench=.   # 无锁 vs 互斥锁基准
```

覆盖内容：

- **恢复**：Analysis 的 TT/DPT 精确重建、Redo 的 pageLSN 跳过、Undo 的 CLR 链
  与 undoNext 指针、崩溃中断 abort 的续跑、二次恢复幂等性。
- **无锁队列**：空出队/满入队边界、容量非 2 的幂取整、FIFO 顺序、环形复用
  （wraparound）、8 生产者 × 8 消费者共 16 万条消息恰好各投递一次、
  SPSC 顺序性、无 goroutine 泄漏断言。
- **WAL**：记录/Checkpoint 序列化回环、撕裂写（torn tail）容错、
  16 并发写者 LSN 唯一性、OpenAppend 续写。

测试结果（`go test -race ./...`）：

```
ok  aries/lfqueue
ok  aries/recovery
ok  aries/wal
```
