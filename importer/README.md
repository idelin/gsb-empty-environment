# importer：可恢复的记录导入器

从源文件按记录边界读取记录，分批提交到目标端（Sink），支持崩溃恢复。

## 检查点

每批提交确认后原子持久化三元组（`checkpoint.go`）：

- `Offset`：已提交到的源文件记录边界字节偏移
- `PrefixDigest`：源文件 `[0, Offset)` 前缀的 SHA-256 摘要
- `Batch`：目标端已确认提交的批号

三者缺一不可——只保存偏移的检查点会被 `ErrInvalidCheckpoint` 拒绝。

## 恢复核对（`Importer.recover`）

1. 加载并校验检查点完整性；
2. 重算源文件前缀摘要比对，不符则 `ErrSourceModified`，明确停止；
3. 与目标端批号对账：
   - 一致：从检查点偏移续传；
   - 目标端超前（回执丢失）：逐批校验目标端保存的偏移链与摘要，
     全部吻合则快进续传，任一不符则 `ErrReconcileFailed`；
   - 目标端落后：`ErrTargetBehind`。

任一核对失败都不会导入任何数据。

## 测试

```sh
go test ./...
```

- `TestTailAppendResume`：源尾追加后续传，不漏不重
- `TestSourceMiddleModified`：源中段修改后恢复明确停止
- `TestReceiptLost`：提交回执丢失后对账快进，不漏不重
- `TestOffsetOnlyCheckpointRejected`：只存偏移不算有效恢复
- `TestTargetBehindCheckpoint`：目标端落后于检查点明确停止
