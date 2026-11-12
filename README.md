# Gluster锁属主窃取防护

GlusterFS 的 POSIX 锁靠请求里的 lk-owner 标识持有者，攻击者伪造他人的 lk-owner，去解锁或接管其锁。为Go的存储服务端写可导入的锁表包：锁归属键是（连接标识，lk-owner）二元组，连接标识由传输层认证后赋值，不取自请求体；同一 lk-owner 来自不同连接视为不同属主，UNLOCK 只能释放本连接持有的区间并可拆分；连接断开后其锁保留 10 秒宽限期，期内只有持断开前发放的 reclaim 令牌的新连接可原样重取，超时清除；clear-locks 类管理操作须带管理身份；阻塞队列里的请求在属主断开后立即移除。测伪造 lk-owner、跨连接解锁、宽限期重取、令牌错配。

## 实现：`locktable` 子包

导入路径 `example.org/netsec/q0463/locktable`，仅依赖标准库，并发安全。

安全语义与需求逐项对应：

- 属主键为 `(ConnID, LkOwner)`。`ConnID` 由 `Connect` 在传输层认证后发放，
  请求体无法指定；同一 `LkOwner` 跨连接是不同属主，天然免疫伪造 lk-owner。
- `Unlock(id, owner, rng)` 只用当前连接解析属主键，支持子区间拆分释放，
  跨连接解锁返回 `ErrLockNotHeld`。
- `Disconnect` 把锁移入 10 秒宽限期（`GracePeriod`），并立即摘除该连接的
  全部阻塞请求（`ErrBlockerGone`）。宽限期内的锁仍参与冲突判定，不能被接管。
- 重取须先在断开前调用 `IssueReclaimToken`，再由新连接调用
  `Reclaim(newConn, owner, token)`；令牌用 `crypto/rand` 生成 256 位随机值、
  恒定时间比较，错配返回 `ErrTokenMismatch`，无令牌返回 `ErrNoReclaimToken`，
  超时返回 `ErrGraceExpired` 并清除锁。
- `ClearLocks(id, filter)` 要求 `Connect(true)` 的管理身份，否则
  `ErrPermissionDenied`；`filter == nil` 全量清空，否则按区间清除并拆分。
- 阻塞 `Lock` 走 FIFO 队列，`ctx` 取消即出队；`Close` 停止后台回收协程。

时间源通过 `Clock` 接口注入：生产用 `New()`（真实时钟），测试用
`NewForTest(NewFakeClock(...))` 确定性驱动 10 秒宽限期。

运行测试（含竞态检测）：

```sh
go test -race -count=1 ./locktable
```
