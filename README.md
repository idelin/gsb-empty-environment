# psd_gaussian

对称半正定（PSD）协方差矩阵的分解、相关高斯采样、线性等式条件下的条件高斯采样，
以及分解残差 / 经验协方差审计。**仅使用 Python 3.11+ 标准库**（`math`、`random`、
`fractions`、`dataclasses`）与 `unittest`，无任何第三方依赖。

## 设计原则

- **可配置的数值判定**：`PSDTolerance(atol, rtol)`，阈值 `atol + rtol * max(1, |λ|max)`。
- **非正定拒绝并给见证**：最小特征值低于负阈值时抛出
  `NotPositiveSemidefiniteError`，携带单位见证向量 `v`（满足 `vᵀΣv < 0`）。
- **近奇异不加抖动**：落在容差带 `[-threshold, 0)` 内的负特征值截断为 0 并记录在
  `truncated_eigenvalues` 中；正特征值一律保留，绝不注入 jitter 掩盖奇异性。
- **负特征值不开方**：拒绝发生在任何 `sqrt` 之前，因子中不会出现 NaN。
- **随机源由调用方注入**：`sample_correlated(..., rng, seed=...)`，`rng` 只需提供
  `gauss(0, 1)`（如 `random.Random(seed)`）；seed 与样本量记录在 `SamplingRecord`。
  相同 seed（或 `getstate`/`setstate` 恢复随机流）精确复现相同样本。
- **精确结论与经验估计分离**：有理数模块（`Fraction`）与分解残差是精确/确定性结论
  （`kind="exact"` 或确定性残差）；样本协方差只是经验估计（`kind="empirical"`），
  精度取决于已记录的 seed 与样本量。

## 模块

- `psd_gaussian/linalg.py` — 纯 Python 矩阵运算与循环 Jacobi 对称特征分解。
- `psd_gaussian/decomposition.py` — PSD 判定、谱分解 `Σ ≈ A Aᵀ`、见证、残差。
- `psd_gaussian/sampling.py` — 相关高斯采样 `x = μ + A z`，随机源注入与记录。
- `psd_gaussian/conditional.py` — 线性等式约束 `Cx = d` 下的条件高斯；检测
  `inconsistent_constraints`（线性方程组无解）与 `outside_support`（零概率事件），
  并报告零方差方向。
- `psd_gaussian/exact.py` — 小矩阵有理数精确 `L D Lᵀ` 分解与 PSD 精确判定、
  精确见证（`Σ = LDLᵀ` 逐元素相等验证）。
- `psd_gaussian/audit.py` — 样本均值/协方差与目标协方差的对比审计。

## 快速开始

```python
import random
from psd_gaussian import decompose, sample_correlated, audit_samples, condition_gaussian

cov = [[4.0, 1.0, 1.0], [1.0, 3.0, 0.5], [1.0, 0.5, 2.0]]
dec = decompose(cov)                      # 谱分解，含残差与截断记录
samples, rec = sample_correlated(dec, [1, -2, 0.5], 5000, random.Random(2024), seed=2024)
audit = audit_samples(samples, [1, -2, 0.5], cov, rec)   # 经验估计（标注 kind="empirical"）

cond = condition_gaussian([1, -2, 0.5], cov, [[1.0, 1.0, 1.0]], [2.0])  # x1+x2+x3 = 2
```

## 运行样例

`python3 examples/run_examples.py` 输出（节选）：

```
=== 1. PSD decomposition (exact/deterministic residual) ===
eigenvalues : ['5.028030', '2.392581', '1.579389']
residual    : ||Sigma - A A^T||_F = 4.793e-15 (max abs 2.220e-15)
truncated   : [] (no jitter added)

=== 2. Non-PSD input is rejected with a witness direction ===
rejected    : matrix is not positive semidefinite: eigenvalue -1.000000e+00 ...
witness v   : ['+0.707107', '-0.707107']

=== 3. Correlated sampling (empirical audit, seed recorded) ===
record      : seed=2024, n=5000 [empirical]
cov error   : ||S - Sigma||_F = 0.2301 (relative 3.97%) [empirical]

=== 4. Conditional Gaussian given x1 + x2 + x3 = 2 ===
zero-var dir: [[0.57735, 0.57735, 0.57735]]
  sample ['-0.1649', '-0.2394', '+2.4043']  sum=2.0000000000
infeasible  : unsatisfiable condition (outside_support); residual = [1.0]

=== 5. Exact rational verification (Fraction arithmetic) ===
is_psd      : True [exact]
L D L^T == Sigma : True
indefinite  : is_psd=False, witness=[Fraction(-2, 1), Fraction(1, 1)], v^T Sigma v=-3
```

## 测试

`python3 -m unittest discover -s tests -v`

结果：**29 个测试全部通过**（约 0.36 s），覆盖：

- 分解残差、重构一致性、秩 1 因子、零矩阵、非对称拒绝；
- 非正定见证（`vᵀΣv < 0`）、容差带截断（不加抖动）、可配置容差、负特征值不开方；
- 同 seed 复现、随机流 `getstate`/`setstate` 恢复不改变样本、seed/样本量记录；
- 秩 1 样本落在对应子空间、零方差维严格恒定、经验协方差审计收敛（经验标注）；
- 条件高斯解析解（均值/协方差与理论值一致）、条件样本满足约束、
  不可满足条件（`inconsistent_constraints` / `outside_support`）、零方差方向报告；
- 有理数精确 `LDLᵀ` 关系验证、奇异/不定矩阵的精确判定与精确见证。
