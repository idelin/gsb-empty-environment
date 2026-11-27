Kubernetes 的 Topology Aware Routing（`EndpointSlice` 提示分配 + 节点代理路由编译）在跨可用区削减跨区流量时存在极易引发雪崩的边界：控制面按各可用区节点 CPU 占比为每个区分配就绪端点提示（`hints.forZones`），但若某区端点数偏少导致单端点承担流量超过过载阈值（默认 `overloadThreshold = 1.2`，即分配比例须覆盖至少 `1/1.2` 的期望份额）或集群少于 2 个可用区，控制面必须触发 Safeguard 机制清空全部 `hints` 回退全局路由；而数据面编译每个节点的后端权重表时，还必须正确处理 `EndpointConditions` 三标志位（`ready`、`serving`、`terminating`）与 `internalTrafficPolicy` 的交互——处于优雅终止（`ready=false, serving=true, terminating=true`）的端点平时不得接收新流量，但在有效路由集合中已无任何 `ready=true` 端点时必须启用作为兜底！为云原生服务网格/代理控制面交付可导入的 Go 拓扑提示分配与节点路由表编译包。输入为各可用区节点 CPU 容量表、服务配置（`trafficDistribution` / `topologyHintsEnabled`、`internalTrafficPolicy` 为 `Cluster` 或 `Local`、可选 `overloadThreshold`）及 EndpointSlice 列表（每端点含 IP、所属 Zone、所属 Node、权重与 `ready`/`serving`/`terminating` 三标志）。规则：1）控制面提示分配器先过滤有效 `ready` 端点并校验 Safeguard 条件（活跃区数 `< 2`、任一区 CPU 为 0、总就绪端点数 `< 活跃区数` 或任一区无法在不超载的前提下获得所需最小端点配额时，清空全部 `hints` 并给出精确退化原因！）；满足条件时优先把本区端点分配给本区，余量再按最小跨区过载比确定性补齐缺口区；2）数据面节点编译器针对给定目标节点 `(node, zone)` 构造最终转发端点集与权重：若 `internalTrafficPolicy == Local`，严格仅选本节点端点（先 `ready`，全空再回退本节点 `serving && terminating`）；若为 `Cluster`，存在有效 `hints` 且包含匹配本 `zone` 的 `ready` 端点时仅选匹配本区的 `ready` 端点，若 `hints` 缺失或本区无匹配 `ready` 端点则回退全集群 `ready` 端点，仅当全集群无任何 `ready` 端点时回退到 `serving && terminating` 端点（遵循相同本区优先/全局兜底规则！）；同时排除 `ready=false && serving=false` 的失效端点。验收覆盖各区 CPU 不均衡触发 Safeguard 全局回退、缺口区跨区最小过载补齐、`Local` 策略覆写、全区无 Ready 时 `serving+terminating` 兜底及多 EndpointSlice 重复端点去重合并。

---

# 交付包：`topology`

可导入的 Go 包（模块 `prescreen.local/ps-ec68864831`，包路径 `.../topology`），
实现 Topology Aware Routing 的控制面提示分配与数据面节点路由表编译，无第三方依赖。

## 公开 API

- `AllocateHints(cfg ServiceConfig, zones []ZoneCapacity, slices []EndpointSlice) *Allocation`
  控制面分配器。先 `MergeSlices` 去重，过滤 `ready` 端点，按区 CPU 占比与
  `overloadThreshold`（默认 1.2）计算每区最小配额；命中任一 Safeguard 时
  `Active=false`、`Hints=nil`，并在 `Reason`/`Message` 给出精确退化原因：
  `HintsDisabled`、`NoInput`、`InsufficientZones`、`ZeroCPUZone`、
  `EndpointCountBelowZoneCount`、`MinimumQuotaOverload`、`InvalidConfig`。
  正常时三阶段确定性分配：本区端点优先满足本区最小配额 → 缺口区按“最大富余区
  优先”跨区借端点（最小化每次跨区造成的过载比）→ 余量按最小结果过载比分发。
- `CompileNodeRoutes(cfg, node, zone, allocation, slices) *RouteTable`
  数据面编译器，输出节点最终后端表 `[]Backend`（含权重、是否终止兜底、命中区）。
  - `Local`：仅本节点端点，先 `ready`，全空再回本节点 `serving && terminating`。
  - `Cluster`：有效 hints 且本区有匹配的 `ready` 端点时仅选本区 hints；
    否则回退全集群 `ready`；仅当全集群无 `ready` 时启用 `serving && terminating`
    兜底（同样遵循本区 hints 优先 / 全局兜底）。
  - `ready=false && serving=false` 的失效端点始终排除。
- `MergeSlices(slices)`：多 EndpointSlice 按 IP 去重合并（条件按 AND 合并、
  权重取最大非零值、身份字段取字典序首个 slice），结果按 IP 排序，分配与编译
  全程确定性，可重入收敛。
- `AggregateZoneCapacity(nodes)`：节点级 CPU 容量聚合为区级容量。
- `CompileNodeRoutesMerged(...)`：调用方已自行合并时的零拷贝入口。

## 最小示例

```go
cfg := topology.ServiceConfig{
    TrafficDistribution:   topology.TrafficDistributionPreferClose,
    InternalTrafficPolicy: topology.InternalTrafficPolicyCluster,
}
zones := []topology.ZoneCapacity{
    {Zone: "zone-a", CPU: 50},
    {Zone: "zone-b", CPU: 50},
}
slices := []topology.EndpointSlice{{Name: "svc-1", Endpoints: []topology.Endpoint{
    {IP: "10.0.0.1", Zone: "zone-a", Node: "n-a-1",
        Conditions: topology.EndpointConditions{Ready: true, Serving: true}},
}}}

alloc := topology.AllocateHints(cfg, zones, slices)
if !alloc.Active {
    log.Printf("safeguard fallback: %s: %s", alloc.Reason, alloc.Message)
}
table := topology.CompileNodeRoutes(cfg, "n-a-1", "zone-a", alloc, slices)
for _, b := range table.Backends {
    fmt.Println(b.Endpoint.IP, b.Weight)
}
```

## 测试

`go test ./...`（含 `-race` 验证）覆盖：CPU 不均衡触发 Safeguard 全局回退及
各退化原因、缺口区跨区最小过载补齐、`Local` 策略覆写与本节点终止兜底、
全区无 Ready 时 `serving+terminating` 兜底（含 hints 下的本区优先）、
多 EndpointSlice 去重合并、权重归一化与分配确定性。
