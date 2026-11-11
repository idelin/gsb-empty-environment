# 敏感序列时间混淆

事件发布服务希望延迟事件以隐藏精确发生时刻，又不能改变必须保留的因果关系。实现供Go隐私宿主调用的时间分桶包，输入整数时间、依赖边与每事件最大延迟，选择最少公开时间桶并最小化总延迟；最多十八事件，边要求公开时间不倒退。给出合法方案或不可行证据，用小时间域穷举核对。

## 模型

- 事件 `i` 的公开时间 `b_i` 必须落在窗口 `[t_i, t_i+d_i]` 内，延迟为 `b_i-t_i`。
- 依赖边 `(u,v)` 要求 `b_u <= b_v`（公开时间不倒退，同桶允许）。
- 目标按字典序优化：先最小化不同公开时间（桶）的数量，再最小化总延迟。

## 算法

1. **可行性判定**：以最长路径松弛传播最早可公开时间
   `L_i = max(t_i, max_{(u,i)} L_u)`。若 `L_i > t_i+d_i` 则不可行，
   并沿松弛父指针还原从源头事件到 `i` 的传播路径作为证据（`InfeasibleError`）。
2. **子集 DP**：一个桶对应一个事件子集，组内最便宜的合法公开时间为
   `lo(G)=max_{i∈G} t_i`，要求它不超过组内最紧截止时间
   `hi(G)=min_{i∈G}(t_i+d_i)`；同时组内事件的所有因果祖先必须在同一子问题内。
   每个掩码只枚举包含“最晚事件（平局取最小下标）”的末组，保证覆盖全部划分且
   桶时间严格递增。复杂度 O(3^n)，n ≤ 18；典型机器约 0.5 秒、约 16 MB。

## 用法

```go
import "example.org/netsec/q0447"

sol, err := netsec.Solve(
    []int{1, 5},          // 事件发生时间
    []int{10, 10},        // 每事件最大延迟
    [][2]int{{0, 1}},     // 因果依赖边
)

var infeasible *netsec.InfeasibleError
if errors.As(err, &infeasible) {
    // infeasible.Witness 是证明不可行的事件链（源 -> 违反事件）
} else {
    // sol.Buckets / sol.Groups / sol.DistinctBuckets / sol.TotalDelay
}
```

## 验证

- `TestExhaustiveOracle` / `TestExhaustiveGrid`：在小时间域上枚举全部合法
  公开时间组合，独立求出字典序最优，与 DP 输出逐项比对（含不可行情形与证据校验）。
- `TestRandomLarger`：5–18 事件随机用例，独立复核窗口、因果边、分组与统计量。
- `BenchmarkSolve18`：18 事件规模性能基准。
