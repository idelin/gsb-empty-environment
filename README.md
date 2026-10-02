# distlock — 带租约隔离的分布式互斥锁

纯 Go 标准库实现，无第三方依赖。

## 设计

- **租约（Lease）**：每次获取锁绑定一个 TTL，服务端记录租约截止时间；
  后台 sweeper 周期扫描，租约过期即自动释放并唤醒 FIFO 队列中的下一个等待者，防止持有者崩溃导致死锁。
- **心跳续期（KeepAlive）**：获取锁成功后自动启动后台 goroutine，以 `TTL/3`
  为间隔调用 `keepAlive` 延长租约；续期失败（租约已丢失）时通过 `Lock.Done()` 通知持有者。
- **栅栏令牌（FenceToken）**：每次授予锁时签发单调递增的 token；下游资源
  （`Storage`）只接受 token 严格递增的写入，崩溃恢复的旧持有者会被 `ErrStaleFence` 拒绝。
- **崩溃模拟**：`Lock.SimulateCrash()` 停止心跳但不释放租约，模拟节点假死/网络分区。

## 文件

- `distlock.go` — 锁服务端（租约、等待队列、过期清扫）与客户端句柄（心跳、解锁）
- `storage.go` — 带 FenceToken 校验的下游存储
- `distlock_test.go` — 验收测试
- `cmd/demo/main.go` — 5 秒租约的完整验收场景演示

## 运行

```sh
go test ./... -v -count=1   # 运行测试
go run ./cmd/demo           # 运行验收场景演示（租约 TTL = 5s）
```

## 测试覆盖

- `TestLeaseExpiryHandsLockToWaiterAndFencesStaleHolder` — 验收场景：A 持锁后假死，
  租约自然过期，排队的 B 获得锁；A 恢复后写入被 FenceToken 拒绝
- `TestKeepAliveExtendsLease` — 心跳持续续期，超过多个 TTL 后 A 仍是持有者
- `TestUnlockWakesWaiter` — 主动 Unlock 立即唤醒等待者，无需等待 TTL
- `TestExpiredHandleCannotUnlock` — 租约过期后旧句柄操作返回 `ErrLockLost`
- `TestFenceTokensIncrease` — FenceToken 跨获取单调递增
