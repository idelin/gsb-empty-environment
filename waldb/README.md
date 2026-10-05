# waldb — 有限页数据库的预写日志恢复器

ARIES 风格恢复：分析（事务表 + 脏页表）→ 按 pageLSN 重做历史 → 反向撤销未提交事务。
事务采用严格两阶段独占页锁（锁持有到提交/回滚），避免脏写；缓冲池允许 steal，
页落盘前对应日志必须已持久化（WAL）。提交只有在 commit 记录持久化后才算成功。

## 日志格式

日志为记录序列，**LSN 即记录在文件中的字节偏移，严格递增**。每条记录：

```
+----------------+------------------------------+----------------+-----------+
| header (25B)   | payload (0..N)               | CRC32 (4B)     |           |
+----------------+------------------------------+----------------+-----------+

header:
  type      u8    记录类型
  lsn       u64   本记录 LSN（== 文件偏移，解码时校验，防御错位垃圾）
  prevLSN   i64   同事务上一条记录的 LSN（-1 表示 NilLSN）
  txnID     u64   事务 ID（检查点记录为 0）
  payloadLen u32  payload 字节数
crc: CRC32-IEEE(header || payload)，用于识别撕裂写/不完整尾记录
```

记录类型与 payload：

| type | 名称             | payload                                                        |
|------|------------------|----------------------------------------------------------------|
| 1    | begin            | 无                                                             |
| 2    | update           | pageID u32, before u64, after u64                              |
| 3    | commit           | 无（持久化即提交成功）                                          |
| 4    | abort            | 无                                                             |
| 5    | CLR              | pageID u32, before u64, after u64, undoNextLSN i64             |
| 6    | end              | 无                                                             |
| 7    | checkpoint_begin | 无                                                             |
| 8    | checkpoint_end   | JSON：{tt: 事务表快照, dpt: 脏页表 pageID→recLSN}               |

CLR 的 before/after 相对被撤销的 update 而言：after = 原 update 的 before（撤销结果），
undoNextLSN 指向撤销链上下一个待撤销记录。CLR 只重做、永不撤销。

页格式（PageSize = 16B）：`pageLSN u64 | value u64`，页数固定。

## 恢复

1. **分析**：正向扫描，重建事务表与脏页表；遇到完整 `checkpoint_end` 用其快照替换。
   不完整尾记录（撕裂写）由长度/CRC 校验识别并截断；只完成一部分的检查点
   （begin 持久化、end 缺失或残缺）自动回退到上一个完整检查点或日志起点。
2. **重做**：从脏页表最小 recLSN 起扫描，对 update/CLR，若页在脏页表且
   `pageLSN < 记录 LSN` 则应用 after 镜像（幂等，由 pageLSN 保证）。
3. **撤销**：按 LSN 从大到小全局回退所有输家事务；每步先写 CLR（含 undoNextLSN）
   并持久化，再改页；链尽写 END。重启后沿 undoNextLSN 跳过已撤销记录，不会重复撤销。

## 可注入持久化切点（CrashPoint）

正常执行：`log-buffered`、`commit-durable`、`before-end`、`page-flush`、
`checkpoint-partial`；恢复过程：`recovery-analysis`、`recovery-redo`、
`recovery-clr`（CLR 已落盘页未改）、`recovery-undo-page`（页已改 END 未写）、
`recovery-end`。`InjectedFile` 的写先落易失映像，`Sync` 才持久，`Crash` 丢弃未同步内容。

## 测试

```sh
GOCACHE=/tmp/gocache go test -v ./...
```

覆盖：正常执行、每个正常/恢复切点崩溃后恢复、CLR 已落盘但页未改、页已改但 END 未写、
不完整尾记录、部分检查点、反复恢复幂等、严格 2PL 阻塞语义。独立检查器
（`checker.go`）不复用恢复逻辑，仅凭原始页值与已提交事务复算期望页值，
并交叉校验每页 pageLSN 指向日志中一条 after 镜像一致的 update/CLR。
