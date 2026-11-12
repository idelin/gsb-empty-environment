# 持久内存区域认证擦除生命周期

`netsec` 为 Go 存储管理服务提供持久内存区域的分配、租约释放、认证擦除与断电恢复。默认使用目录内的追加式擦除日志和每区域一个后端文件；测试可注入 `MemDevice` 或自定义 `Device`。

## 安全不变量

- 区域只有三种持久状态：`StateInUse`（在用）、`StateErasing`（擦除中）、`StateClean`（已清洁）。
- `Allocate` 只接受 `StateClean`；在用和擦除中的区域一律返回 `ErrNotAllocatable`。
- 擦除必须先通过该区域凭据认证。凭据只以区域绑定的 SHA-256（或 `WithCredentialKey` 的 HMAC）形式落盘，并做常量时间比较。
- 连续认证失败达到 `WithMaxAuthFailures(n)`（默认 3）后，该区域进入易失性锁定，正确凭据也不再接受，直到进程重启（重新打开 `Manager`）。
- `CreateRegion` 先把区域记录为擦除中并做初始净化，完成逐条带校验后才变为已清洁；因此新区域也不会暴露后端残留数据。
- 擦除日志每条记录先 `fsync`，再推进内存状态。断电后日志仍为擦除中，`Recover` 会继续。
- 每条 1 MiB 条带必须完成写入、读回比对和日志检查点。最终清洁门还会要求全部条带都有检查点并再次读回；任何遗漏都会保持 `StateErasing`。
- 若旧数据恰好已经等于目标图案，会先写入互补图案并校验，再写目标图案并校验，防止设备吞写后借“读回正确”蒙混过关。

## 基本用法

```go
m, err := netsec.NewManager("/var/lib/storage/regions")
if err != nil {
    return err
}
defer m.Close()

// 服务启动时恢复所有中断的擦除。
if err := m.Recover(); err != nil {
    return err
}

// 预置区域（内部完成初始净化）。
if err := m.CreateRegion("region-1", 64*netsec.StripeSize, "tenant-erase-secret"); err != nil {
    return err
}

lease, err := m.Allocate("region-1", "tenant-a")
if err != nil {
    return err
}

// 租约释放：认证后同步擦除；成功后区域才可再次分配。
if err := m.ReleaseLease(lease, "tenant-erase-secret"); err != nil {
    return err
}
```

需要分阶段擦除时可调用 `BeginErase`，之后用 `Erase`（需再次认证）、`ResumeErase` 或服务启动时的 `Recover` 完成。

## 断电与故障注入

`Faults` 提供 `BeforeStripe`、`AfterWrite`、`AfterVerify`、`BeforeMarkClean` 和 `BeforeRecover` 钩子；返回 `ErrPowerLoss` 可在确定点模拟断电。也可以在预置/分配完成后用 `SetFaults` 临时安装钩子，使断电只影响被测的擦除操作。钩子返回后管理器不会把未确认的条带记为清洁；重新打开同一日志即可验证区域仍为 `StateErasing`，随后 `Recover` 从检查点继续。

测试覆盖：

- 在条带写入前、写入后、读回后以及最终清洁前的四个断电点；
- 错误凭据、连续失败计数清零、锁定后正确凭据仍被拒绝、重启后解锁；
- 擦除中的区域不可分配，且重启后仍不可分配；
- 条带被设备吞写、短写、零值空白内存上的吞写；
- 日志尾部撕裂恢复、日志内部损坏拒绝、伪造“未校验即清洁”记录拒绝。

运行：

```sh
go test -race ./...
```
