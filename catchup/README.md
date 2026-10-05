# catchup — 快照追赶模拟器

模拟 Leader 向落后副本分块传输快照、期间日志继续追加的追赶过程。

## 设计

- **快照代次（generation）**：每次 `Leader.Snapshot()` 递增代次。Follower 收到更高代次的块时丢弃旧代次暂存进度；旧代次的迟到块与已安装代次的块一律忽略。
- **块校验**：每块携带 CRC32 校验和，与 `SnapshotMeta.ChunkChecksums` 双重核对；安装前再校验拼装后完整数据的 `DataChecksum`。校验失败的块被拒绝且不污染暂存区。
- **安装边界**：`ReceiveChunk` 只负责暂存落盘（`MemStore` 模拟持久化）；`Install` 在全部块收齐且整体校验一致时，才原子替换状态、截断快照覆盖的日志并清空暂存区，否则拒绝安装、状态不变。
- **断点恢复**：暂存进度持久化，重启后 `Recover()` —— 已收齐则直接完成安装（覆盖安装中断场景），未收齐则保留进度等待补传。
- **增量重放**：安装后 `Replay` 只应用 `LastIncludedIndex` 之后且未应用过的日志条目，重复重放幂等。

## 测试

```
go test ./...
```

- `TestSnapshotGenerationChange` — 快照换代：gen1 传输途中改传 gen2，旧块被丢弃/忽略
- `TestDuplicateLastChunk` — 最后一块重复投递幂等，状态只安装一次
- `TestInstallInterruptedRecovery` — 全部块落盘后崩溃，重启恢复安装并只重放快照后日志
- `TestRecoverPartialStaging` — 部分暂存崩溃后保留进度、补传剩余块
- `TestChecksumAndInstallBoundary` — 损坏块拒绝、未收齐拒绝安装
