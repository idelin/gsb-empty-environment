# lowrank — 分解后系统的秩一/低秩更新求解

纯 Python 3.11 标准库实现（不依赖 NumPy/SciPy）。对已经做过一次稠密 LU
分解（部分主元）的系统 `A x = b`，用 **Sherman–Morrison**（秩一）与
**Woodbury**（秩 k）公式求解更新后的系统，避免每次完整重分解：

- `(A + u vᵀ) x = b`：一次 `A⁻¹u` 求解 + 每次右端一次求解，O(n²)。
- `(A + U Vᵀ) x = b`：k 次求解构造 k×k 电容矩阵 `C = I + Vᵀ A⁻¹ U` 并分解。
- 更新导致奇异/不稳定时抛出 `SingularUpdateError`（分母 `1 + vᵀA⁻¹u`
  接近 0 或电容矩阵近奇异），**原分解不受影响、仍可继续使用**。
- 尺寸 `n`、稀疏索引与值均为显式输入（`indices`/`values`），不从容器形状推断。
- 更新可组合：`reverse_update()` 用 `(-u, v)` 再更新一次即恢复原矩阵的解。
- 支持多右端 `solve_many`，测试中与直接稠密参考解在容差内一致。

## 文件

- `lowrank.py` — LU 分解、`RankOneUpdate`、`LowRankUpdate`、异常类型
- `test_lowrank.py` — unittest 测试（16 个用例）
- `demo.py` — 运行样例

## 运行

```bash
python3.11 demo.py
python3.11 -m unittest test_lowrank -v
```

## 运行样例（`python3.11 demo.py`）

```
=== base factorization ===
x0 = [0.078758, 0.516433, 0.337073, 1.01704]

=== rank-1 update (Sherman-Morrison) ===
denominator 1 + v^T A^-1 u = 1.044367
x1 = [-0.547271, 0.683903, 0.620532, 0.926482]
direct reference       = [-0.547271, 0.683903, 0.620532, 0.926482]
max |x1 - x_ref|       = 1.11e-16

=== multiple right-hand sides ===
3 RHS, max error vs direct reference = 1.11e-16

=== reverse update (A + u v^T) - u v^T = A ===
x_back = [0.078758, 0.516433, 0.337073, 1.01704]
max |x_back - x0| = 5.55e-17

=== rank-2 update (Woodbury) ===
x2 = [0.017154, 0.486563, 0.403076, 0.999889]
max |x2 - x_ref| = 1.11e-16

=== singular update detection ===
caught: rank-1 update is singular or unstable: denominator 1 + v^T A^-1 u = 0.000e+00
base LU still usable, residual = 0.00e+00
```

## 测试结果（`python3.11 -m unittest test_lowrank -v`）

```
test_multiple_rhs (test_lowrank.TestDenseLU.test_multiple_rhs) ... ok
test_singular_matrix_detected (test_lowrank.TestDenseLU.test_singular_matrix_detected) ... ok
test_solve_matches_residual (test_lowrank.TestDenseLU.test_solve_matches_residual) ... ok
test_wrong_size_rejected (test_lowrank.TestDenseLU.test_wrong_size_rejected) ... ok
test_matches_direct_dense_solve (test_lowrank.TestLowRankUpdate.test_matches_direct_dense_solve) ... ok
test_multiple_rhs_consistent_with_reference (test_lowrank.TestLowRankUpdate.test_multiple_rhs_consistent_with_reference) ... ok
test_rank_one_is_special_case_of_low_rank (test_lowrank.TestLowRankUpdate.test_rank_one_is_special_case_of_low_rank) ... ok
test_reverse_update_via_composition (test_lowrank.TestLowRankUpdate.test_reverse_update_via_composition) ... ok
test_singular_low_rank_update_detected_and_base_survives (test_lowrank.TestLowRankUpdate.test_singular_low_rank_update_detected_and_base_survives) ... ok
test_base_factorization_not_refactorized (test_lowrank.TestRankOneUpdate.test_base_factorization_not_refactorized) ... ok
test_dimension_mismatch_rejected (test_lowrank.TestRankOneUpdate.test_dimension_mismatch_rejected) ... ok
test_matches_direct_dense_solve (test_lowrank.TestRankOneUpdate.test_matches_direct_dense_solve) ... ok
test_multiple_rhs_consistent_with_reference (test_lowrank.TestRankOneUpdate.test_multiple_rhs_consistent_with_reference) ... ok
test_near_zero_denominator_reported_unstable (test_lowrank.TestRankOneUpdate.test_near_zero_denominator_reported_unstable) ... ok
test_reverse_update_recovers_solution (test_lowrank.TestRankOneUpdate.test_reverse_update_recovers_solution) ... ok
test_singular_update_detected_and_base_survives (test_lowrank.TestRankOneUpdate.test_singular_update_detected_and_base_survives) ... ok

----------------------------------------------------------------------
Ran 16 tests in 0.003s

OK
```

## 用法示例

```python
from lowrank import LU, RankOneUpdate, LowRankUpdate, SingularUpdateError

lu = LU(A, n)                                   # 一次分解
upd = RankOneUpdate(lu, u_idx, u_val,           # 秩一更新（显式索引/值）
                    v_idx, v_val, n)
x = upd.solve(b)                                # O(n^2)，不重分解
xs = upd.solve_many([b1, b2, b3])               # 多右端
back = upd.reverse_update()                     # 反向更新恢复原解

lr = LowRankUpdate(lu, u_cols, v_cols, n, k=3)  # 秩 k 更新（Woodbury）
try:
    bad = RankOneUpdate(lu, ...)                # 奇异/不稳定更新
except SingularUpdateError:
    x = lu.solve(b)                             # 原分解仍可用
```
