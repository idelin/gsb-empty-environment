# waldb — 有限页数据库的 ARIES 风格 WAL 恢复器

一个教学级但完整的预写日志（WAL）恢复实现：严格两阶段独占页锁、
steal/no-force 缓冲池、分析/重做/撤销三阶段恢复、CLR、模糊 checkpoint，
以及带可注入持久化切点的文件适配器与独立检查器。

## 日志记录格式（小端）

```
Record := Len(4B) Body CRC32(4B, IEEE, 覆盖 Body)
Len    := len(Body) + 4            // 不含 Len 自身
Body   := LSN(8) PrevLSN(8) TxnID(8) Type(1) Payload
```

Payload 按类型：

| 类型       | Payload                                                |
|------------|--------------------------------------------------------|
| begin      | （空）                                                  |
| update     | PageID(4) Before(8) After(8)                           |
| commit     | （空）——持久化即提交成功                                 |
| abort      | （空）                                                  |
| CLR        | PageID(4) After(8) UndoNextLSN(8)   // 只重做，不撤销   |
| end        | （空）                                                  |
| checkpoint | NTxns(4) [TxnID(8) Status(1) LastLSN(8)]* NDPT(4) [PageID(4) RecLSN(8)]* |

- LSN 严格递增（顺序分配，崩溃后从最大 LSN+1 继续）。
- 不完整/校验失败的尾部记录在扫描时截断，恢复从最后一个完整记录继续。
- 页文件：`NumPages(32) * 16B`，第 i 页偏移 `i*16`：`PageLSN(8) Value(8)`。

## 关键规则

- **WAL 规则**：页落盘前，日志必须先持久化到该页的 pageLSN（`bufferPool.flushAll`）。
- **提交点**：commit 记录持久化才算提交成功；end 记录丢失不影响结果。
- **撤销**：每次撤销先写含 undoNextLSN 的 CLR，再改页；CLR 只重做、不再撤销。
- **checkpoint**：模糊（不强制刷页）、可只完成一半——撕裂的 checkpoint 被
  CRC 拒绝，恢复回退到更早的 checkpoint 或全日志扫描，恢复起点始终可用。
- **锁**：严格 2PL 独占页锁，提交/中止时才释放，杜绝脏写。

## 可注入的退出点（`CrashPoint`）

正常执行：`PtUpdateLogged` `PtPageDirtied` `PtCommitLogged` `PtCommitFlushed`
`PtEndLogged` `PtPageFlush` `PtCheckpoint` `PtAbortUndo`
恢复过程：`PtRecAnalysis` `PtRecRedo` `PtRecUndo` `PtRecEnd`

`Adapter` 通过「易失写覆盖层 + Sync 持久化边界 + Crash 丢弃」模拟崩溃，
`Truncator` 钩子可注入撕裂写（不完整尾记录、半个 checkpoint）。

## 独立检查器（`checker.go`）

不依赖恢复代码：从事务结果（持久化日志中的 commit 记录）与原始页值复算
期望页内容，并校验日志结构不变量（LSN 严格递增、CLR undoNextLSN 回指、
每个 pageLSN 都能在持久化日志中找到对应记录）。

## 测试

```
go test ./...        # 或 go test -race -v ./...
```

覆盖：每个执行/恢复退出点各崩一次、反复恢复幂等、CLR 已落盘但页未改、
页已改但 end 未写、不完整尾记录、半个 checkpoint、并发事务、崩溃风暴。
