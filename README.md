自治系统内部署多台路由反射器时，由于 MED 默认只在来自同一相邻自治系统的路由之间比较，选路关系失去传递性，常导致内部路由永不收敛。为控制面分析服务交付可导入的 Go 仿真包。输入节点集合（边界路由器、反射器、客户端）、IGP 最短路径代价矩阵、各边界路由器收到的外部前缀路由（含 LocalPref、ASPathLen、Origin、相邻 AS 号、MED、RouterID）以及反射器客户端隶属关系和簇 ID。节点按标准序比较两候选路由，仅同相邻 AS 比较 MED，反射器转发时追加自身簇 ID 且丢弃含本簇 ID 的路由，并只向对等体通告当前最优路由。组件在同步轮次模型下模拟通告演化，返回唯一收敛后的各节点最优路由，或在出现周期震荡时返回首次重复状态及最小震荡周期，并标出因不可比性被淘汰的次优候选。验收用小拓扑逐轮展开对照，覆盖典型三反射器 MED 震荡三角形、启用 Always-Compare-MED 后震荡消失、客户端同时直连多反射器触发簇 ID 环路过滤，以及 IGP 代价并列按最低 RouterID 决胜。

---

## 交付包 `bgpsim`

模块根目录即可导入，包名为 `bgpsim`：

```go
import "prescreen.local/ps-b15ac20600"

res, err := bgpsim.Simulate(&bgpsim.Config{ /* ... */ })
```

仅依赖 Go 标准库；也可用 `bgpsim.Run(cfg)`、`bgpsim.NewSimulator(cfg)` 或
`cfg.Validate()`。运行 `go run ./cmd/bgpsim-demo` 可直接打印四个验收场景的逐轮表。

### 建模约定

- `Config.Nodes`：节点及 `RouterID`、`Reflector`、`Border`、可选 `ClusterID`。
- `Config.Clusters`：`{ClusterID, Reflectors, Clients}` 隶属关系；同一客户端可出现在多个簇中（双归）。所有反射器自动按标准约定组成 iBGP 全互联，另可用 `ExtraPeers` 增补会话。
- `Config.IGP`：`IGP[from][egress]` 最短路径代价；`bgpsim.DenseIGP(ids, matrix)` 可从稠密矩阵构造。
- `Config.ExternalRoutes`：边界路由器收到的外部路由，属性含 `LocalPref/ASPathLen/Origin/NeighborAS/MED/RouterID`，`Egress` 即接收边界（也是 IGP 目标）。外部路由作为稳定注入源每轮由其边界通告（等价 eBGP 管理距离优先）。
- 标准选路序：`LocalPref` 高 → `ASPathLen` 短 → `Origin` 小（IGP<EGP<INCOMPLETE）→ MED 小（默认仅当 `NeighborAS` 相同；`Options.AlwaysCompareMED=true` 时跨 AS 比较）→ 到 `Egress` 的 IGP 代价小 → 携带的 `RouterID` 数值小。
- 候选按 `RouterID/Egress/LearnedFrom/ClusterList` 规范化排序后做确定性逐对比较（BGP best-path fold），结果与 map 遍历、输入顺序无关。
- 反射器转发学到的路由时追加自身 `ClusterID`；收到含本簇 ID 的通告即丢弃并记录 `DiscardClusterLoop`。每节点每前缀只向所有对等体通告当前最优。
- 同步轮次：第 0 轮只有外部注入；第 k 轮处理第 k-1 轮末生成的通告，随后生成下一轮通告。

### 返回结果

- `Result.Status`：`converged` / `oscillating` / `limit_reached`。
- 收敛时 `Result.Best[node][prefix].Best` 为唯一最优路由。
- 震荡时 `Result.Oscillation` 给出 `FirstRound`、`RepeatRound`、最小 `Period`、首次重复状态 `FirstState` 与周期状态序列 `Cycle`。状态指纹包含最优路由的 `ClusterList/LearnedFrom`，判周期严格。
- `RoundReport.Choices[node][prefix].Candidates` 给出每个候选的逐对淘汰阶段（`EliminatedAt`）、`MEDCompared/MEDIncomparable/LowerMEDThanRival` 以及相对最终最优的 `IncomparabilityEliminated` 标记。
- `Result.MEDIncomparableEliminations` 汇总所有“因跨相邻 AS 不可比而跳过 MED、随后在 IGP/RouterID 阶段被淘汰”的候选；`Result.ClusterLoopDiscards` 汇总簇 ID 环过滤事件。

### 验收逐轮对照

三反射器三角形（`rr1/rr2/rr3` 各带边界客户端 `b1/b2/b3`；相邻 AS 为 `10/20/10`，MED 为 `10/20/30`，RR 到出口 IGP 行分别为 `(2,1,3)/(3,2,1)/(1,3,2)`）：

```text
默认（仅同 AS 比 MED）          AlwaysCompareMED
round  rr1 rr2 rr3             round  rr1 rr2 rr3
0      -   -   -               0      -   -   -
1      b1  b2  b3              1      b1  b2  b3
2      b2  b3  b1              2      b1  b1  b1
3      b1  b2  b3  (= round1)  3      b1  b1  b1  (= round2)
=> oscillating first=1         => converged
   repeat=3 period=2
```

- 默认配置最小震荡周期 2，并记录 `rr1` 上 `b1`（MED 10）因与 `b2`（MED 20）相邻 AS 不同而不可比、最终在 IGP 阶段输给 `b2` 的标记；同 AS 的 `b1/b3` 之间仍真实比较 MED（`b3` MED 30 出局）。
- 双归客户端 `c` 同时直连 `rr1`(簇 `1`) 与 `rr2`(簇 `2`)：第 3 轮 `rr1` 丢弃 `c` 回送的含 `[1]` 通告，并丢弃经 `rr2` 反射后含 `[1 2]` 的通告，随后收敛。
- IGP 并列 5、MED 相同、其余属性持平时，`RouterID 2.2.2.2`（e2）胜过 `4.4.4.4`（e1），淘汰阶段为 `RouterID`，交换输入切片顺序结论不变。

### 测试

```sh
go test -v ./...
go run ./cmd/bgpsim-demo
```

验收测试：`TestThreeReflectorMEDTriangleOscillates`、
`TestAlwaysCompareMEDStopsOscillation`、
`TestDualHomedClientTriggersClusterLoopFilter`、
`TestIGPTieBrokenByLowestRouterID`，另有比较子单元测试、两个
`Example*` 逐轮输出断言和 JSON/校验冒烟测试。
