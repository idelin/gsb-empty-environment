# lowrank-update

在已分解系统上做秩一（Sherman-Morrison）或低秩（Woodbury）更新求解，避免每次完整重分解。纯 Python 3.11 标准库实现，不依赖 NumPy/SciPy；小规模用自写稠密 LU 作参考解核验。

## 功能

- `LUFactorization`：稠密 LU 分解（部分主元），可重复求解，更新失败时保持可用。
- `sherman_morrison_solve(fact, u, v, b)`：解 `(A + u v^T) x = b`，O(n^2)，不重分解。
- `sherman_morrison_solve_many`：多右端共享 `A^{-1} u` 与分母。
- `sherman_morrison_reverse_solve`：反向更新，恢复原系统解（round-trip 校验）。
- `woodbury_solve(fact, ucols, vcols, b)`：秩-k 更新，k×k 电容矩阵现分解。
- 奇异性检测：分母 `1 + v^T A^{-1} u`（或电容矩阵主元）≤ `sing_tol` 抛 `SingularUpdateError`；≤ `stab_tol` 发 `UnstableUpdateWarning` 报告不稳定。
- 尺寸与索引显式输入：`sparse_vector(n, indices, values)` 按显式索引构造更新向量。

## 运行

```console
$ python3.11 lowrank_update.py
original solve        x0 = ['0.128035', '0.425200', '0.125318', '1.884191']
rank-1 update         x1 = ['-1.439139', '0.800250', '0.643883', '1.578027']
dense reference max|x1-ref| = 4.441e-16
reverse update max|xr-x0|   = 2.498e-16
multi-RHS (2) max error     = 4.441e-16
rank-2 Woodbury max error   = 2.220e-16
singular update detected  -> sherman_morrison_solve: update makes the system singular (denominator 0.000e+00); original factorization is unchanged
factorization still usable, max|x-x0| = 0.000e+00
```

## 测试

```console
$ python3.11 -m unittest test_lowrank_update
.....................
----------------------------------------------------------------------
Ran 21 tests in 0.001s

OK
```

覆盖：LU 残差与奇异拒绝、显式索引向量、秩一更新 vs 稠密参考解（容差 1e-9）、反向更新恢复、奇异更新检测且原分解仍可用、近零分母不稳定告警、多右端与参考解一致、Woodbury 秩 2/3 与秩一一致性、奇异电容矩阵检测。
