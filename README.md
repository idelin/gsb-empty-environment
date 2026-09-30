# countadjust — 计数 DAG 的精确有理数加权最小二乘调整

对带独立噪声的树/计数 DAG 节点计数做加权最小二乘（WLS）调整：
父节点等于子节点之和，可选非负约束。全部计算基于 `fractions.Fraction`
精确有理数运算——残差为 0 是证明而非近似，**不会**把"迭代变化小"
当作最优。仅使用 Python 3.11 标准库。

## 问题定义

```
minimize    1/2 * Σ_i w_i (x_i − y_i)²
subject to  A x = b        （父 = 子和，或一般线性约束；矩阵允许冗余）
            x_i = f_i      （固定计数，硬等式，可选）
            x_i ≥ 0        （可选，nonneg=True）
```

## 参数与语义

- `y_i`（observed）：节点观测计数，可为任意有理数。
- `w_i`（weight）：
  - `w_i > 0`：对观测的信任度，越大越信任；
  - `w_i = 0`：**观测被完全忽略**，`x_i` 为自由变量，仅由约束决定；
    若约束也无法确定它，则问题奇异，求解器返回
    `SINGULAR_ZERO_WEIGHT` 并附零权奇异方向证书 `d ≠ 0, W d = 0, A d = 0`；
  - `w_i < 0`：建模时直接拒绝。
- `fix(name, f)`：固定计数。若固定为硬等式；固定值之间/与约束冲突时
  返回 `INFEASIBLE` 并附 Farkas 不可行证书 `μ`（`μᵀA = 0, μᵀb ≠ 0`，
  两次精确点积即可验证）。
- `nonneg`：是否启用 `x ≥ 0`（活动集法，精确符号判定）。
- `max_iter`：活动集更新次数上限（默认 `10(n+1)`）。**达到上限返回
  `MAX_ITER`，绝不声称最优。**

## 输出

`SolveResult`：`status`、`x`（精确分数解）、`objective`、`iterations`、
`active_set`，以及按状态附带的证书：

- `OPTIMAL`：`KKTCertificate`——原始可行性残差（`Ax−b`，全为 0）、
  平稳性残差（`W(x−y)+Aᵀλ−z`，全为 0）、对偶可行（`z ≥ 0`）、
  互补松弛（`z_i x_i = 0`），`is_valid()` 精确复核；
- `INFEASIBLE`：Farkas 证书 `μ`；
- `SINGULAR_ZERO_WEIGHT`：奇异方向 `d` 与不确定变量名；
- `MAX_ITER`：无证书，非最优。

## 枚举校验与热启动

- `enumerate_active_sets(model)`：对小规模问题（n ≤ 14）枚举全部活动集，
  精确求解并筛选原始/对偶可行点，返回全局最优目标值。与活动集法
  目标值精确相等即为全局最优的证明（凸 QP）。
- `solver.revise(observed_updates=...)`：局部观测/权重修订后可用上一次
  活动集热启动，但 KKT 证书总是从头重算并精确验证后才返回 `OPTIMAL`。

## 运行

```bash
python3.11 examples/run_demo.py     # 合成数据样例
python3.11 -m unittest discover -s tests -v
```

## 免责声明

本仓库只处理**合成数据**；所有统计实验仅作算法演示，**不构成**任何
形式化隐私证明。
