# 加密卷密钥擦除屏障

删除机密卷时，磁盘数据保留但所有可解密密钥副本必须失效。写供Go卷宿主调用的销毁协调包，密钥副本有持久位置与活跃租约，先禁止新租约，再收集各副本擦除确认；任一未知副本使结果为未完成。崩溃后沿销毁事务继续，不能恢复可用状态，用故障存储测试迟到确认与卷名重用。

## 包结构

`package netsec` 为卷宿主提供可恢复的密钥销毁协调器：

- `BlobStore`：持久化边界（原子单键写、前缀枚举），内置 `MemoryStore`；`FaultStore` 在测试中注入 I/O 故障、延迟提交与崩溃丢写。
- `Coordinator`：创建卷、发放/释放租约、启动销毁事务；构造时自动扫描并恢复未完成事务。
- `Destruction`：销毁事务句柄，支持 `Step` 推进、`ReportErasure`/`ReportAbsent` 收集确认、`Status` 快照。
- `Eraser` / `ReplicaScanner`：宿主实现的副本擦除后端与副本观测接口。

## 销毁状态机

1. `freezing`：先把卷清单的 `frozen` 标志与销毁记录持久化，此后所有 `GrantLease` 返回 `ErrLeasesProhibited`，且没有任何密钥副本被触碰。
2. `draining`：等待全部活跃租约到期或显式释放。
3. `erasing`：对清单内每个副本收集擦除确认（宿主后端或异步 `ReportErasure`），每次确认单独落盘。
4. `reconciling`：扫描可达副本；清单外副本记入 `UnknownReplicas` 并返回 `ErrUnknownReplica`，结果保持 `incomplete`，直到该副本自身的确认到达。
5. `complete`：全部密钥副本确认失效，名称索引改为墓碑；磁盘密文与销毁记录均保留。

## 崩溃与恢复语义

- 每个状态迁移都是一次新的持久化写入，不存在“回滚到可用状态”的路径；`NewCoordinator` 恢复时会重新断言 `frozen`。
- 迟到确认按卷代际（随机 `VolumeID`）与副本 ID 路由，重复确认返回 `ErrAlreadyErased`，不会作用于重用同名的新卷。
- 卷名仅在销毁完成后可重新创建为新代际；销毁进行中重用返回 `ErrNameInUse`，已销毁名称打开返回 `ErrVolumeDestroyed`。
- `FaultStore` 的 `DelayPuts`/`DeliverPending`/`Crash` 用于测试迟到提交与崩溃续跑。

## 测试

```sh
go test -race ./...
```

覆盖完整销毁、租约排空、未知副本阻断、迟到确认、卷名重用、崩溃续跑、I/O 故障重试与后端擦除失败恢复等场景。
