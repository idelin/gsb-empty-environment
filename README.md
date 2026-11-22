# changeaccept

`changeaccept` 是面向 Go 网络控制器的可导入变更验收包。它只对受管租户之间的授权建模，并把策略下发视为入口/出口规则与标签变化图的组合。

## 语义

- 策略是 `Policy`，由有序 `Rule{Match, Action}` 组成；第一条匹配规则生效，无匹配则默认拒绝。
- 一条跳是 `u -> v`。当前位于 `u` 的标签先过 `u.Egress`，随后按 `u.Changes` 得到新标签，再过 `v.Ingress`。任一侧拒绝都没有这条跳。
- `LabelGraph` 为 `nil` 表示所有标签原样通过；非 nil 表示严格图，未列出的标签没有后继。需要显式保留标签时，把标签映射到自身。
- 端到端授权是 `Authorizer` 中的有序 `PairPolicy`，同时匹配发起标签和到达标签；未配置的租户对、未受管租户均拒绝。
- 有向拓扑可含环。验证在 `(端点, 当前标签, 发起标签, 是否已走一跳)` 的有限乘积上收敛，不会因环无限展开。
- 未显式指定 `Input.Pairs` 时，默认检查所有不同受管租户的有序对；指定时只检查给定范围。

通配规则无法枚举无限标签空间，因此初始标签域由 `Input.Labels` 指定。非空时它是精确的初始集合；为空时从端点规则、变化图和授权规则推断。探活与活跃连接中出现的具体标签始终会加入该域。

## 基本用法

```go
import changeaccept "prescreen.local/ddd-1574-20261122-v2"

auth, err := changeaccept.NewAuthorizer(
    []changeaccept.TenantID{"payments", "search", "shared"},
    map[changeaccept.TenantPair]changeaccept.PairPolicy{
        {Source: "payments", Destination: "search"}: {
            {
                Source:      changeaccept.AnyLabel(),
                Destination: changeaccept.AnyLabel(),
                Action:      changeaccept.ActionAllow,
            },
        },
    },
)

input := changeaccept.Input{
    BaseGeneration: 40,
    Topology: changeaccept.Topology{
        Endpoints: []changeaccept.Endpoint{
            {
                ID: "edge-a", Tenant: "payments",
                Egress: changeaccept.Policy{{
                    Match: changeaccept.Labels("gold"),
                    Action: changeaccept.ActionAllow,
                }},
            },
            {
                ID: "edge-b", Tenant: "shared",
                Ingress: changeaccept.Policy{{
                    Match: changeaccept.Labels("gold"),
                    Action: changeaccept.ActionAllow,
                }},
                Egress: changeaccept.Policy{{
                    Match: changeaccept.Labels("gold"),
                    Action: changeaccept.ActionAllow,
                }},
            },
            {
                ID: "edge-c", Tenant: "search",
                Ingress: changeaccept.Policy{{
                    Match: changeaccept.Labels("gold"),
                    Action: changeaccept.ActionAllow,
                }},
            },
        },
        Edges: []changeaccept.Edge{
            {From: "edge-a", To: "edge-b"},
            {From: "edge-b", To: "edge-c"},
        },
    },
    Auth:   auth,
    Labels: []changeaccept.Label{"gold"},
    Pairs: []changeaccept.TenantPair{
        {Source: "payments", Destination: "search"},
    },
    Probes: []changeaccept.Probe{{
        ID: "payments-to-search",
        Pair: changeaccept.TenantPair{Source: "payments", Destination: "search"},
        Source: "edge-a", Destination: "edge-c",
    }},
}

report, err := input.Check()
```

`Check` 返回每个前缀的 `PrefixReport`，包括空前缀。总体结果是：

- `OutcomeCompliant`：所有前缀的所有可达连接均在端到端关系内，且探活成功。
- `OutcomeNonCompliant`：至少一个前缀存在确定的反例。
- `OutcomeUndetermined`：预算耗尽且尚未得到确定反例；绝不能当作合规或拒绝。

`Accept` 先检查候选顺序；若确定不合规，则搜索满足依赖的最少相邻交换修复顺序。同分时按组 ID 字典序选择。`AcceptanceReport.Given` 保留原始结论，`Recommended` 给出修复结果。

## 原子切换组

- 最多 16 个组。
- 每个组可同时修改多个端点的多个平面，组内一起生效。
- 每个 `(端点, 平面)` 只能由一个组替换，避免产生不可判定的隐式顺序。
- `DependsOn` 指定前置组；依赖不能成环，候选顺序若违反依赖会被拒绝，`Accept` 仍可求修复顺序。
- `PolicyChange.Old` 可选；设置后必须等于基础拓扑中的旧策略，`New` 是替换值。

重排采用 `2^16` 子集动态规划。目标函数是相对输入候选顺序的逆序数，也就是最少相邻交换次数；同时要求每个中间前缀都合规。

## 连接跟踪

`Tracker` 只缓存已成功核对的完整具体跳。缓存键包含：

- 策略代和源/目的租户；
- `ConnectionID`、起止端点、起止标签；
- 完整路径前缀与标签历史的 SHA-256 摘要；
- 可选的快照指纹，用于区分数值代相同的不同候选分支。

因此同一中间节点来自不同源租户时不会共享状态，旧标签历史也绝不会命中新历史的缓存。失败和格式非法的跳不会进入缓存。`ActiveConnection.VerifiedThrough` 可保留已有连接已核对的头部跳，但 `VerifiedHistory` 必须匹配对应标签历史前缀。

## 覆盖证书

`BuildCoverageCertificate` 为一个租户对生成完整可达状态/迁移图。证书包含：

- 快照指纹、策略代、租户对；
- 全部可达状态和全部启用的具体跳；
- 每个到达目的租户状态的端到端首匹配规则见证；
- 证书自身摘要。

```go
snapshot, err := input.BuildSnapshot(report.Order)
certificate, outcome, err := changeaccept.BuildCoverageCertificate(
    snapshot,
    changeaccept.TenantPair{Source: "payments", Destination: "search"},
    changeaccept.Budget{},
)
checked, err := changeaccept.CheckCoverageCertificate(snapshot, *certificate, changeaccept.Budget{})
```

`CheckCoverageCertificate` 不信任生成过程：它会从快照重新探索、比较完整状态与迁移集合、复算每条端到端见证和两个摘要。覆盖不完整、摘要不符或见证被篡改都会返回错误；完整覆盖但存在拒绝见证时返回 `OutcomeNonCompliant`。

## 预算

`Budget` 上限分别约束状态数、迁移数和重排子集数。任何 `ErrBudgetExhausted` 都会显式映射到 `OutcomeUndetermined`。已经找到确定反例时，剩余探索即使被预算截断，结论仍可为 `OutcomeNonCompliant`，因为反例本身已经充分。

## 测试

测试覆盖了：

- 首匹配与默认拒绝；
- 出口/入口双平面及标签改写；
- 有向环上的收敛、误放与误拒；
- 同一中间节点按源租户分离状态；
- 全部三组切换事件交错；
- 依赖约束下的最少相邻交换和组 ID 同分规则；
- 缓存对策略代、租户、具体跳、标签历史的绑定；
- 活跃连接已验前缀；
- 证书独立复算、篡改检测和预算未定。
