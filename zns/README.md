# Go NVMe ZNS 状态机与主机侧冷热分层 GC 引擎

本目录提供两个可导入的 Go 包，在无真实硬件的情况下精确模拟 NVMe Zoned
Namespaces 协议，并在其上实现主机 FTL（逻辑块映射、冷热分层追加、原子 GC）。

- `zns/device`：设备级 ZNS 状态机（七态、写指针、MOR/MAR 槽位、`ZoneAppend`）。
- `zns/engine`：主机侧存储引擎（`LBA -> (zoneID, offset)` 映射、无效块统计、
  冷热路由、最低有效块率 victim 选择、CAS 原子迁移、`ZoneReset` 安全回收、WAF）。

## 协议约束的实现位置

### 1. Zone 七态生命周期与严格写指针

状态枚举见 `device/zone.go`：`EMPTY`、`IMPLICITLY_OPEN`、`EXPLICITLY_OPEN`、
`CLOSED`、`FULL`、`READ_ONLY`、`OFFLINE`。每个 Zone 持有 `SLBA`、`ZoneSize`、
`ZoneCapacity`（强制 `0 < ZoneCapacity <= ZoneSize`）和绝对 LBA 形式的
`WritePointer`。

- 普通 `Write` 在任何状态变更前完成校验：起始 LBA 必须严格等于 `WritePointer`，
  且 `lba+blocks <= SLBA+ZoneCapacity`。注意可写区域止于 `SLBA+ZoneCapacity`
  而非 `SLBA+ZoneSize`，违规统一返回 `ErrInvalidZoneWrite`。
- `ZoneAppend` 在同一把设备锁内分配当前写指针位置、写入并原子返回分配到的
  起始 LBA，再推进写指针。
- 写到容量边界自动转 `FULL`，同时释放 Open 与 Active 槽位（`device/write.go`）。

### 2. MOR / MAR 硬件槽位约束

计数规则（`device/device.go`）：

- Open 槽位 = `IMPLICITLY_OPEN + EXPLICITLY_OPEN`。
- Active 槽位 = Open + `CLOSED`。
- 限制值 `0` 表示无约束（对应 NVMe 的 “no limit” 语义）。

当 Open 槽位已满需要新槽位时，`ensureOpenSlotLocked` 只自动驱逐（关闭）**一个
最低 id 的 `IMPLICITLY_OPEN` 区**；若全部是 `EXPLICITLY_OPEN` 区则返回
`ErrTooManyOpenZones`，显式打开的区受保护。激活新 `EMPTY` 区前若 Active 槽位
已满则返回 `ErrTooManyActiveZones`；`ZoneFinish` 强制转 `FULL` 以释放 Active
槽位，`ZoneReset` 回到 `EMPTY`。

### 3. 逻辑地址映射与冷热分层追加

`engine.Engine` 维护：

- `mapping map[int64]Loc`：逻辑块到物理位置 `(zoneID, offset, heat)` 的映射。
- `zoneOwners map[int]map[int64]struct{}`：每个 Zone 当前仍有效的逻辑块集合，
  即无效块位图的活块补集，用于 victim 有效块占比计算与 reset 前安全门。
- 每个热级别一个 `zonePool`：`ModeTiered` 下高频覆写块（覆写次数达到
  `AutoHotThreshold`）进入 HOT 池，写一次的冷块进入 COLD 池；`ModeMixed` 下
  所有块共用一个池，用于对照冷热混写的写放大。

覆写通过 `ZoneAppend` 追加新版本并原子替换映射，旧物理位置立即从 owner 集合
移除（失效），等所在 Zone 被整体 reset 时才真正擦除。

### 4. 原子 GC 与 ZoneReset 安全回收

`Engine.GC`（`engine/gc.go`）流程：

1. `pickVictimLocked` 选择有效块占比最低的 `FULL` 区（也可通过 hint 指定），
   无 `FULL` 区时返回 `ErrNothingToGC`。
2. 快照该区当前存活的逻辑块；逐块重新校验映射是否仍指向 victim。
3. 通过 `ZoneAppend` 迁移到按热度选择的目标 GC Zone，随后对映射做**比较交换
   （CAS）**：只有逻辑块仍指向原 victim 位置时才安装新位置，否则丢弃迁移结果、
   计入 `GCBlocksStale`，绝不用旧版本覆盖并发覆写产生的新版本。
4. 安全门确认 victim 的 owner 集合归零后才调用 `Device.Reset` 回到 `EMPTY`，
   否则返回 `ErrGCInvariant` 且不 reset。

WAF 由 `GCStats.WAF()` 计算：`FlashWrites / HostWrites`，其中 `FlashWrites`
同时包含主机追加写与 GC 迁移写。

## 快速上手

```go
dev, _ := device.New(device.Config{
    NumZones: 6, ZoneSize: 8, ZoneCapacity: 6,
    MaxOpenZones: 2, MaxActiveZones: 4,
})
eng, _ := engine.New(engine.Config{Device: dev, Mode: engine.ModeTiered})

loc, err := eng.WriteWithGC(42, []byte("payload")) // 自动路由 + 必要时 GC
payload, err := eng.Read(42)
run, ok, err := eng.GCOnce(-1)                      // 回收最低有效块率的 FULL 区
```

## 测试与验收映射

运行：`go test -race ./...`

| 验收点 | 测试 |
| --- | --- |
| `ZoneCapacity < ZoneSize` 边界、错位写拦截、`ZoneAppend` | `device.TestZoneCapacityBoundaryAndMisalignedWrites` |
| MOR 满时仅驱逐 IMPLICITLY_OPEN、保护 EXPLICITLY_OPEN | `device.TestMORImplicitEvictionProtectsExplicit` |
| MAR 槽位记账、Finish/Reset 释放 | `device.TestMARActiveAccounting` |
| 七态生命周期与 RO/OFFLINE 拦截 | `device.TestSevenStateLifecycle` |
| 并发 `ZoneAppend` 原子分配 | `device.TestConcurrentZoneAppend` |
| 并发覆写期间 GC 的 CAS 不覆盖新版本 | `engine.TestGCAtomicCASDoesNotOverwriteNewVersion`、`engine.TestGCConcurrentOverwriteRace` |
| victim 选择与 reset 前安全门 | `engine.TestGCSafetyGateAndVictimSelection` |
| 冷热分离相对混写显著降低 WAF | `engine.TestTieredWAFOutperformsMixed`、`engine.TestTieredRoutingSeparatesHeat` |

当前参考结果：同一覆写密集负载下分层引擎 WAF 约 1.06，混写引擎约 1.49
（改善约 29%），且两种模式主机写字节数完全相同。
