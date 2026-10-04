# quota — 磁盘配额预留库

先按预估写入容量预留，压缩完成后按实际大小结算；两级配额、幂等操作、
共享块单归属计费、事件账本复算峰值。

## 核心流程

```go
m := quota.NewManager(quota.Unlimited)   // 全局配额
m.SetTenantQuota("tenant-a", 1<<30)      // 租户配额

r, _ := m.Reserve("req-1", "tenant-a", 100<<20) // 1. 预留预估容量
// ... 压缩 ...
r, err := m.Settle("req-1", 60<<20, "blk-123")  // 2a. 按实际大小结算并发布块
// 或压缩失败：m.Abort("req-1")                  // 2b. 中止并释放预留
// 或主动取消：m.Cancel("req-1")
```

## 设计要点

- **两级配额**：预留与结算都同时校验租户配额与全局配额（`quota/manager.go`）。
- **幂等**：`Reserve`/`Settle`/`Cancel`/`Abort`/`TransferOwnership` 均以业务 ID 幂等；
  重复回执返回首次结果，参数不一致返回 `ErrReservationExists`/`ErrSettleConflict`。
- **结算超额不发布**：结算时按实际计费字节重新校验配额（限额可能已调低），
  超额则预留单转 `rejected`、预留量释放、不建块不计费。
- **共享物理块**：`Settle` 命中已有块时计费为 0，只向块归属方计费；
  `TransferOwnership` 在同一临界区内完成校验、计费转移与账本记录，原子生效。
- **事件账本**：每次状态变更追加一条 `Event`；`RecomputePeaks` 回放账本复算
  全局/租户峰值（已结算 + 在途预留），`VerifyAgainstLedger` 校验实时态与账本一致。

## 测试

```sh
go test ./quota/ -race -count=1
```

覆盖：压缩失败中止、重复/冲突回执、租户与全局限额调低后结算拒绝、
共享块归属计费、归属迁移原子性、峰值复算与并发一致性。
