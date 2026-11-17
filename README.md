# 网络隔离修复步骤安全性

演练平台修改网络连边以隔离受感染节点时，中间步骤不能短暂开放原本禁止的路径。写供Go控制宿主调用的更新规划包，输入至多十二条可变边、初始与目标开关状态及禁止到达对；每步只能翻转一条边，求每个中间图都满足约束的最短步骤，等长按边ID排序。给出无法安全迁移时的已遍历状态证据，用全状态BFS核对，动作只作用于内存网络模型。

## 使用

```go
import "example.org/netsec/q0120"

req := netsec.Request{
    Edges: []netsec.Edge{
        {
            ID:             "e1-infected",
            From:           "infected",
            To:             "gateway",
            InitialEnabled: true,
            TargetEnabled:  false,
        },
        {
            ID:             "e2-core",
            From:           "gateway",
            To:             "core",
            InitialEnabled: false,
            TargetEnabled:  true,
        },
    },
    Forbidden: []netsec.ForbiddenPair{
        {From: "infected", To: "core"},
    },
}

plan, err := netsec.Plan(req)
if err != nil {
    // 输入错误，例如超过 12 条可变边或边 ID 重复。
}
if !plan.Feasible {
    // plan.Failure.Code 为 initial_unsafe、target_unsafe 或 no_safe_path。
    // plan.Evidence 包含 BFS 已遍历的安全状态、边界阻断状态和完整状态总数。
} else {
    for _, step := range plan.Steps {
        // step.EdgeID、step.From、step.To 描述一次原子翻转。
    }
}
```

## 语义

- 可变边最多 `netsec.MaxMutableEdges`（12）条，完整状态空间不超过 4096。
- 边按 ID 字典序归一化，`State` 的第 `i` 位对应该序中的第 `i` 条边。
- 默认按无向边计算可达性；`Request.Directed` 为 `true` 时按有向边计算。
- 初始图、目标图和每个中间图都必须不包含任何 `ForbiddenPair` 的可达路径。
- 返回最短的逐边翻转序列；长度相同时返回边 ID 序列字典序最小者。
- `Plan` 只读取请求并构造内存结果，不修改请求，也不访问真实网络。
- 失败时 `Evidence.TotalStates` 是完整状态数，`Traversed` 是按 BFS 发现顺序记录的全部可达安全状态；`Blocked` 是搜索边界上因禁止到达对而拒绝的状态；`Complete` 表示该可达分量已穷尽。
- 成功结果可用 `netsec.Verify(req, plan)` 复核：它会重新执行全状态搜索，检查每一步的状态迁移、最短距离和字典序最优性。
