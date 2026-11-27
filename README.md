Kubernetes DRA（Dynamic Resource Allocation）用结构化的 `ResourceSlice`、`DeviceClass` 与 `ResourceClaim` 取代传统标量扩展资源，使调度器能在单次节点评估内完成多维异构硬件（如 GPU 显存/互联域/驱动版本、FPGA 分区）的组合匹配与事务分配，四处调度约束极易出错：1）**多请求组合分配与互斥去重**：单份 `ResourceClaim` 可包含多个具名 `DeviceRequest`（支持 `AllocationMode: ExactCount(N)` 与 `All` 两种模式！），每个请求引用一个 `DeviceClass` 并叠加自身属性/容量过滤表达式（支持语义版本比较 `semver`、字符串/整型/布尔相等与范围比较及容量下限！）；同一 `ResourceClaim` 内不同请求分配到的具体 `(Driver, Pool, Device)` 绝不可重复冲突，且当节点上存在多个满足条件的设备组合时必须回溯搜索避免贪心局部死锁（例如 Request A 选了唯一满足 Request B 约束的设备导致整体无解！）；2）**跨设备联合约束（`MatchAttribute` Constraint）**：支持 `ResourceClaim` 级约束要求指定一组请求的已选设备必须共享同一属性值（例如同一 NVLink `fabricDomain` 或同一 PCIe `numaNode`！），仅当存在满足全部联合约束的分配方案时节点才通过 Filter 阶段；3）**Pool 代次（Generation）完整性校验**：每个 `(Node, Driver, Pool)` 的 `ResourceSlice` 携带 `(Generation, ResourceSliceCount)`，若某 Pool 在当前最高 `Generation` 下收到的分片数少于 `ResourceSliceCount`（拓扑上报尚不完整！），调度器必须忽略该不完整 Pool 绝不可基于残缺视图做错误分配；4）**共享声明引用计数与 Reserve/Unreserve 事务回滚**：多个 Pod 可同时引用同一已分配 `ResourceClaim`（记录 `ReservedFor` 集合，不重复扣减硬件设备！），而专属 `ResourceClaimTemplate` 在 Pod 绑定失败 `Unreserve` 或删除时必须立即释放所有已占用设备供其他待调度 Pod 使用。为异构集群调度器交付可导入的 Go DRA 结构化设备匹配、联合约束回溯求解与声明事务管理包。验收覆盖贪心陷阱下的回溯成功分配、`MatchAttribute` 同互联域联合约束、不完整 `Generation` Pool 自动屏蔽、`All`/`ExactCount` 双模式以及共享声明引用计数与 `Unreserve` 精确回滚。

## 交付物：`dra` 包

纯 Go 标准库实现（无外部依赖），导入路径
`prescreen.local/ps-20d98bbcc0/dra`，类型镜像 Kubernetes DRA
`resourceapi` 的关键结构，可直接嵌入调度器的 Filter/Reserve/Unreserve
阶段。

- `dra/types.go`：`ResourceSlice`、`ResourcePool`、`Device`、
  `DeviceClass`、`ResourceClaim`、`DeviceRequest`、`Selector`、
  `MatchAttributeConstraint`、`AllocationResult` 与全部哨兵错误。
- `dra/semver.go`：SemVer 2.0 解析、优先级比较（含 prerelease）与
  caret（`SemverCompatible`）兼容范围。
- `dra/selector.go`：属性表达式求值（bool/int64/string/version 的相等、
  不等、范围、`In`/`NotIn`、semver 兼容）与容量下限/范围比较。
- `dra/pool.go`：按 `(Node, Driver, Pool)` 合并 `ResourceSlice`，
  以最高 `Generation` 为准并在分片数 `< ResourceSliceCount` 时将 Pool
  标记为不完整，新 Generation 到达即清空旧视图。
- `dra/solver.go`：回溯求解器，支持 `All`（整池匹配设备一次性分配）与
  `ExactCount(N)`（枚举所有 N 元组合），以 `(Driver, Pool, Device)`
  全局去重并排除已占用设备，增量校验 `MatchAttribute` 联合约束。
- `dra/scheduler.go`：线程安全的调度器门面 `Scheduler`，提供
  `ApplySlice`/`RemoveSlice`、`AddClass`/`AddClaim`、`Filter`、
  `Reserve`、`Unreserve`、`DeleteClaim`、`PoolStatuses` 与
  `EnsurePoolsComplete`。

### 四大机制如何落地

1. **多请求组合与互斥去重**：每个请求先按 DeviceClass 与请求自身
   Selector 求候选集；DFS 按请求顺序枚举，进入下一层前把已选
   `(Driver, Pool, Device)` 写入 `used` 集合，冲突即剪枝，因此贪心先选
   唯一设备导致的局部死锁会通过回溯找到全局可行解。
2. **MatchAttribute 联合约束**：每分配一个请求就重算约束，要求所有已选
   设备都具备该属性且取值相等；任一分支违反立即回退。
3. **Generation 完整性**：不完整 Pool 不会进入求解器（自动屏蔽），
   `EnsurePoolsComplete` 可显式返回包装了 `ErrIncompletePool` 的诊断。
4. **引用计数事务**：`Reserve` 对已分配声明只追加 `ReservedFor`
   （共享声明不重复扣硬件），首次分配才占用设备；`Unreserve` 对专属
   声明立即释放全部设备，对共享声明在最后一个消费者离开时释放，
   `DeleteClaim` 无条件释放。

### 快速上手

```go
sched := dra.NewScheduler()
sched.AddClass(dra.DeviceClass{Name: "gpu"})
sched.ApplySlice(dra.ResourceSlice{
    Name: "nv-pool-0", NodeName: "node-a", Driver: "nv",
    Pool: dra.ResourcePool{Name: "pool", Generation: 1, ResourceSliceCount: 1},
    Devices: []dra.Device{{
        Name:       "gpu0",
        Attributes: map[string]dra.AttributeValue{"model": dra.StringValue("a100")},
        Capacities: map[string]int64{"memory": 80},
    }},
})

claim := &dra.ResourceClaim{
    Namespace: "default", Name: "job",
    Requests: []dra.DeviceRequest{{
        Name: "g", ClassName: "gpu",
        AllocationMode: dra.AllocationModeExactCount, Count: 1,
        Selectors: []dra.Selector{{
            CapacityRequirements: []dra.CapacityRequirement{
                {Name: "memory", Op: dra.OpCapacityGte, Value: 80},
            },
        }},
    }},
}
ok, err := sched.Filter("node-a", claim)
if err == nil && ok {
    _ = sched.Reserve("node-a", claim,
        dra.Consumer{UID: "p1", Namespace: "default", Name: "pod1"})
}
```

### 测试

```bash
go test -race -cover ./dra/...
```

`dra/*_test.go` 覆盖五项验收场景：贪心陷阱回溯
（`TestGreedyTrapBacktracking`）、同互联域联合约束正反例
（`TestMatchAttributeSameFabric`）、不完整 Generation 屏蔽与恢复
（`TestIncompleteGenerationPoolMasked`）、`All`/`ExactCount` 双模式
（`TestAllocationModes`）、共享引用计数与 `Unreserve` 精确回滚
（`transaction_test.go`），另含 semver、选择器、错误路径与 32 路并发
分配测试。
