# dagadjust：计数 DAG 上的精确加权最小二乘平差

对树 / 计数 DAG（允许共享叶）上的带噪声计数做加权最小二乘（WLS）调整，
约束"父节点 = 子节点之和"，可选非负约束。全部计算使用 `fractions.Fraction`
精确有理运算（仅 Python 3.11 标准库，无第三方依赖），最优性只由精确 KKT
证书判定，**从不**以"迭代变化很小"作为收敛依据。

**仅处理合成数据。本包的任何统计实验结果都不构成形式化隐私证明。**

## 问题定义

    minimize    f(x) = 1/2 * sum_i w_i (x_i - y_i)^2
    subject to  x_p = sum_{c in children(p)} x_c   （每个内部节点 p）
                x_i = f_i                           （固定计数，可选）
                x >= 0                              （可选）

- `CountDAG`：节点 `0..n-1`，`children[p]` 为 p 的子节点。叶可被多个父节点
  共享（DAG，不必是树）。约束矩阵允许冗余，求解器用精确秩检测剔除并报告。
- `observed y_i`：节点 i 的噪声观测计数（合成数据）。
- `weights w_i >= 0`：对观测的信任度。**`w_i = 0` 的含义：该观测被忽略**，
  `x_i` 仅由约束决定；若因此导致系统欠定，求解器报 `singular` 并指出
  涉及的自由变量，而不是静默编造数值。
- `fixed`：精确已知计数 `x_i = f_i`（如已公布的总数）。
- `nonneg`：是否附加 `x >= 0`。

## 求解与状态

- 等式约束 WLS：直接精确求解 KKT 方程组。
- 非负约束：原始活动集法，每次迭代精确求解一个等式 QP；仅当当前点
  满足全部 KKT 条件时停止。
- `max_iterations`：EQP 求解次数上限。**达到上限时状态为
  `not_converged`，结果不能称为最优**（`x` 为 `None`）。
- 状态：`optimal`（KKT 证书精确验证通过）/ `not_converged` /
  `infeasible` / `singular`。`SolveResult.is_optimal` 仅在前者成立。

## 证书（Certificates）

每个 `optimal` 结果附带精确证书，字段全部为 `Fraction`：

- `max_eq_residual`：`max |C x - d|`，对**全部**约束行（含冗余行与固定行）。
- `min_primal_value`：`min x_i`（仅非负问题），原始可行性。
- `max_stationarity`：`max |W(x-y) + C'λ - μ|`。
- `max_complementary`：`max |μ_i x_i|`。
- `min_dual`：`min μ_i`（仅非负问题），对偶可行性。
- `verified`：以上残差全部精确为 0 且符号条件成立。

不可行检测：固定计数与等式约束不一致（精确秩判定），或固定计数在
`x >= 0` 下不可行（Bland 规则精确单纯形 Phase-I）。零权奇异性通过
KKT 矩阵精确消元的自由列检测并报告变量编号。

## 热启动与局部修订

`problem.with_observation(i, value, weight=None)` 生成局部修订后的新问题；
`solve(new_problem, warm_start=old_result)` 用旧活动集热启动。热启动只是
初始工作集，**证书总是重新计算**，不继承旧结论。

## 枚举校验

`dagadjust.enumeration.enumerate_active_sets(problem)` 对小规模实例
（默认 `n <= 16`）枚举全部活跃集，精确求解对应等式 QP 并筛选满足 KKT
条件的点，返回目标最优者。测试用它与迭代求解器对拍全局最优性。

## 运行

```sh
python3.11 -m dagadjust.demo                      # 合成数据样例
python3.11 -m unittest discover -s tests -t . -v  # 测试
```

## 文件

- `dagadjust/linalg.py`：精确有理线性代数（RREF、秩、独立行、方阵求解、
  Phase-I 单纯形可行性）。
- `dagadjust/problem.py`：`CountDAG` / `AdjustmentProblem` 定义与校验。
- `dagadjust/solver.py`：等式 WLS、非负活动集、状态机。
- `dagadjust/certificates.py`：原始可行性与 KKT 残差证书。
- `dagadjust/enumeration.py`：小规模活跃集枚举对拍。
- `dagadjust/demo.py`：样例运行。
- `tests/test_dagadjust.py`：unittest 套件。
