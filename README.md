# dpproj：公开固定分桶的加噪计数 + 精确有理投影（含 KKT 证据）

纯 Python 3.11 标准库实现（`unittest` 测试）。**仅处理合成数据**。

流程：对有界贡献计数在公开固定分桶上加 Laplace 噪声（种子可复算、只抽一次），
随后把带噪向量投影到「非负 + 线性一致约束（含总量一致）」的可行域上。投影是
有理权重最小二乘 QP，全部用 `Fraction` 精确计算，并输出可独立核验的 KKT 证据。

## 定义

- **公开固定分桶**：分桶边界与桶名是公开的、事先固定的，不随数据变化。
- **有界贡献**：每个个体对所有桶的 L1 贡献总量不超过 `l1_sensitivity`（记 Δ）。
- **Laplace 机制**：对 L1 敏感度 Δ、参数 ε，加尺度 `scale = Δ / ε` 的 Laplace 噪声。
- **总量目标来源**：总量约束的右端项只能来自
  - `PublicTarget(value)`：明确公开的常数；或
  - `NoisedTarget(epsilon)`：真实总量 + 独立 Laplace 噪声，使用**独立的账本键和
    独立的 ε 份额**。绝不允许用带噪桶值反推总量（避免重复使用同一噪声）。
- **投影（后处理）**：求解
  `min  Σ w_i (x_i − y_i)²   s.t.  A x = b,  x ≥ 0`
  其中 `y` 为带噪向量（负值合法，由非负约束处理；零计数桶同理），
  `w_i = 1/scale²`（有理数），`A x = b` 为总量与重叠分组的一致性约束。
  投影是带噪输入的**确定性函数，绝不重采样**挑选满意结果。
- **KKT 证据**：`(x, λ, μ)` 满足
  - 驻点条件 `2 w_i (x_i − y_i) − (Aᵀλ)_i − μ_i = 0`
  - 原始可行 `A x = b, x ≥ 0`；对偶可行 `μ ≥ 0`；互补松弛 `μ_i x_i = 0`
  任何第三方可用 `dpproj.qp.verify_kkt` 以精确算术独立核验，无需信任求解器。

## 关键行为

- **噪声只抽一次**：`NoiseLedger` 以 `(release_id, bucket)` 为键缓存每次抽样；
  重复发布复用已存噪声，不会重抽。相同种子 + 相同请求序列可完全复算。
  账本可 JSON 序列化（含 RNG 状态），用于存档与审计。
- **不可行总量明确拒绝**：如总量目标为负（`x ≥ 0` 下无可行解），
  抛出 `InfeasibleError`，发布被拒绝。可行性由精确两阶段单纯形法
  （Bland 规则，保证终止）判定。
- **迭代预算不足标未收敛**：`max_iterations` 耗尽时返回
  `status="not_converged"`、`converged=False`，且不附带 KKT 证据。
- **原噪声与后处理分离**：`ReleaseResult.noisy_counts`（float，机制原始输出）
  与 `ReleaseResult.projected`（`Fraction`，后处理结果）分字段保存。
- **小规模参考核验**：`dpproj.reference.solve_qp_enumeration` 枚举全部
  2ⁿ 个活动集求精确解，用于交叉验证主求解器（测试中以 30 组伪随机
  用例比对一致）。
- **浮点→有理的舍入是公开规范的一部分**：带噪浮点值以分母 10⁶ 的
  最近有理数进入 QP（`Fraction.limit_denominator(10**6)`），确定性、可复算。

## 参数

| 参数 | 含义 |
|---|---|
| `seed` | 账本种子；决定全部噪声抽样 |
| `epsilon` | 桶计数的 ε（Laplace 尺度 = Δ/ε） |
| `l1_sensitivity` | 有界贡献 L1 敏感度 Δ |
| `total` | `PublicTarget` 或 `NoisedTarget(epsilon)`（独立 ε 份额） |
| `constraints` | 重叠分组的线性一致约束（有理系数 + 目标来源） |
| `max_iterations` | 活动集迭代预算；耗尽标未收敛 |

## 运行

```bash
python3.11 examples/run_example.py   # 端到端样例（4 个场景）
python3.11 -m unittest discover -s tests -v
```

## 免责声明

- 本仓库只处理**合成数据**，用于演示机制与后处理的正确性工程。
- 代码实现的是教科书式 Laplace 机制与确定性投影；**统计实验不构成
  形式化隐私证明**。任何具体部署的隐私核算（ε 分配、组合定理、
  浮点安全等）需另行论证。
- 浮点 Laplace 抽样的已知侧信道（如精度/表示问题）不在本实现处理范围内；
  需要强保证的场景应使用离散化机制。
