# leasering — 租约感知的一致性哈希寻址器

将节点心跳租约状态与哈希环绑定的路由组件，仅依赖 Go 标准库。

## 设计

- **红黑树哈希环**（`rbtree.go`）：虚拟节点按 uint32 哈希值组织在红黑树中，
  寻址即 `ceiling` 顺时针查找（含回绕），插入/删除/查找均为 O(log n)。
- **租约绑定**（`ring.go`）：节点通过 `Heartbeat` 续约；只有持有有效租约的
  节点参与环拓扑。`Tick` 推进逻辑时钟后，在**同一写锁临界区**内将超时节点的
  全部虚拟节点一次性抹去——观察者只会看到"剔除前/剔除后"两种一致状态。
- **透明重平衡**：节点被剔除后，其 Key 区间自然归顺时针后继所有，无需显式
  数据搬迁；`RebalanceEvent` 通过回调上报剔除节点与继承分布。

## API

```go
ring := leasering.New(leasering.WithVNodes(64), leasering.WithLeaseTicks(3))
ring.AddNode("A")          // 入环并授予租约
ring.Heartbeat("A")        // 续约
ring.Tick(1)               // 推进时钟；静默 >3 tick 的节点被原子剔除
owner, _ := ring.Locate("user:1001")
```

## 运行

```sh
go test -race -v .   # 测试（含验收边界）
go run ./cmd/demo    # 运行样例：C 断开心跳后的原子剔除与 Key 平移
```

## 验收边界对应测试

- `TestLeaseExpiryAtomicEviction`：C 静默超过 3 个 tick 后被原子剔除，
  原属 C 的 Key 全部平移给 A/B，其余 Key 归属不变，全程无 Locate 失败。
- `TestKeysShiftToClockwiseSuccessor`：逐 Key 验证新归属恰为顺时针后继。
- `TestLeaseBoundaryExactlyThreeTicks`：静默恰好 3 tick 不剔除，第 4 tick 剔除。
- `TestConcurrentLocateDuringEviction`：剔除与寻址并发下的一致性（`-race`）。
