# dvsim — 距离向量路由离散事件模拟

纯标准库 Go 实现。所有路由器、消息队列与链路都运行在整数 tick 的
离散事件模型上，不产生任何真实网络流量。

## 设计

- `sim.go` — 离散事件调度器：最小堆按 (tick, 序号) 排序，同 tick FIFO。
- `router.go` — 距离向量路由器：
  - **Split horizon + poison reverse**：向邻居 N 通告时，把下一跳为 N
    的路由以 `Infinity` 通告回去（`buildUpdate`）。
  - **指标上限即不可达**：`Infinity = 16`，任何 ≥ 上限的度量被钳制并
    视为不可达，因此"数到无限"必然有界、不会永久持续。
  - **版本规则**：每个发送方维护单调递增的 `Seq`，接收方丢弃不比已
    接受版本新的报文，乱序/重复投递安全。
  - **触发更新 + 周期更新 + 路由超时清扫**：同一邻居的更差度量也被
    接受；全表通告中消失的目的地即撤销；超时未刷新的路由被清扫。
- `network.go` — 链路与消息队列：每条消息延迟为 `[1,5]` tick 的随机
  值（种子可复现），因此异步投递天然乱序；链路可 up/down。
- `reference.go` — Bellman-Ford 参考最短路，`VerifyAgainstReference`
  校验每个路由器的度量与下一跳（下一跳必须落在某条最短路上）。

## 运行

```sh
go test ./...      # 全部测试
go run ./cmd/dvsim # 样例：收敛 → 链路断开 → 恢复，每阶段对照参考最短路
```

## 样例输出（节选）

```
=== phase 1: initial convergence ===
[phase 1] converged at tick 5: matches reference shortest paths
=== phase 2: link B-C goes down at tick 300 ===
[phase 2] converged at tick 317: matches reference shortest paths
=== phase 3: link B-C restored at tick 600 ===
[phase 3] converged at tick 605: matches reference shortest paths
messages: delivered=1316 dropped=2
max finite metric ever installed: 6 (infinity=16, count-to-infinity bounded)
RESULT: PASS
```

## 测试结果

15 个测试全部通过：事件顺序、毒化逆转、旧版本报文丢弃、同下一跳
接受更差度量、路由撤销、指标钳制、断链报文忽略、收敛一致性、断链
后有界重收敛、网络分区不永久计数、随机拓扑与 20 组乱序种子收敛。
