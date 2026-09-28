# flowlb — 稳定流 ID 的加权等价路径分配 + 离散事件仿真

纯 Go 标准库实现（无第三方依赖），包含两部分：

- **分配器（`assign.go` / `hash.go`）**：基于加权 Rendezvous（HRW）哈希，
  把稳定流 ID 映射到路径。路径容量权重显式声明在 `Path.Weight` 上，
  权重为 0 的路径不接新流。
- **仿真器（`sim.go`）**：离散事件模型，时间为整数 tick。包到达是事件堆中的
  事件，每条路径一个 FIFO 队列，链路每 tick 按 `Path.Rate` 个包服务。
  不发任何真实网络流量。

## 性质

- 同一流的所有包始终走同一路径（分配按流 ID 记忆，拓扑不变时不抖动）。
- 删除路径只迁移原来就在该路径上的流，其余流纹丝不动。
- 新增路径只从现有路径"吸走"与其权重成比例的流。
- 权重 0 的路径（如备用链路）不会分配到任何流。
- 迁移比例与路径负载（包数、字节、占比、平均排队时延、最大队列）均可输出。

## 运行

```sh
go run ./cmd/flowlb-demo            # 默认 2000 流、500 tick
go run ./cmd/flowlb-demo -flows 5000 -ticks 1000
```

演示分三个阶段：基线（5 条路径，权重 3:2:2:1:0）→ 删除 path-A →
新增 path-E（权重 3），每阶段打印各路径负载，拓扑变化时打印迁移比例。

## 测试

```sh
go test ./...
```

覆盖：分配稳定性与可复现性、删路径只迁移必要流、权重 0 不接流、
权重比例分布（±2%）、加路径迁移比例（≈w_new/Σw）、仿真全量交付、
包级路径一致性、权重 0 路径空闲、拥塞下队列增长。

## 样例输出（`go run ./cmd/flowlb-demo`）

```
== baseline: 5 paths (weights 3:2:2:1:0) ==
path           weight   rate  delivered    share      bytes  avgWait  maxQueue
path-A              3    500     195848   38.06%  139029618    0.00         0
path-B              2    320     123750   24.05%   89400370    0.00         0
path-C              2    320     131043   25.47%   92587103    0.00         0
path-D              1    170      63959   12.43%   43878774    0.00         0
path-standby        0    100          0    0.00%          0    0.00         0
generated=514600 delivered=514600 dropped=0

>> remove path-A: migrated 767/2000 flows (38.35%), dropped 0

== after removing path-A ==
path           weight   rate  delivered    share      bytes  avgWait  maxQueue
path-B              2    320     160000   31.09%  114247368   48.75     38755
path-C              2    320     160000   31.09%  111566753   60.35     50925
path-D              1    170      85000   16.52%   61480180   47.46     19920
path-standby        0    100          0    0.00%          0    0.00         0
generated=514600 delivered=405000 dropped=0

>> add path-E (weight 3): migrated 742/2000 flows (37.10%)

== after adding path-E ==
path           weight   rate  delivered    share      bytes  avgWait  maxQueue
path-B              2    320     121332   23.58%   88331313    0.00         0
path-C              2    320     136926   26.61%   94247076    0.00         0
path-D              1    170      67666   13.15%   45956122    0.00         0
path-E              3    500     188676   36.66%  136361354    0.00         0
path-standby        0    100          0    0.00%          0    0.00         0
generated=514600 delivered=514600 dropped=0
```

说明：迁移比例 ≈ 被删/新增路径的权重占比（3/8 = 37.5%），与理论一致；
删除 path-A 后剩余链路总速率低于到达率，因此第二阶段出现排队（avgWait、
maxQueue 上升），扩容 path-E 后恢复零积压。

## 测试结果

```
ok  	flowlb	2.470s
```

10 个测试全部通过（见上方"测试"一节）。
