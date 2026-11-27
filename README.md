# 页级 FTL 与掉电恢复包

`prescreen.local/ps-73dfc2e0b1` 提供一个可直接导入的 Go 页级闪存转换层（FTL）模型，用于存储仿真、块设备固件验证和掉电一致性测试。包名为 `ftl`。

## 快速开始

```go
dev, err := ftl.New(ftl.Config{
    BlockCount:         16,
    PagesPerBlock:      64,
    PageSize:           4096,
    ReservedFreeBlocks: 2,  // Over-Provisioning 预留块
    WearLevelThreshold: 2,  // 最大允许擦除次数差
})
if err != nil {
    panic(err)
}

_ = dev.Write(7, []byte("page data"))
value, _ := dev.Read(7)
_ = dev.Trim(7, 1)

_ = dev.Checkpoint()
dev.PowerLoss()             // 丢弃全部易失 RAM 状态
_ = dev.Recover()           // 从检查点 + OOB + TRIM 日志精确重建
```

`Write`/`Trim`/`Checkpoint` 成功返回即代表对应记录已经持久化；`PowerCycle` 等价于 `PowerLoss` 后 `Recover`。

## 设计

### 1. 物理 Flash 约束

`Flash` 只拥有物理页和擦除计数，强制以下硬件不变量：

- 块内页只能按 `0 .. P-1` 严格顺序编程，跳页、回写、重复编程都会被拒绝；
- 编程前块必须已擦除，擦除只能整块进行并递增 `EraseCount`；
- 页编程写入数据、OOB 元数据和 CRC32C 校验；
- `ProgramPartial`/`InjectOOBFlip` 可注入页编程撕裂或坏校验页，撕裂页会被物理写指针消费，但恢复时不会进入 L2P。

错误包括 `ErrNotSequential`、`ErrNotErased`、`ErrBlockFull`、`ErrOutOfRange`、`ErrProgramTorn`。

### 2. OOB 元数据与 TRIM 日志

每个物理页的 OOB 头包含 `LBA`、单调递增 `seq`、有效长度、物理块/页号和 CRC32C。恢复扫描只接受校验完整的页。

`Trim(start, count)` 会先把 CRC 保护的 `TrimRecord` 追加到持久化日志，再解除 RAM 中的映射并把旧 PPA 从有效位图移除。因此：

- 读已 TRIM 的 LBA 返回确定性的全零页；
- 旧物理页即使仍残留原 LBA 的 OOB，也会被 `seq` 更大的 TRIM 墓碑杀掉，不会“僵尸复活”；
- 日志记录本身按编码字节保存，恢复时重新做 CRC 校验。

### 3. 检查点与恢复

`MetaArea` 有两个交替的检查点槽。新检查点完整写入后才会被下一次恢复采纳；旧槽保留为回退。日志只裁剪到“较旧的有效检查点”之前，因此最新槽即使被发现撕裂，也能用旧检查点加日志重做恢复。

`Recover` 的步骤固定为：

1. 选择 `seq` 最大的完整检查点，加载 L2P 与 TRIM 墓碑；
2. 扫描全部物理块的 OOB，丢弃 CRC 损坏的半写页；
3. 收集 `seq > checkpoint.seq` 的数据页和 TRIM 记录，按 `seq` 升序合并；
4. 重建 `LBA -> PPA`、每块有效页位图、每块顺序写指针、擦除计数和下一个 `seq`；
5. 用 `Verify` 校验映射唯一性、有效页数量、写指针和物理页状态守恒。

### 4. GC 与磨损均衡

- 当可用块数触及 `ReservedFreeBlocks` 水位时触发 GC；始终保留至少一个可擦除目标块，避免 GC 死锁；
- 常规阶段采用贪心，优先回收无效页最多的块以降低写放大；
- 当最热块与最冷块的擦除次数差超过 `WearLevelThreshold` 时，优先搬迁静态冷块（低擦除次数、旧写入 `seq`）；
- GC 先把每个有效页以更大的 `seq` 复制到目标块，全部完成后才擦除源块；
- GC 中途掉电时，源块和已完成/半完成的目标页同时存在，恢复按 `seq` 取最新完整版本；撕裂的复制页被丢弃，源页仍然有效。

## 验收测试

运行：

```bash
go test ./... -count=1
go test -race ./...
```

测试覆盖：

- `TestFlashHardwareConstraints`：顺序编程、擦除、擦除计数；
- `TestTrimPersistsAndPreventsZombieResurrection`：TRIM 后掉电不复活旧页；
- `TestCheckpointFallbackWithRetainedTrimRedo`：检查点撕裂回退且 TRIM 重做不丢失；
- `TestSparseLargeTrimRange`：超大稀疏 TRIM 范围不遍历整个 LBA 命名空间；
- `TestTornPageIsDiscardedAndWritePointerAdvances`：撕裂页精确丢弃、写指针守恒；
- `TestTornOverwriteRollsBackOnlyToOlderCompletePage`：坏新版本只回退到上一个完整版本；
- `TestGCMidMigrationTearLosesNoDataAndDoesNotRollBack`：GC 搬迁中途掉电不丢页、不回退；
- `TestGCPowerOffAfterCompletedCopiesKeepsNewestSeq`：GC 完整复制后掉电按 `seq` 取新版本；
- `TestGCReserveNeverDeadlocks`：持续热写不跌破预留块水位；
- `TestRecoveryReconstructsExactMappingBitmapsAndPointers`：L2P、位图、写指针、擦除计数精确重建；
- `TestWearLevelingConvergesUnderHotColdSkew`：热冷负载下磨损差收敛；
- `TestRandomWorkloadMatchesReferenceModelAcrossRecovery`：随机负载与参考模型逐步对照；
- `TestPhysicalPageStateConservation`：物理页状态守恒。

## 主要 API

| API | 说明 |
| --- | --- |
| `New(Config)` / `NewDevice` / `NewFTL` | 创建新设备 |
| `Open(flash, meta, cfg)` | 挂载已有持久化介质并恢复 |
| `Read(lba)` / `ReadInto` / `ReadLBA` | 读取；未映射返回全零页 |
| `Write` / `WritePage` / `WriteLBA` | 追加写并异地失效旧 PPA |
| `Trim` / `Deallocate` / `Discard` / `Unmap` | 持久化解除映射 |
| `Checkpoint` / `Flush` | 发布完整检查点 |
| `PowerLoss` / `Crash` / `Recover` / `PowerCycle` | 掉电与恢复 |
| `GCOnce` / `CollectGarbage` / `WearLeveling` | 主动 GC 与磨损均衡 |
| `Verify` / `Mapping` / `ValidBitmap` / `PageState` | 自检与状态观测 |
| `SetProgramHook` | 注入用户写或 GC 搬迁的撕裂/坏校验/掉电故障 |

## 默认值与约定

- LBA 类型为 `int`，负值非法；`PageSize` 是物理页数据容量。
- 未设置 `PageSize`/几何参数时使用 `DefaultConfig` 的默认值。
- `ReservedFreeBlocks` 至少为 1，因此 N 个物理块的有效用户容量约为 N-1 到 N-reserve 个块。
- 检查点与 TRIM 日志位于独立持久化元数据区，不占用数据块；这与真实 SSD 的控制器元数据区对应。
- 包内所有状态变更由互斥锁保护，可在测试中并发调用。
