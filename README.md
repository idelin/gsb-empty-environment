# Tombstone GC Coordinator (Go)

因果历史修剪与垃圾回收协调器：清理 CRDT 长周期运行产生的已删除墓碑。
通过**分布式因果稳定水位（Causal Stability Frontier）**识别已被全网所有
节点感知的墓碑，并物理释放其内存。仅使用 Go 标准库。

## 设计

- `clock.go` — 向量时钟与因果稳定水位：`Frontier` 取所有节点向量时钟的
  逐分量最小值；水位之下的事件保证已被全网观测。
- `store.go` — LWW CRDT 存储：删除产生保留 value 的墓碑；`GC` 仅物理回收
  被水位 dominate 且 `T < threshold` 的墓碑。
- `node.go` — 模拟节点：反熵 gossip 交换条目、自身时钟与第三方时钟表；
  `StableAt` 基于本地时钟表判断全网是否都已越过阈值。
- `gc.go` — 协调器：仅当所有协调节点（A、B）各自确认因果稳定后，才在
  全集群执行回收；否则安全地不回收任何对象。
- `sim.go` / `main.go` — 5 节点（A–E）集群模拟与演示。

## 运行样例

```console
$ go run .
=== Tombstone GC via Causal Stability Frontier ===
cluster: 5 nodes (A-E), coordinators: A, B, threshold: T=50
node A: local clock T=56, entries=160, memory=32300 B
node B: local clock T=56, entries=160, memory=32300 B
node C: local clock T=56, entries=160, memory=32300 B
node D: local clock T=56, entries=160, memory=32300 B
node E: local clock T=56, entries=160, memory=32300 B
--- GC pass ---
causally stable at T=50: true
tombstones reclaimed: 550 (freed 153750 B)
cluster memory: 161500 B -> 7750 B (reduction 95.2%)
```

## 测试结果

```console
$ go test -v ./...
=== RUN   TestTombstoneGCReclaimsStableTombstones
--- PASS: TestTombstoneGCReclaimsStableTombstones (0.00s)
=== RUN   TestNoGCWhenClusterNotStable
--- PASS: TestNoGCWhenClusterNotStable (0.00s)
=== RUN   TestVectorClockMergeAndDominates
--- PASS: TestVectorClockMergeAndDominates (0.00s)
=== RUN   TestFrontierIsComponentWiseMin
--- PASS: TestFrontierIsComponentWiseMin (0.00s)
PASS
ok  	tombstonegc
```

验收边界覆盖：

- `TestTombstoneGCReclaimsStableTombstones` — 5 节点集群，A/B 确认全网越过
  T=50 后自动回收 T<50 的全部墓碑（含副本共 550 个），内存下降 ≥90%
  （实测 95.2%）；存活条目与 T≥50 的晚墓碑完整保留。
- `TestNoGCWhenClusterNotStable` — 节点 E 停滞在 T=10 且失联时，协调器
  拒绝回收，任何墓碑不得丢失。
- 向量时钟合并/dominate 与水位逐分量最小值的单元测试。
