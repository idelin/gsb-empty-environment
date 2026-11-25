# handshake prescreen

可导入的 Go 判定包，供配置发布器在接受握手策略滚动发布计划时调用。

## 设计要点

- `bdd`：固定变量序的 ROBDD，变量索引即决策层，唯一表保证句柄相等即函数等价；
  支持 `And/Or/Not/Xor/Ite/Restrict`、表达式解析、最小满足赋值，全程不枚举
  2^N 能力向量，最大 48 位。
- 根包 `handshake`：
  - `RuleSet` 实现首匹配语义，`Regions/Partitions` 给出互不相交的能力分区和
    等价规则划分，未命中任何规则视为拒绝。
  - `Topology` 维护回退序列、每节点新旧规则与合法能力 BDD；`Evaluate` 支持
    普通连接（start=0）和会话票据重连（从票据目标节点 start=k 开始，受当前
    生效规则重验），只有拒绝才继续回退。
  - `SymbolicResult` 用 BDD 计算每个结果对应的能力集合；`CheckStates` 对所有
    可达中间态和所有回退起点检查两条性质：
    1. 中间结果不得严格弱于全旧和全新两端；
    2. 两端都能连接时中间态不得拒绝。
    反例返回按位序（位 0 为最高位、false<true）最小的合法能力向量。
  - `Schedule` 在最多 14 节点上做逐层 BFS，处理先后依赖、同批原子组和每个
    维护窗口的可切换节点集合，优化目标为使用窗口最少，平局按每批排序后的节点
    编号序列取字典序最小。
  - 无解时返回结构化 `Blocker`，`VerifyBlocker` 可独立复核；`VerifyPlan` 可
    独立复核一个方案的窗口、原子组、依赖、单次切换和中间态安全性。

## 最小用法

```go
manager, _ := bdd.NewManager(4)
strong, _ := manager.Variable(0)

topology, _ := handshake.NewTopology(
    manager,
    []handshake.NodeConfig{{
        NodeID: 1,
        Old: handshake.RuleSet{Rules: []handshake.Rule{
            {When: bdd.One, Outcome: handshake.AuthOutcome(3)},
        }},
        New: handshake.RuleSet{Rules: []handshake.Rule{
            {When: strong, Outcome: handshake.AuthOutcome(3)},
            {When: bdd.One, Outcome: handshake.RejectOutcome()},
        }},
    }},
    []int{1},
    bdd.One,
    nil,
)

result, _ := handshake.Schedule(handshake.SchedulingInput{
    Topology:    topology,
    WindowNodes: [][]int{{1}},
})
if result.Blocker != nil {
    // result.Blocker 是可核验的阻断依据
}
```

## 测试

`go test ./...` 包含不超过 6 位的穷举模型，覆盖拒绝回退、同批可行而拆开不可
行、等价规则划分、零个合法客户端，并额外用 48 位符号模型和 14 节点压力模型
验证不枚举全部能力向量。
