# markov

有限马尔可夫链的精确分析库（Go，基于 `math/big.Rat` 有理数运算，无浮点误差）。

## 功能

- 输入有理数转移概率矩阵，校验：方阵、元素落在 `[0,1]`、每行和恰为 1。
- 给定目标吸收集合 `A`，对每个起点 `i` 输出：
  - **命中概率** `h_i = P_i(T_A < ∞)`；
  - **条件期望步数** `E_i[T_A | T_A < ∞]`（`h_i = 0` 时为 `nil`）；
  - **状态分类**，区分三种情形：
    - `NeverReaches`：`h_i = 0`，永不到达（例如处于与 `A` 不相交的闭合类）；
    - `InfiniteExpectation`：`0 < h_i < 1`，无条件期望步数无穷（条件期望仍有限）；
    - `CertainAbsorption`：`h_i = 1`，几乎必然吸收且期望有限。

## 方法

1. 在支撑图上从 `A` 反向 BFS，得到可达集 `R`；`R` 外状态 `h = 0`。
2. 在自由状态 `S = R \ A` 上解线性方程组（精确高斯消元）：
   - 命中概率：`(I − Q) h = r`，`r_i = Σ_{j∈A} P[i][j]`；
   - 加权期望：`g = h + Q g`，其中 `g_i = E[T·1{T<∞}]`。
3. 条件期望 `E[T|T<∞] = g_i / h_i`。

把方程组限制在可达集上可避开闭合非吸收类造成的 `I − Q` 奇异；
有限链上 `h_i > 0` 时条件期望必有限。

## 用法

```go
p, err := markov.ParseMatrix([]string{
    "1   0   0   0",
    "1/4 1/4 1/2 0",
    "0   0   0   1",
    "0   0   1   0",
})
chain, err := markov.NewChain(p) // 校验每行和为 1
res, err := chain.Analyze([]int{0})
// res[1]: HitProb = 1/3, ConditionalExpectedSteps = 4/3, InfiniteExpectation
// res[2], res[3]: HitProb = 0, ConditionalExpectedSteps = nil, NeverReaches
```

## 测试

```sh
GO111MODULE=on go test ./...
```

测试全部用手算可验证的小链：几何等待时间、含闭合非吸收类 `{2,3}` 的链
（`h=1/3`、条件期望 `4/3`，并用 `E[T|hit] = 3·Σ t(1/4)^t` 交叉验证）、
回环链、多吸收态、不可达目标，以及非二进有理数的精确性检查。
