# 虚拟设备 DMA 隔离（IOMMU 模拟）

`netsec` 是一个纯软件、可导入的 Go 包，为设备沙箱模拟 IOMMU 地址翻译，
不访问任何真实硬件、DMA 引擎或平台 IOV 表。模块路径为
`example.org/netsec/q-024`，包名 `netsec`。

## 核心模型

- 可信 `Host` 持有物理页归属表：每个 4096 字节物理页最多归属一个租户，
  归属只能通过 `Host.Assign` 修改，设备侧翻译 API 无法触碰该表。
- `IOMMU` 为每个设备创建独立 `Domain`，域内有独立页表与单调递增的
  代次（generation）；任何映射变更（Map/Remap/Unmap/Revoke）都会推进代次，
  使旧翻译失效。
- 映射携带 `PermRead` / `PermWrite` 权限。
- 设备攻击者可控制设备 ID、IOVA、长度和散布聚集（scatter-gather）列表，
  但无法修改宿主归属表。

## 传输与租约

- `IOMMU.Transfer`（或先 `Acquire` 后自行回调）先在持锁状态下验证整张
  列表，再按 4096 字节跨页拆分。
- 任一页缺失（`ErrUnmapped`）、越权（`ErrPermission`）、地址溢出
  （`ErrOverflow`）、超限（`ErrTooLarge`）、混租户（`ErrMixedTenant`）
  或归属表不一致（`ErrOwnerMismatch`）都会整体失败，且不触发任何内存回调。
- 验证通过后原子取得覆盖全部页面的 `Lease`。重映射/撤销推进代次只会让
  新请求失效；已持有的租约仍按旧代次完成传输，直到 `Release`。
- `Domain.Revoke` 清空页表、推进代次，并阻塞等待撤销前取得的所有租约
  释放后才返回。
- 内存回调 `MemCallback` 在所有包锁之外执行；回调失败会包装
  `ErrCallback` 返回并保证租约被释放。

## 主要 API

- `NewHost` / `Host.Assign` / `Host.OwnerOf`
- `NewIOMMU` / `IOMMU.NewDomain`
- `Domain.Map` / `Remap` / `Unmap` / `Revoke` / `Generation` / `ActiveLeases`
- `IOMMU.Acquire`（返回 `*Lease`）/ `IOMMU.Transfer`（校验+回调+释放）
- `Lease.Operations` / `Generation` / `Release`（可重复调用）

## 测试

测试均为确定性或有界并发，不依赖硬件：

- `TestRevokeWaitsForOutstandingLease`、`TestRemapInvalidatesOldGeneration`：
  撤销/重映射与在途传输的代次和租约竞态。
- `TestConcurrentRemapRevokeVersusTransfers`、
  `TestConcurrentAcquireReleasesWithRevoke`：可控并发压力，配合 `-race`
  检查数据竞争与租约回收。
- `TestSecondHalfUnauthorized`：列表后半项越权/缺页时整表原子失败、零回调。
- `TestPhysicalPageCrossTenantAlias`：宿主表拒绝同物理页跨租户归属，并对
  伪造页表项做归属表交叉校验。
- 另含跨页拆分、权限、溢出、限额、混租户、未知设备等用例与可运行 `Example`。

```sh
go test -race -count=1 ./...
go test -cover ./...
```
