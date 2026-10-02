# leasehash — 租约感知的一致性哈希寻址器

将节点心跳租约状态与哈希环（红黑树）绑定的路由组件，纯 Go 标准库实现。

## 设计

- **哈希环 = 红黑树**（`rbtree.go`）：CLRS 红黑树，key 为虚拟节点的 64 位 FNV-1a 哈希，
  `successor(hash)` 即"顺时针查找"，越界自动回绕到环首。
- **租约绑定拓扑**（`router.go`）：每个物理节点持有 `leaseLeft`（单位：tick）。
  `Heartbeat(id)` 续租至完整 TTL；`Tick()` 推进一个心跳周期，所有节点租约减一。
- **原子剔除**：租约耗尽的节点在**同一把写锁内**被抹去全部虚拟节点，
  并发读者只能观察到"剔除前"或"剔除后"两种完整拓扑，绝无中间态。
- **透明平移**：节点掉线后，原属它的 Key 由一致性哈希语义自然落到顺时针下一节点，
  其余节点的 Key 零扰动（最小扰动特性），业务无阻断。
- **下游重平衡**：`OnNodeDown` 回调与 `Tick()` 返回值携带 `DownEvent`
  （掉线节点、抹去虚拟节点数、每个位置的顺时针接管者），在锁外触发。

## API

```go
r := leasehash.NewRouter(256, 3)  // 每节点 256 虚拟节点，租约 TTL = 3 tick
r.AddNode("A")                    // 加入环并授予完整租约
r.Heartbeat("A")                  // 续租
r.Tick()                          // 推进心跳周期；租约耗尽者被原子剔除
owner, err := r.Lookup("key")     // 路由：只返回持有效租约的在线节点
r.OnNodeDown = func(ev leasehash.DownEvent) { /* 下游重平衡 */ }
```

## 运行样例

```sh
go run ./cmd/demo
```

模拟节点 C 心跳断开：tick 1–2 租约未耗尽 C 仍在环上；tick 3 租约耗尽，
C 的 256 个虚拟节点被原子抹去，原属 C 的 Key 瞬间平移至顺时针后继节点，其余 Key 零扰动。

## 测试

```sh
go test -race -v ./...
```

覆盖：红黑树性质（随机 5000 次插入/删除 + BST 有序性/红黑性质校验）、
顺时针回绕、租约到期原子剔除（验收场景）、Key 顺时针平移精确比对、
心跳续租、并发读写下的原子性（race detector）、主动摘除与空环、负载分布。

### 测试结果（go1.11 darwin/amd64）

```
=== RUN   TestRBTreeInsertRemoveRandom          --- PASS (1.02s)
=== RUN   TestRBTreeSuccessorWraps              --- PASS (0.00s)
=== RUN   TestLeaseExpiryAtomicallyEvictsNode   --- PASS (0.01s)
=== RUN   TestKeysShiftToClockwiseSuccessor     --- PASS (0.08s)
        keys on C before eviction: 310/2000
        migrated 310 keys off C transparently; 1690 keys on A/B untouched
=== RUN   TestHeartbeatRenewsLease              --- PASS (0.00s)
=== RUN   TestAtomicEvictionUnderConcurrency    --- PASS (0.01s)
=== RUN   TestRemoveNodeAndEmptyRing            --- PASS (0.01s)
=== RUN   TestDistributionRoughlyBalanced       --- PASS (0.59s)
        distribution: A=7930 B=12340 C=9730
=== RUN   TestDownEventSuccessors               --- PASS (0.00s)
PASS
ok      leasehash       3.694s
```
