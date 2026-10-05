# quota — 磁盘配额预留库

先预约写入容量，压缩完成后按实际大小结算。Go 实现，并发安全，无外部依赖。

## 核心流程

```go
s := quota.NewStore(1 << 40)          // 全局配额 1TiB
s.SetTenantLimit("tenant-a", 1<<30)   // 租户配额 1GiB

// 1. 写入前预约（压缩前估值）
res, _ := s.Reserve("req-123", "tenant-a", 100<<20)

// 2a. 压缩成功：按实际物理块结算，超额则拒绝发布
err := s.Settle("receipt-123", res.ID, blocks, func() error {
    return cdn.Publish(file) // 仅记账成功后调用
})

// 2b. 压缩失败 / 主动取消：释放预留
s.Fail("fail-123", res.ID, "zstd: corrupt input")
s.Cancel("cancel-123", res.ID)
```

## 语义保证

- **两级配额**：租户级 + 全局级，预留与结算都校验。
- **幂等**：`Reserve` / `Settle` / `Cancel` / `Fail` / `TransferOwnership`
  均携带幂等键，重复回执返回首次结果，不产生重复副作用；
  对已释放的预留单重复取消/失败标记为成功空操作。
- **结算超额不发布**：释放预留、不记账、不登记块、不调用 publish，
  返回 `ErrTenantLimitExceeded` / `ErrGlobalLimitExceeded`。
- **共享块单次计费**：已存在的物理块不重复计费，只计其归属租户；
  新块归属结算租户。
- **归属迁移原子**：`TransferOwnership` 在单锁内完成
  「校验目标限额 → 源减 → 目标加 → 改归属」，无中间态。
- **事件账本**：全部变更追加到只增账本，`ReplayPeak()` 重放复算
  租户级与全局级历史峰值（预留+已用）。
- **限额调低**：存量不强制回收，但此后新增预留/结算按新限额拒绝。

## 注意

- `Settle` 的 `publish` 回调在锁内执行，不得在回调中再调用 `Store` 方法。
- `publish` 失败不回滚已提交的计费（物理字节已落盘），该错误会被幂等缓存。

## 测试

```sh
go test ./...
```

覆盖：压缩失败释放、重复回执、租户限额调低、结算超额拒发布、
共享块计费、归属迁移原子性与幂等、账本峰值复算。
