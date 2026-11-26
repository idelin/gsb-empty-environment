大规模对象存储在发生多盘同时离线或机架短暂掉电后，数百个不同 `(k, m)` 纠删码与多副本归置组（PG）同时进入降级态；若修复调度器不按剩余容错裕度排序、把重建分片错放到与同条带存活分片共享同一故障域（同主机或同机架）的目标盘上，或者忽略各机架顶交换机（ToR）上下行带宽上限，就会在修复中途引发二次掉盘数据丢失或打爆跨机架链路拖垮前台业务。为存储集群控制面交付可导入的 Go 故障域修复规划与带宽调度包。输入为层级拓扑树（Root -> Row -> Rack -> Host -> OSD，每节点带在线/离线状态、剩余容量及入/出带宽上限）、待修复条带集合（每条带含副本或 `(k, m)` 参数、当前分片分布及所在 OSD、强制故障域层级如 `rack` 或 `host` 以及分片大小）和全局并发修复槽位。规则：1）精确分级每个条带状态——存活分片 `< k` 标为 `UNRECOVERABLE` 不调度、存活分片 `>= k` 且 `< k+m` 按最小剩余裕度 `surviving - k` 升序（最濒危优先！）、次按条带大小降序、再按条带 ID 字典序排优先级；2）为每缺失分片选择合法目标 OSD：必须在线、容量充足、且其指定故障域祖先绝不与该条带任意存活或已规划目标分片重叠（无合法目标时报告容量/域不足而不违规放置！）；3）对 `(k, m)` 纠删码重建，从全部存活分片中选出恰好 `k` 个源盘使得跨机架读取流量最小（优先选与目标盘同机架的存活盘！），并在每个调度时间轮内严格满足所有涉及 OSD 与机架的入/出带宽上限及最大并发约束，按分轮时间线输出完整修复计划与每条带恢复完成时刻。验收覆盖濒危 `surviving == k` 优先抢修、故障域反亲和约束、跨机架最小流量源盘选取、ToR 带宽瓶颈分轮排队及不可恢复条带与域耗尽精确报告。

## 实现：`repairplan` 包

纯 Go 标准库实现，无第三方依赖，入口为 `repairplan.BuildPlan`。

### 包结构

- `repairplan/topology.go`：`Root -> Row -> Rack -> Host -> OSD` 拓扑树构建与校验，节点在线状态、剩余容量、入/出带宽上限、并发上限，以及 OSD 故障域祖先查询。
- `repairplan/stripe.go`：条带（EC 与多副本）模型与精确分级 `UNRECOVERABLE` / `DEGRADED` / `HEALTHY`，计算 `surviving`、缺失分片与裕度 `surviving-k`。多副本以 `k=1, m=replicas-1` 表达，可用 `NewReplicatedStripe` 构造。
- `repairplan/planner.go`：优先级排序、故障域反亲和目标选择、容量预留与回滚、最小跨机架流量的 `k` 源盘选择，以及不可放置原因（`NO_ONLINE_OSD` / `CAPACITY_EXHAUSTED` / `FAULT_DOMAIN_EXHAUSTED`）。
- `repairplan/scheduler.go`：分轮带宽与并发调度，按 LCA 精确计算每条链路的入/出字节，严格满足各节点 `IngressLimit` / `EgressLimit` / `MaxConcurrent` 与全局槽位；不可推进时返回 `ErrNoProgress`。
- `repairplan/render.go`：`RenderText` 输出可读的分轮时间线。
- `cmd/repairplan-demo`：端到端示例，可 `go run ./cmd/repairplan-demo`。

### 调度语义

- 优先级：裕度升序（`surviving==k` 最濒危最先）→ 条带大小降序 → 条带 ID 字典序。
- 目标 OSD：有效在线（自身及所有祖先在线）、容量足够、其故障域祖先不与任何存活/已规划分片重叠，且同一 OSD 不重复承载该条带分片；候选按剩余容量降序、OSD ID 升序确定性选取。
- 源盘选择：优先与目标同机架的存活盘，再补其他机架，恰好选 `k` 个；`CrossRackSources` 显式给出跨机架读取集合。
- 带宽：一条重建任务对每个经过的链路节点按一次分片大小计费（目标写入只计一次，不按源数重复），逐轮预算，超限任务排队到后续轮次。
- 输出：`Plan.Rounds` 为分轮时间线（含每轮任务与节点用量），`Plan.StripeReports` 给出每条带状态、失败原因与 `CompletedRound` / `CompletedAt`，`Plan.Unrecoverable` 列出不可恢复条带。

### 验证

`go test ./...` 覆盖：濒危优先与三级排序、rack/host 反亲和、域耗尽与容量耗尽精确报告、同机架源盘优先与跨机架源集合、ToR 出带宽瓶颈分轮排队、全局/节点并发、不可恢复与健康条带分级、入流量按任务计一次以及拓扑与输入校验。
