多集群工作负载联邦调度器（Karmada 风格）需要把全局 `replicas` 按权重、集群可调度容量上限、跨区域分布约束与最小迁移原则拆分到多个 Kubernetes 成员集群中，朴素按权重取整会同时破坏守恒与稳定性：1）**静态权重与容量上限递归溢出分配**：给定总副本数 `R`、各候选集群的权重 `w_i` 与硬容量上限 `cap_i`（由各集群剩余 CPU/内存除以单副本需求向下取整得出！），分配必须满足每集群 `0 <= alloc_i <= cap_i` 且 `sum(alloc_i) == R`（若 `sum(cap_i) < R` 返回明确容量不足与最大可分配方案！）；当某集群按权重应得份额超过其 `cap_i` 时，锁定该集群为 `cap_i` 并把溢出副本按其余未饱和集群的相对权重迭代重分配（每轮小数余数严格采用最大余数法 + 集群名字典序确定性分配！）；2）**跨故障域（Region/Zone）上下限约束**：支持 `SpreadConstraints`（限定单个 Region 承担的副本数区间 `[minReplicas, maxReplicas]` 或最小覆盖 Region 数 `minGroups`！），在满足区域约束与单集群上限前提下求解可行分布；3）**最小迁移再平衡（Minimal Disruption Rebalance）**：给定上一次调度的既有分布 `existing_i`，当 `R` 变更、个别集群容量缩水或权重调整时，支持 `KeepExisting` 最小迁移模式——绝不可为了微小权重漂移而大规模跨集群驱逐在跑副本，必须先冻结所有未超新上限的存量副本，仅缩减超上限集群的多余副本或把新增/释放出的差量按权重补齐到未饱和集群（严格验证迁移总副本数 `sum(max(0, existing_i - new_i))` 达到约束下理论极小！）。为多集群控制面交付可导入的 Go 副本权重递归分配、故障域约束与最小迁移调度包。验收覆盖多轮容量上限溢出重分配与总副本守恒、最大余数确定性、跨 Region `minGroups`/`maxReplicas` 约束、`KeepExisting` 最小迁移数极小性证明以及总容量不足精确诊断。

## 交付内容

可导入包 `fedscheduler`（模块 `prescreen.local/ps-4cf6c31a4a`），仅依赖 Go 标准库：

- `fedscheduler/apportion.go`：硬容量上限 + 静态权重的递归溢出分配；每轮最大余数法（Hamilton）按集群名字典序决胜，全程整数 / `math/big` 精确运算。
- `fedscheduler/spread.go`：Region/Zone 两级故障域约束，支持 `ReplicasRange`（`[MinReplicas, MaxReplicas]`）与 `MinGroups` 最小覆盖域数。
- `fedscheduler/rebalance.go`、`fedscheduler/rebalance_constrained.go`：`KeepExisting` 最小迁移再平衡，基于“自由副本池”模型，仅执行约束强制的驱逐，迁移数 `sum_i max(0, existing_i-new_i)` 达到理论极小。
- `fedscheduler/errors.go`：`ErrInsufficientCapacity` / `ErrInvalidInput` 哨兵错误；容量不足时 `*InsufficientCapacityError` 携带 `Requested`、`Capacity`、`Deficit` 与饱和的最大可分配 `MaxPlan`。

### 公开 API

```go
func Allocate(replicas int64, clusters []Cluster) (*Allocation, error)
func AllocateWithSpread(replicas int64, clusters []Cluster, constraints []SpreadConstraint) (*Allocation, error)
func Rebalance(replicas int64, clusters []Cluster, existing map[string]int64, mode RebalanceMode) (*RebalanceResult, error)
func RebalanceWithSpread(replicas int64, clusters []Cluster, existing map[string]int64, mode RebalanceMode, constraints []SpreadConstraint) (*RebalanceResult, error)
```

### 验收测试

`go test ./...` 覆盖：多轮容量溢出重分配与守恒、最大余数确定性（含独立教科书 LRM 参考实现对拍）、跨 Region/Zone 的 `minGroups` 与 `maxReplicas/minReplicas`、容量不足精确诊断，以及三组全枚举暴力校验——无约束、Region 上限、`minGroups` 场景下逐一证明 `KeepExisting` 的迁移数等于所有可行方案中的全局最小值。
