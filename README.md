# 有理矩阵高斯消元（Python 3.11，纯标准库）

在有理数域 Q 上精确地做高斯消元：求秩、解线性方程组、给出不相容证据，
并输出完整的初等变换轨迹。**全程使用 `fractions.Fraction` 精确计算，
不转浮点，不调用任何符号代数库**（仅标准库，测试用 `unittest`）。

## 功能

- **求秩**：`rank(A)` / `rref(A)`，精确到有理数，无浮点误差；
- **解方程组** `solve(A, b)`：
  - 唯一解：`particular` 给出该解，`nullspace_basis` 为空；
  - 欠定：`particular` 给特解（自由变量取 0），`nullspace_basis` 给零空间基，
    通解 = 特解 + Σ tⱼ·vⱼ（tⱼ ∈ Q）；
  - 不相容：`consistent=False`，`evidence` 给出 `0 = c (c ≠ 0)` 的证据行；
- **行操作轨迹**：每一步初等变换（行交换 / 行倍乘 / 行倍加 / 列交换）
  都记录操作内容并注明**适用域**（该变换在什么条件下保持方程组等价）；
- **列交换还原变量顺序**：列交换只作用于系数列（不碰右端常数列），
  同时记录变量置换 `col_perm`，最终 `particular` 与 `nullspace_basis`
  一律按**原始变量顺序**给出；
- **拒绝浮点输入**：`float` 直接抛 `TypeError`，杜绝精度污染。

## 文件

- `rational_gauss.py` — 核心模块 + 演示（`python3.11 rational_gauss.py`）
- `test_rational_gauss.py` — 27 个单元测试（`python3.11 -m unittest -v`）

## 算法要点

1. 增广矩阵 `[A|b]` 直接消到**行最简形**（RREF），主元化为 1 并上下清零；
2. 当前列找不到主元时，在**系数列范围内**寻找可交换的列做列交换，
   置换记入 `col_perm`（`col_perm[j]` = 当前第 j 列对应的原始变量下标）；
3. 秩 = 主元个数；系数区全零而右端非零的行即不相容证据 `0 = c`；
4. 主元列对应基本变量，其余为自由变量；特解令自由变量为 0，
   零空间基令每个自由变量轮流取 1；
5. 读出解后按 `col_perm` 的逆置换**还原为原始变量顺序**再返回。

## 运行样例（`python3.11 rational_gauss.py`）

```
========================================================================
例 1：含分数系数的唯一解
========================================================================
系数矩阵 A（2 x 2），右端 b = (1, 1)

消元轨迹（每步注明适用域）：
  [ 1] 行倍乘: R1 <- R1 * (2)（即除以 1/2，乘数非零，满足适用条件）
       适用域：乘数 c 必须是非零有理数（c != 0）；方程两边同乘非零常数 得到的是等价方程，解集不变
  [ 2] 行倍加: R2 <- R2 - (1) * R1
       适用域：倍数 c 可取任意有理数，且要求 i != j；把第 j 行的 c 倍加到 第 i 行，是行交换与行倍乘的复合，解集不变
  [ 3] 行倍乘: R2 <- R2 * (-1/3)（即除以 -3，乘数非零，满足适用条件）
       适用域：乘数 c 必须是非零有理数（c != 0）；方程两边同乘非零常数 得到的是等价方程，解集不变
  [ 4] 行倍加: R1 <- R1 - (2) * R2
       适用域：倍数 c 可取任意有理数，且要求 i != j；把第 j 行的 c 倍加到 第 i 行，是行交换与行倍乘的复合，解集不变

行最简形 [A|b]（列为当前变量顺序，置换 col_perm = [1, 2]）：
    [      1,      0,    4/3 ]
    [      0,      1,    1/3 ]

rank(A) = 2，未知数个数 n = 2
特解 x_p = (4/3, 1/3)（已按原始变量顺序还原）
零空间 = {0}，解唯一
验证 A*x_p = (1, 1)  （应等于 b）

========================================================================
例 2：欠定系统（rank=2 < n=4），给特解与零空间基
========================================================================
系数矩阵 A（3 x 4），右端 b = (2, 7, 9)

消元轨迹（每步注明适用域）：
  [ 1] 行倍加: R2 <- R2 - (2) * R1
       适用域：倍数 c 可取任意有理数，且要求 i != j；把第 j 行的 c 倍加到 第 i 行，是行交换与行倍乘的复合，解集不变
  [ 2] 行倍加: R3 <- R3 - (3) * R1
       适用域：倍数 c 可取任意有理数，且要求 i != j；把第 j 行的 c 倍加到 第 i 行，是行交换与行倍乘的复合，解集不变
  [ 3] 列交换: C2 <-> C3（即未知数 x2 <-> x3，最终答案将还原变量顺序）
       适用域：仅限系数列之间交换（不得涉及右端常数列），且必须同步交换对应 未知数的标签，方程组才保持等价；求解结束后将按原始变量顺序还原
  [ 4] 行倍乘: R2 <- R2 * (1/3)（即除以 3，乘数非零，满足适用条件）
       适用域：乘数 c 必须是非零有理数（c != 0）；方程两边同乘非零常数 得到的是等价方程，解集不变
  [ 5] 行倍加: R1 <- R1 - (-1) * R2
       适用域：倍数 c 可取任意有理数，且要求 i != j；把第 j 行的 c 倍加到 第 i 行，是行交换与行倍乘的复合，解集不变
  [ 6] 行倍加: R3 <- R3 - (3) * R2
       适用域：倍数 c 可取任意有理数，且要求 i != j；把第 j 行的 c 倍加到 第 i 行，是行交换与行倍乘的复合，解集不变

行最简形 [A|b]（列为当前变量顺序，置换 col_perm = [1, 3, 2, 4]）：
    [      1,      0,      2,      2,      3 ]
    [      0,      1,      0,      1,      1 ]
    [      0,      0,      0,      0,      0 ]

rank(A) = 2，未知数个数 n = 4
特解 x_p = (3, 0, 1, 0)（已按原始变量顺序还原）
零空间维数 = n - rank = 2，零空间基：
  v1 = (-2, 1, 0, 0)
  v2 = (-2, 0, -1, 1)
通解 x = x_p + t1*v1 + t2*v2（t_j 为任意有理数）
验证 A*x_p = (2, 7, 9)  （应等于 b）
验证 A*v1 = (0, 0, 0)  （应等于 0）
验证 A*v2 = (0, 0, 0)  （应等于 0）

========================================================================
例 3：不相容系统，给出 0 = 1 证据行
========================================================================
系数矩阵 A（3 x 2），右端 b = (1, 3, 0)

消元轨迹（每步注明适用域）：
  [ 1] 行倍加: R2 <- R2 - (2) * R1
       适用域：倍数 c 可取任意有理数，且要求 i != j；把第 j 行的 c 倍加到 第 i 行，是行交换与行倍乘的复合，解集不变
  [ 2] 行倍加: R3 <- R3 - (1) * R1
       适用域：倍数 c 可取任意有理数，且要求 i != j；把第 j 行的 c 倍加到 第 i 行，是行交换与行倍乘的复合，解集不变
  [ 3] 行交换: R2 <-> R3
       适用域：任意两行均可交换；相当于交换两个方程的书写次序，线性方程组的解集不变
  [ 4] 行倍乘: R2 <- R2 * (-1/2)（即除以 -2，乘数非零，满足适用条件）
       适用域：乘数 c 必须是非零有理数（c != 0）；方程两边同乘非零常数 得到的是等价方程，解集不变
  [ 5] 行倍加: R1 <- R1 - (1) * R2
       适用域：倍数 c 可取任意有理数，且要求 i != j；把第 j 行的 c 倍加到 第 i 行，是行交换与行倍乘的复合，解集不变

行最简形 [A|b]（列为当前变量顺序，置换 col_perm = [1, 2]）：
    [      1,      0,    1/2 ]
    [      0,      1,    1/2 ]
    [      0,      0,      1 ]

rank(A) = 2，未知数个数 n = 2
不相容证据：行 (0, 0, 1) 对应方程 0*x1 + 0*x2 = 1
即 0 = 1，矛盾 —— 方程组无解

========================================================================
例 4：首列全零触发列交换，答案按原始变量顺序还原
========================================================================
系数矩阵 A（3 x 3），右端 b = (1, 2, 0)

消元轨迹（每步注明适用域）：
  [ 1] 列交换: C1 <-> C2（即未知数 x1 <-> x2，最终答案将还原变量顺序）
       适用域：仅限系数列之间交换（不得涉及右端常数列），且必须同步交换对应 未知数的标签，方程组才保持等价；求解结束后将按原始变量顺序还原
  [ 2] 行交换: R1 <-> R2
       适用域：任意两行均可交换；相当于交换两个方程的书写次序，线性方程组的解集不变
  [ 3] 行倍加: R3 <- R3 - (1) * R1
       适用域：倍数 c 可取任意有理数，且要求 i != j；把第 j 行的 c 倍加到 第 i 行，是行交换与行倍乘的复合，解集不变
  [ 4] 列交换: C2 <-> C3（即未知数 x1 <-> x3，最终答案将还原变量顺序）
       适用域：仅限系数列之间交换（不得涉及右端常数列），且必须同步交换对应 未知数的标签，方程组才保持等价；求解结束后将按原始变量顺序还原
  [ 5] 行倍加: R1 <- R1 - (1) * R2
       适用域：倍数 c 可取任意有理数，且要求 i != j；把第 j 行的 c 倍加到 第 i 行，是行交换与行倍乘的复合，解集不变
  [ 6] 行倍加: R3 <- R3 - (-2) * R2
       适用域：倍数 c 可取任意有理数，且要求 i != j；把第 j 行的 c 倍加到 第 i 行，是行交换与行倍乘的复合，解集不变

行最简形 [A|b]（列为当前变量顺序，置换 col_perm = [2, 3, 1]）：
    [      1,      0,      0,      1 ]
    [      0,      1,      0,      1 ]
    [      0,      0,      0,      0 ]

rank(A) = 2，未知数个数 n = 3
特解 x_p = (0, 1, 1)（已按原始变量顺序还原）
零空间维数 = n - rank = 1，零空间基：
  v1 = (1, 0, 0)
通解 x = x_p + t1*v1（t_j 为任意有理数）
验证 A*x_p = (1, 2, 0)  （应等于 b）
验证 A*v1 = (0, 0, 0)  （应等于 0）
```

## 测试结果（`python3.11 -m unittest -v`）

```
test_column_swap_in_trace (test_rational_gauss.TestColumnSwap.test_column_swap_in_trace) ... ok
test_swap_then_verify_against_original (test_rational_gauss.TestColumnSwap.test_swap_then_verify_against_original) ... ok
test_variable_order_restored (test_rational_gauss.TestColumnSwap.test_variable_order_restored) ... ok
test_fraction_inconsistency (test_rational_gauss.TestInconsistent.test_fraction_inconsistency) ... ok
test_zero_equals_one_evidence (test_rational_gauss.TestInconsistent.test_zero_equals_one_evidence) ... ok
test_bool_rejected (test_rational_gauss.TestNoFloat.test_bool_rejected) ... ok
test_float_in_b_rejected (test_rational_gauss.TestNoFloat.test_float_in_b_rejected) ... ok
test_float_input_rejected (test_rational_gauss.TestNoFloat.test_float_input_rejected) ... ok
test_fraction_entries (test_rational_gauss.TestRank.test_fraction_entries) ... ok
test_full_rank (test_rational_gauss.TestRank.test_full_rank) ... ok
test_rank_deficient (test_rational_gauss.TestRank.test_rank_deficient) ... ok
test_tall_matrix (test_rational_gauss.TestRank.test_tall_matrix) ... ok
test_zero_matrix (test_rational_gauss.TestRank.test_zero_matrix) ... ok
test_entries_are_fractions (test_rational_gauss.TestRrefStructure.test_entries_are_fractions) ... ok
test_pivot_structure (test_rational_gauss.TestRrefStructure.test_pivot_structure) ... ok
test_every_step_states_domain (test_rational_gauss.TestTrace.test_every_step_states_domain) ... ok
test_operations_are_elementary (test_rational_gauss.TestTrace.test_operations_are_elementary) ... ok
test_general_solution_family (test_rational_gauss.TestUnderdetermined.test_general_solution_family) ... ok
test_nullspace_basis_in_kernel (test_rational_gauss.TestUnderdetermined.test_nullspace_basis_in_kernel) ... ok
test_nullspace_basis_independent (test_rational_gauss.TestUnderdetermined.test_nullspace_basis_independent) ... ok
test_particular_satisfies_system (test_rational_gauss.TestUnderdetermined.test_particular_satisfies_system) ... ok
test_rank_and_nullity (test_rational_gauss.TestUnderdetermined.test_rank_and_nullity) ... ok
test_fraction_system_exact (test_rational_gauss.TestUniqueSolution.test_fraction_system_exact) ... ok
test_integer_system (test_rational_gauss.TestUniqueSolution.test_integer_system) ... ok
test_string_input_accepted (test_rational_gauss.TestUniqueSolution.test_string_input_accepted) ... ok
test_b_length_mismatch_rejected (test_rational_gauss.TestValidation.test_b_length_mismatch_rejected) ... ok
test_ragged_matrix_rejected (test_rational_gauss.TestValidation.test_ragged_matrix_rejected) ... ok

----------------------------------------------------------------------
Ran 27 tests in 0.006s

OK
```
