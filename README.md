# Binder 事务缓冲配额与偏移校验

Android 每个进程的 Binder 映射缓冲约 1MB，单向事务只能使用其中一半。本包在事务入口校验偏移数组，并在事务完成前持续计入待处理缓冲：

- 所有单向事务合计不超过 `B/2`。
- 同一调用方待处理事务合计不超过 `B/4`。
- 配额检查失败不会预留空间，超限调用方不会挤占其他人的额度。
- 事务占用为 `dataSize + 8 * offsetCount`，再向上取整到 8 字节。
- 偏移必须 4 字节对齐且严格递增。
- 每个 `flat_binder_object` 固定占 24 字节，必须完全位于数据内且对象区间不重叠。

## 使用

```go
checker := netsec.New(1 << 20) // B

reservation, err := checker.Admit(callerPID, transactionID, data, offsets)
if err != nil {
    // 拒绝该调用方的事务；不要改动其他调用方的待处理账本。
    return err
}

// 服务框架在事务完成事件到达时调用，重复事件返回 false。
checker.Complete(callerPID, transactionID)

// 或者使用准入令牌释放：
fmt.Println(reservation.Size())
reservation.Release()
```

`Admit` 假定 `data` 和 `offsets` 是服务端当前解析的不可变快照。校验只读取，不复制或保留事务内容；准入令牌只保存调用方、事务 ID 和计费字节数。

常用接口：

- `New(B)` / `NewChecker(B)`：创建并发安全的检查器。
- `Admit(caller, transactionID, data, offsets)`：校验并预留额度。
- `Complete(caller, transactionID)`：按完成事件释放。
- `Release(reservation)` / `reservation.Release()`：按令牌释放。
- `TransactionSize(dataLength, offsetCount)`：计算向上取整后的占用。
- `Validate(data, offsets)`：只做偏移和对象区间校验。
- `PendingBytes()` / `PendingBytesFor(caller)`：读取当前账本。

## 测试

```sh
go test ./...
```
