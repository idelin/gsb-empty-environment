# distlock — 带租约隔离的分布式互斥锁

纯 Go 标准库实现，语义对齐 etcd/ZooKeeper 风格的分布式锁：

- **租约 TTL 防死锁**：获取锁时绑定租约；持有者崩溃停止心跳后，租约到期自动释放给等待者。
- **KeepAlive 心跳**：客户端后台协程按 `TTL/3` 周期自动续期。
- **Fencing Token 防脑裂**：每次授锁分配全局单调递增 token，受保护资源拒绝携带过期 token 的写操作（防止假死恢复的节点脏写）。

## 结构

- `distlock.go` — 锁服务（租约管理、等待唤醒、fencing 校验、受保护资源读写）
- `client.go` — 客户端节点（后台心跳协程、`Freeze/Unfreeze` 假死模拟）
- `distlock_test.go` — go test 验收测试
- `cmd/demo/main.go` — 运行样例

## 运行测试

```sh
go test -v ./...
```

核心验收用例 `TestLeaseExpiryAndFencing`：A 持锁并心跳 → A 假死 → 5 秒租约自然超时 →
排队中的 B 获锁 → A 恢复后写操作被 `ErrFencing` 拒绝。

## 运行样例

```sh
go run ./cmd/demo
```
