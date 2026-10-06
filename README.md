# scrubber

Go 对象巡检库：读取时捕获版本，校验完成后仅标记该版本；对象已更新则结果过期，绝不隔离新对象。

## 设计

- `Backend`（`scrubber/backend.go`）：版本化对象存储。`MarkHealthyIfVersion` /
  `QuarantineIfVersion` 将**版本比较与状态变更原子执行**，版本不匹配返回
  `ErrStaleVersion`，对象已删除返回 `ErrNotFound`，两者都不产生任何副作用。
- `Inspector`（`scrubber/inspector.go`）：`Get` 捕获版本 → `Verifier` 校验 →
  条件标记。更新交错时返回 `Stale`，删除交错时返回 `Gone`，已隔离返回
  `AlreadyQuarantined`。
- `MemoryBackend`（`scrubber/memory.go`）：内存实现，比较与标记在同一把锁内完成。
- `PausableBackend`（`scrubber/pausable.go`）：测试用包装后端。`ArmPause` 让下一次
  `Get` 在底层读取返回后、调用方继续标记前冻结，从而精确交错更新、删除与重复巡检。

## 测试

```sh
go test -race ./...
```

`scrubber/inspector_test.go` 覆盖：健康标记、损坏隔离、读取后更新（结果过期、
新版本不受隔离）、读取后删除（不复活对象）、同版本重复巡检交错、隔离后重新
写入从干净状态开始。
