# quadopt

在整数盒 `{x : lo[i] <= x[i] <= hi[i]}` 上精确最小化整系数二次函数

    f(x) = C + G·x + xᵀQx

所有系数与函数值均为 `math/big.Int`，Q 可以不定（负曲率、交叉项均支持）。

## 用法

```go
f, _ := quadopt.NewQuadratic(c, g, q)        // q 必须对称，输入会被深拷贝
res, _ := quadopt.Solve(f, lo, hi, &quadopt.Options{MaxNodes: 100000})
// res.Status: Optimal / BudgetExhausted
// res.Point / res.Value: 最优点（平局按坐标字典序取最小者）与上界
// res.LowerBound: 全局下界；Optimal 时等于 Value
```

## 算法

- 最佳优先分支定界：按区间下界最小的节点出堆，沿最宽坐标二分。
- 下界为逐单项式的区间算术界：每个 `Q_ij·x_i·x_j` 在盒角点上取积的
  最小/最大值，再按系数符号组合。该界对不定 Q 依然合法，不依赖任何
  仅适用于正定情形的松弛（如特征值/SDP 界）。
- 剪枝用严格不等式（lb > 当前最优值才剪），保证等值时仍能找到坐标
  字典序最小的最优点。
- 预算耗尽时返回当前最优可行点（上界）与所有未决节点下界的最小值
  （全局下界）。

## 验证

`go test ./...` 覆盖：负曲率、单点盒、边界最优、纯交叉项、常数目标
平局、超 int64 大整数、随机小盒与全枚举（`BruteForce`）逐一比对，
并通过 `Options.OnPrune` 钩子对每个被剪节点的界做枚举核对，确认其
不超过该子盒真实最小值。
