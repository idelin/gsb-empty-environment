# 多路径连接包号归属

多路径传输把两条路径的确认信息混用会错误释放重传数据。实现供Go会话宿主调用的确认账本，包号属于连接级空间但发送记录保存路径实例及帧引用；确认只释放真实已发送且认证过的范围，重复确认幂等，未知范围拒绝。用区间参考模型测试路径迁移、号回绕和同帧多次发送。

## 设计

`Ledger` 是每个连接一个的确认账本，解决多路径下 ACK 空间混用导致的错误释放：

- **连接级包号空间**：包号不按路径分表。发送时用 `Sent` 记录包号、所属路径实例与帧引用，ACK 到达任意已认证路径都按同一空间解释，因此路径迁移后旧路径发出的帧仍能被新路径的 ACK 正确释放。
- **只认真实已发送且认证的范围**：`AckRanges` 先校验承载路径已认证，再确认区间内每个包号都有存活发送记录；任何未知/已回收/越界包号都会整批拒绝（`ErrUnknownPacket` / `ErrInvalidAckRange`），不产生部分副作用。
- **幂等释放**：帧引用去重，重复 ACK、同帧在多个包中重传都只会释放一次；重复确认返回空集合而非错误。
- **回绕安全**：小号模空间下，仍有在途包时回绕返回 `ErrWrapWhileInFlight`；`RetireAcked` 回收已确认槽位后才允许复用，并用 epoch 区分不同周期。

## 主要 API

| 方法 | 作用 |
| --- | --- |
| `NewLedger()` / `NewLedgerWithModulus(m)` | 全 64 位空间 / 可注入 2 的幂小模数（测试回绕） |
| `AddPath` / `RemovePath` / `PathAuthenticated` | 路径实例注册与认证状态 |
| `Sent(pn, path, frames)` | 记录一次真实发送（路径 + 帧引用） |
| `AckRanges(path, ranges)` | 处理认证 ACK，返回本次新释放的帧引用 |
| `MarkLost(pns...)` | 声明丢包，返回待重传且尚未释放的帧 |
| `RetireAcked()` | 回收已确认记录，允许包号槽位复用 |
| `InFlight` / `Record` / `WasSent` / `WasAcked` / `SentRanges` / `AckedRanges` | 状态查询 |

错误统一为哨兵值（`ErrUnknownPacket`、`ErrUnauthenticatedPath`、`ErrWrapWhileInFlight` 等），用 `errors.Is` 匹配。所有方法带互斥锁，可供宿主并发调用。

## 测试

- `interval_set.go` 是区间参考模型（归一化并集/差集，含 uint64 边界），单测覆盖合并、拆分与溢出。
- `ledger_test.go` 内有独立 map/slice 预言机 `oracle`，`TestDifferentialAgainstReferenceModel` 在 5 种模数、3000 轮随机发送/确认/丢包/回收负载下做差分校验。
- 命名场景：`TestPathMigration`（迁移后跨路径确认、移除路径拒绝 ACK）、`TestPacketNumberWrap`（回绕、epoch、陈旧 ACK 拒绝）、`TestSameFrameMultipleSends`（同帧多次发送只释放一次）、`TestUnknownAndInvalidAcknowledgments`（未知范围整批拒绝且无副作用）。

```sh
go test ./... -count=1 -race
```
