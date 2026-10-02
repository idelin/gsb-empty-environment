# tombgc — 因果历史修剪与 Tombstone GC 协调器 (Go)

清理 CRDT 长周期运行积累的已删除墓碑。通过分布式**因果稳定水位
（Causal Stability Frontier）**识别已被全网所有节点感知的墓碑，并物理释放。

## 设计

- `VersionVector`：因果水位（向量时钟），支持 `Merge` / `Dominates` / `Min`。
- `Tombstone`：删除标记，携带删除时刻的因果时间戳与内存占用。
- `Coordinator`：每个节点一个。跟踪本地墓碑集合与全网各节点 gossip 来的
  水位；稳定水位 = 所有节点水位的逐分量最小值。被稳定水位 dominate 的
  墓碑即被全网感知，`CollectGarbage` 将其物理移除并做内存记账。
- `Cluster`：内存模拟的全互联集群（gossip + 协调 GC），演示与测试用。

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 5 节点集群演示
```

## 验收场景

5 节点集群（A–E），每节点积累 100 个 T∈[1,49] 的旧墓碑与 5 个
T∈[51,55] 的新墓碑。所有节点越过 T=50 并 gossip 后，A/B 确认稳定水位
≥50，协调 GC 物理回收全部 T<50 墓碑，集群墓碑内存下降 95.2%（≥90%）。
