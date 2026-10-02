# lsm — WAL 与 MemTable 刷盘分层转换器

LSM 树内存表向 SSTable 持久化转换的状态机实现（Go，仅标准库）。

## 状态机

```
MemTable (active, 可变, 接收写入)
   │  size >= 4 MiB 阈值
   ▼  冻结 (O(1) 指针交换 + WAL 轮转, 前台不停顿)
Immutable MemTable (FIFO 队列)  +  新 MemTable 接管写入  +  新 WAL 段
   │  后台单线程 flusher 严格按 FIFO 顺序刷盘
   ▼
Level-0 SSTable 文件 (L0-000001.sst, ...)
   │  文件 fsync 落盘后
   ▼
配对 WAL 段被安全删除 (wal-00000N.log)
```

关键不变式：

- **先写 WAL 再写 MemTable**：`Put` 在同一把写锁内完成 WAL 追加与跳表插入，
  保证每条 MemTable 记录都有对应的 WAL 记录。
- **MemTable 与 WAL 段 1:1 配对**：WAL 段只有在配对的 MemTable 刷盘
  并 fsync 完成后才被删除，崩溃恢复（重放所有 WAL 段）不丢数据。
- **前台无停顿**：冻结只做指针交换与 WAL 段切换；刷盘完全由后台单
  goroutine 顺序执行，测试中用可阻塞的 hook 确定性验证了这一点。

## 组件

| 文件 | 职责 |
|---|---|
| `skiplist.go` | 有序跳表（MemTable 的存储结构） |
| `memtable.go` | MemTable：阈值统计、冻结单元 |
| `wal.go` | WAL 段写入（CRC 校验记录）、重放、撕裂尾部截断 |
| `sstable.go` | L0 SSTable 写盘（数据块 + 稀疏索引 + footer）与读取 |
| `db.go` | 状态机编排：轮转、后台单线程顺序刷盘、WAL 安全轮转、恢复 |
| `cmd/lsmdemo/main.go` | 运行样例 |
| `db_test.go` | 验收测试 |

## 运行样例

```sh
go run ./cmd/lsmdemo
```

实际输出（10000 条 1 KiB 写入，4 MiB 阈值）：

```
opened db in /tmp/lsmdemo-... (MemTable threshold = 4 MiB)
writing 10000 entries of 1 KiB ...
  [rotate] MemTable#1 frozen at 4.00 MiB -> immutable queue=1, new WAL segment wal-000002.log
  [flush] MemTable#1 -> L0-000001.sst (3961 entries, L0), WAL segment wal-000001.log safely removed
  [rotate] MemTable#3 frozen at 4.00 MiB -> immutable queue=1, new WAL segment wal-000003.log
  [flush] MemTable#3 -> L0-000002.sst (3961 entries, L0), WAL segment wal-000002.log safely removed
writes finished in 395.5ms (25283 ops/s, no foreground stall)
state: rotations=2 flushes=2 queuedImmutables=0 L0 SSTables=2 activeMemTable=2.10 MiB (2078 entries)
verifying all keys ...
verified 10000/10000 keys: OK
files on disk: L0-000001.sst L0-000002.sst wal-000003.log
  [rotate] MemTable#4 frozen at 2.10 MiB -> immutable queue=1, new WAL segment wal-000004.log
  [flush] MemTable#4 -> L0-000003.sst (2078 entries, L0), WAL segment wal-000003.log safely removed
closed cleanly (remaining MemTable flushed to L0)
```

## 测试结果

```sh
go test -v ./...
```

```
=== RUN   TestRotationAndSequentialFlush
--- PASS: TestRotationAndSequentialFlush (1.57s)
=== RUN   TestForegroundWritesDoNotStall
--- PASS: TestForegroundWritesDoNotStall (0.12s)
=== RUN   TestCrashRecovery
--- PASS: TestCrashRecovery (0.11s)
=== RUN   TestOverwriteAcrossComponents
--- PASS: TestOverwriteAcrossComponents (0.05s)
PASS
ok  	lsm	2.165s
```

测试与验收边界的对应关系：

- `TestRotationAndSequentialFlush`：高频写入两次触发 4 MiB 阈值 → 恰好
  2 次轮转、2 个不可变内存表顺序刷入 L0（`L0-000001/2.sst`）；WAL 安全
  轮转（旧段删除，仅剩当前段 `wal-000003.log`）；9000 个键全部读回校验，
  并直接解析 SSTable 文件验证有序性与值正确性；Close 后重开数据仍在。
- `TestForegroundWritesDoNotStall`：用 hook 阻塞后台 flusher，验证前台
  写入（含再次触发轮转）在其阻塞期间照常完成。
- `TestCrashRecovery`：模拟崩溃（WAL 已同步、未刷盘），重开后 WAL 重放
  恢复全部数据，且可继续写入。
- `TestOverwriteAcrossComponents`：MemTable 中的新值正确遮蔽已刷入 L0
  的旧值。
