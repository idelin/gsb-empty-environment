# errordag — 误差传播 DAG（共享噪声符号 + 可验证证书）

纯 Python 3.11 标准库实现（不依赖 NumPy/SciPy），测试使用 `unittest`。

## 设计

- **仿射形式（`errordag/affine.py`）**：每个量表示为
  `center + Σ coeffs[i]·ε_i`，其中 `ε_i ∈ [-1, 1]` 是**共享**噪声符号。
  同源引用保留相关性：`x - x` 精确为 0；`x` 与独立的 `y` 即使区间相同
  也不抵消（两个不同符号）。
- **公开保守余项界（`errordag/bounds.py`）**：
  - 乘法：噪声部分乘积由半径之积 `r(a)·r(b)` 界定（标准仿射算术界）；
  - 倒数 `1/x`（区间不含 0）：Chebyshev 极小极大仿射包络，偏差
    `g(x)=1/x−αx` 的临界点为 `x=±√(l·u)`，故端点加临界点即精确极小极大；
  - 分段线性函数：`f−αx` 仍为分段线性，极值在端点**和内部拐点**处取得，
    全部代入求精确极小极大包络——跨拐点自动拆分/包络，绝不只取端点；
    区间内无拐点时余项 `δ=0`，退化为精确线性映射。
- **DAG（`errordag/dag.py`）**：线性步骤（加/减/数乘）精确；非线性步骤
  （乘、倒数、分段）把余项折入新共享符号。`div` 分解为 `recip`+`mul`，
  使每个节点只携带一个余项，便于逐步验证。
- **增量变更**：`update_var` 只把该叶节点的传递后继标脏，`evaluate` 仅
  重算这些节点（`dag.recomputed` 可查）；覆盖受影响节点的已签发证书被
  降级为 `INCOMPLETE`（未完成证书），不相交分支的证书保持 `COMPLETE`。
- **预算截断**：`DAG(max_symbols=k)` 超出噪声符号额度时求值中止，
  签发的证书为 `INCOMPLETE` 并携带原因。
- **独立验证器（`errordag/certificate.py`）**：不信任证书记录的任何余项，
  用**自写稠密实现**（`errordag/dense.py`，显式 `size` 与下标，纯列表）
  从零重放证书结构，重算每个 `α, ζ, δ`，并强制符号分配的新鲜性纪律
  （新符号下标严格递增且未被先前节点使用）。篡改区间/系数、除数跨 0、
  额度超限、证书过期（版本不符）都会被拒绝。
- **稠密核验**：`DenseAffineForm(size, ...)` 尺寸显式输入，小规模下与
  稀疏形式互相转换并逐项比对（测试 `DenseCrossCheckTests`）。

## 运行

```sh
python3.11 -m unittest discover -s tests -v
```

## 实际运行结果（2026-09-25，Python 3.11.15）

```
test_budget_sufficient_completes ... ok
test_symbol_budget_exceeded_returns_incomplete_certificate ... ok
test_bad_certificate_rejected ... ok
test_enumeration_full_chain_containment ... ok
test_incomplete_certificate_not_verifiable ... ok
test_valid_certificate_verifies ... ok
test_independent_vars_same_interval_do_not_cancel ... ok
test_x_minus_x_is_exact_zero ... ok
test_dense_explicit_size_enforced ... ok
test_dense_matches_sparse_on_small_chain ... ok
test_divisor_away_from_zero_is_sound ... ok
test_divisor_crossing_zero_explicitly_handled ... ok
test_incremental_update_invalidates_only_affected_certificates ... ok
test_incremental_update_touches_only_affected_successors ... ok
test_abs_crossing_kink_is_not_endpoint_only ... ok
test_piecewise_envelope_sound_by_enumeration ... ok
test_piecewise_without_interior_kink_is_exact ... ok

----------------------------------------------------------------------
Ran 17 tests in 0.002s

OK
```

## 测试覆盖（对应需求）

| 需求 | 测试 |
| --- | --- |
| `x - x` 精确为 0 而非宽区间 | `test_x_minus_x_is_exact_zero` |
| 独立 `y` 区间相同也不抵消 | `test_independent_vars_same_interval_do_not_cancel` |
| 分段函数跨拐点拆分/保守包络，不只求端点 | `test_abs_crossing_kink_is_not_endpoint_only`、`test_piecewise_*` |
| 更改一个噪声界仅更新受影响后继 | `test_incremental_update_touches_only_affected_successors` |
| 增量变更使受影响证书变为未完成 | `test_incremental_update_invalidates_only_affected_certificates` |
| 小域枚举检查全链包含性 | `test_enumeration_full_chain_containment`（11×11 网格逐节点包含） |
| 坏证书明确拒绝 | `test_bad_certificate_rejected` |
| 除数跨 0 明确处理 | `test_divisor_crossing_zero_explicitly_handled` |
| 符号额度超限明确处理 | `test_symbol_budget_exceeded_returns_incomplete_certificate` |
