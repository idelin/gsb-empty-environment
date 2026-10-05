# frsimp

折线简化库：保留首尾点，在**调用方指定的距离度量**下，保证原折线到简化折线的
**离散 Fréchet 距离**不超过 ε。优化目标为先**最少保留点数**，再**索引字典序最小**。
每个结果都附带**匹配证据**（单调耦合）；计算预算耗尽时返回**可行上界**。

判据是货真价实的离散 Fréchet 距离——不用垂距阈值冒充。`TestOrderMatters`
锁定了一个垂距为 0.5 而 Fréchet 距离 ≥ 5 的用例，防止此类回退。

## 用法

```go
points := []frsimp.Point{{0, 0}, {5, 0}, {5, 0.5}, {0, 0.5}}
res, _ := frsimp.Simplify(points, frsimp.Options{
    Epsilon: 1.0,
    Metric:  frsimp.Euclidean, // nil 默认欧氏；可传任意 func(a, b Point) float64
    Budget:  0,                // <=0 不限；>0 为距离求值次数上限
})
res.Indices  // 保留的原索引，含 0 与 n-1
res.Matching // 证据：单调耦合，每对距离 <= Epsilon
res.Distance // 证据上的实际最大距离
res.Optimal  // false 表示预算耗尽，点数是可行上界
```

## 保证的模型

判据基于如下等价性（两个方向均可构造证明）：

> 子序列 c₁=0 < c₂ < … < c_m=n-1 满足 dF(P, P') ≤ ε，当且仅当 [0, n-1]
> 可划分为 m 个连续非空块，第 s 块全部落在 points[c_s] 的 ε 球内。

注意全局耦合可以把点匹配到"所属段"之外的保留点，因此按相邻保留点切段
（shortcut 图）的常用做法只是上界而非精确判据；本库不做这种近似。
块划分本身就是返回的匹配证据。

## 算法

- 懒计算 reach 表：`reach[w][l]` = 使 [l, r] 全在 w 的 ε 球内的最大 r
  （双指针，每行 O(n) 次度量求值）。
- 按块数做层级 BFS，状态为（最后保留点, 已覆盖前缀），Pareto 前沿剪枝，
  得到最少块数 m。
- 自后向前计算各层"可完成"状态集合（支配关系：v 更小、e 更大者占优）。
- 自前向后贪心选取仍能完成的最小索引，得到字典序最小解。
- 预算在任意阶段耗尽 → 贪心最远覆盖 + 恒等子序列兜底（单点块零误差），
  返回 `Optimal=false` 的可行上界，证据依然满足 ε 保证。

复杂度：O(n²) 次度量求值、O(n²) 内存、最坏 O(n³) 时间；预算机制用于
在大输入上受控降级。

## 核对工具

- `DiscreteFrechet(a, b, metric)`：标准 DP 计算离散 Fréchet 距离并回溯见证耦合。
- `BruteForce(points, opts)`：枚举全部子序列（n ≤ 18），用真实 dF 逐一判定，
  给出基线最优解。
- `Verify(points, opts)`：交叉核对 `Simplify` 与枚举结果（点数与字典序完全一致）。
- `CheckMatching(points, indices, matching, eps, metric)`：独立校验证据合法性
  （端点、单调性、逐对距离）。

随机小折线（含大量重复点的用例）上，`Verify` 以枚举为基准逐例核对，
见 `simplify_test.go`。
