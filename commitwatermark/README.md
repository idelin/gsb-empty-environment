# commitwatermark

Raft 提交水位（`commitIndex`）计算包。适用于成员变更中途切主、日志前缀上
生效配置动态变化（含联合共识 `C_old,new` 与非投票学习者）的场景。

## 提交规则（与 Raft 论文一致）

对候选索引 `k`，仅当同时满足以下条件时，新主才可把 `commitIndex` 推进到 `k`：

1. `k` 处条目任期等于新主当前任期（旧任期条目不能直接提交，只能随当前任期
   条目被间接提交）。
2. 区间 `(PrevCommit, k]` 内经历过的**每一个生效配置**都已确认复制到 `k` 及
   以上：
   - 单配置：该配置投票者过半（`len(Voters)/2 + 1`）。
   - 联合配置 `C_old,new`：`C_old` 与 `C_new` **各自**过半（双多数）。
3. 配置条目一旦写入，即从自身索引起生效。
4. 学习者只接收日志，其确认永不计入多数。

副本的确认资格还受持久任期约束：

- 节点 `PersistedTerm > CurrentTerm`（领先于新主）时完全不计。
- 节点虽声称较高 `MatchIndex`，但 `PersistedTerm` 未覆盖到该索引条目任期时，
  其有效匹配被截断到最后一个任期不超过 `PersistedTerm` 的条目前缀。

## 返回值

`Analyze(Input) (Result, error)` 返回：

- `Result.CommitIndex`：精确的最大安全提交水位，绝不回退。
- `Result.TargetIndex`：日志中最后一个当前任期条目（本次推进的理论上限）。
- `Result.Bottlenecks`：在 `TargetIndex` 处未达门槛的每个法定人数组，含所属
  生效区间、联合侧标识（`old`/`new`）、投票者、确认者与多数阈值。

## 用法

```go
import "prescreen.local/ps-3288818f8b/commitwatermark"

result, err := commitwatermark.Analyze(commitwatermark.Input{
    CurrentTerm: 5,
    Leader:      "n1",
    Initial:     commitwatermark.Config{Voters: []commitwatermark.NodeID{"n1", "n2", "n3"}},
    Entries: []commitwatermark.LogEntry{
        {Index: 1, Term: 4},
        {Index: 2, Term: 5, Change: &commitwatermark.ConfigChange{
            Kind: commitwatermark.ChangeEnterJoint,
            Old:  commitwatermark.Config{Voters: []commitwatermark.NodeID{"n1", "n2", "n3"}},
            New:  commitwatermark.Config{Voters: []commitwatermark.NodeID{"n2", "n3", "n4"}},
        }},
        {Index: 3, Term: 5, Change: &commitwatermark.ConfigChange{
            Kind: commitwatermark.ChangeLeaveJoint,
            New:  commitwatermark.Config{Voters: []commitwatermark.NodeID{"n2", "n3", "n4"}},
        }},
    },
    Replicas: map[commitwatermark.NodeID]commitwatermark.ReplicaState{
        "n1": {MatchIndex: 3, PersistedTerm: 5},
        "n2": {MatchIndex: 3, PersistedTerm: 5},
        "n3": {MatchIndex: 3, PersistedTerm: 5},
        "n4": {MatchIndex: 3, PersistedTerm: 5},
    },
})
```

## 验收

- 定向用例：主不在新配置、学习者不计多数、旧任期日志被当前任期配置变更间接
  提交、连续两条配置日志未提交时双多数同时受阻、持久任期截断、`PrevCommit`
  窗口、非法输入与空日志。
- `TestStateMachineEnumeration`：用显式状态机枚举合法故障切换历史（普通写入、
  切主任期跳变、单配置切换、进入/离开联合），对每个历史注入随机复制状态
  （含落后任期、领先任期、截断前缀），由独立参考实现逐条对照提交水位、目标
  索引与瓶颈集合，并断言前缀闭合、当前任期锚点、最大性、学习者不变性等。
