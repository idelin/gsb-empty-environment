# walstore — 带日志的固定长页存储原型

本地页存储：每笔事务修改多个固定长页（64 字节，含 8 字节 pageLSN），
写入可能只完成前缀（撕裂写），未刷盘的数据可能丢失。

## 设计

- **WAL（`wal.go`）**：`[len][crc32][type][payload]` 记录，页镜像 redo +
  commit 记录。扫描时遇截断/撕裂/CRC 不符即截断，之后字节视为垃圾。
- **提交协议（`store.go` `Commit`）**：先追加数据记录 + commit 记录，
  再 `Sync` WAL（提交点），**确认（ack）一定晚于恢复所需信息落盘**；
  之后才把脏页写入页文件（no-steal，且刻意不刷盘）。
- **恢复（`store.go` `recover`）**：按 LSN 重做已提交事务的页镜像；
  页头 pageLSN 使重做幂等——**重复恢复零页写、状态不变**；
  未提交事务从未到达页文件，天然全有或全无。
- **可注入故障的文件接口（`file.go` `SimFile`）**：
  - `TearNextWrite(n)`：下一次写只落盘前 n 字节（短写/撕裂写）；
  - `Crash()`：丢弃所有未 `Sync` 的写（持久化丢失）；
  - `Tracer` 记录跨文件全局持久化事件序列，`ReplayPrefix` 可在任意
    切点重放并崩溃。

## 切点枚举核对（`store_test.go`）

三笔串行事务（页有重叠）：`T1: p0=A1 p1=B1`、`T2: p1=B2 p2=C2`、
`T3: p0=A3 p2=C3`，对应串行状态 S0..S3。

- `TestPersistenceCutPoints`：枚举轨迹上**每一个**持久化切点
  （含崩溃瞬间正在进行的写只落盘前缀的变体），断言恢复结果恰好是
  `S_i`（i = 切点前已完成 commit 刷盘的事务数），并断言
  **ack 一定出现在 commit 刷盘之后**、重复恢复幂等。
- `TestTornSyncedWALWrite`：枚举“WAL 写被撕裂且随后被 Sync 持久化”
  的切点，CRC 截断日志，已落盘部分仍是合法串行前缀，绝不出现半笔事务。
- `TestFaultInjection` / `TestUncommittedTxnNeverReachesDisk`：
  短写注入、持久化丢失、未提交事务不外泄。

## 运行

```sh
go test ./...    # 切点枚举 + 故障注入测试
go run ./cmd/demo  # 打印每个切点崩溃恢复后的串行状态表
```

## 已知边界（原型取舍）

- redo-only + no-steal：未提交事务不写页文件，无需 undo；
  代价是提交前脏页全部缓存在内存。
- 与所有 CRC 型 WAL 一样：若已 Sync 的 WAL 写被存储层**静默撕裂**，
  恢复会在撕裂处截断，保证原子性与串行序，但该笔已确认事务可能丢失
  （真实系统靠扇区原子写/多副本规避）。测试对这种情况断言的是
  “恢复结果仍是合法串行前缀”。
- 原型不截断 WAL（checkpoint 只刷页文件），恢复靠 pageLSN 幂等跳过。
