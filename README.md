# matdiag — 小型矩阵数值诊断工具

解稠密线性方程组 `A x = b`（部分主元 LU），并给出数值诊断：残差、
后向误差、条件数估计，以及**尺度感知**的奇异判定。可选对角缩放。

## 运行

```sh
go run .      # 演示 5 个典型案例
go test -v .  # 单元测试（含 big.Rat 精确参考解对照）
```

## 方法

- **求解**：`Factorize` 做部分主元 LU（`P A = L U`），`Solve`/`SolveTranspose`
  做前代回代与转置求解。
- **条件数**：Hager/Higham 1-范数估计，`cond1 ≈ ‖A‖₁·‖A⁻¹‖₁`，只需求解
  若干次三角形系统，O(n²)。
- **残差与后向误差**：始终在**原始**（未缩放）系统上评估。
  - 分量后向误差（Oettli–Prager）：`η = max_i |r_i| / (|A||x| + |b|)_i`，
    即使 `x` 成为某个逐分量邻近问题的精确解所需的最小相对扰动。
  - 范数后向误差：`‖r‖∞ / (‖A‖∞‖x‖∞ + ‖b‖∞)`。
  - 前向误差大致满足 `relerr ≲ cond · η`，所以"残差小"不等于"解准"，
    必须用条件数解释两者之间的缺口（见演示 3）。

## 近奇异 vs 真奇异（不用固定绝对阈值）

- **真奇异**：消元中某一列的对角及以下**精确为零** → 报 `Singular`，
  不返回解。
- **近奇异**：相对判据，二选一触发 → 报 `NearSingular`，仍返回尽力解：
  - 主元 `< n·eps·‖A‖∞`（阈值随矩阵自身尺度缩放）；
  - 或 `rcond = 1/cond1 < n·eps`（工作精度下奇异）。
- 因此 `1e-200·I`（条件数 1）不会被误判为奇异，而 `1e200` 尺度的矩阵
  也用同一把相对尺子衡量（演示 5 / `TestNoAbsoluteThreshold`）。

## 对角缩放及变量映射

`Equilibrate` 先做行平衡再做列平衡，所有缩放因子取 2 的幂（二进制下
缩放本身无舍入）。求解的是缩放后的系统

```
(Dr·A·Dc) y = Dr·b
```

映射回原变量：

```
x = Dc·y        （即 x[i] = Col[i]·y[i]）
```

残差、后向误差一律代回原始 `A, b, x` 计算。行交换（主元置换 `P`）只
影响分解内部，对外不可见。演示 2 中尺度悬殊 1e20 的矩阵：未缩放时
分量后向误差 0.33、解完全错误；缩放后精确复原。

## 精确参考

`ExactSolve` 用 `math/big.Rat` 对 float64 输入的**精确二进制有理值**
做高斯消元，给出精确解与精确奇异性，作为测试中的前向误差基准。
`ExactMatVec` 精确计算 `b = A·x` 再舍入，用于构造已知精确解的算例。

## 测试覆盖（diag_test.go）

- `TestRowExchange`：首主元为 0，必须行交换，且验证确实发生了交换。
- `TestScaleDisparity`：尺度差 1e20，未缩放误差 100%，缩放后 ~0；
  并验证 `x = Dc·y` 映射与报告解一致。
- `TestSmallResidualLargeError`：Hilbert(10)，残差 ~3e-16、后向误差
  ~6e-17，但前向误差 ~2e-4，且 `relerr ≲ cond·η` 成立。
- `TestSingularVsNearSingular`：秩亏矩阵报 `Singular` 且不解；
  行几乎重合的矩阵报 `NearSingular` 但仍给解。
- `TestNoAbsoluteThreshold`：`1e±200·I` 均不误报奇异。
- `TestCondEstimate`：Hilbert(6) 条件数估计落在真值量级内。
