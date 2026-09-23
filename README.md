# 等式消元器（Python 3.11，仅标准库）

用已知等式消元变量，记录每次代入的条件与无法消去的情形；循环等式交给线性代数
（高斯消元）处理而非无限替换。全程使用 `fractions.Fraction` 做有理数精确计算，
不调用任何符号代数库；每一步变换都标注适用域。

## 文件结构

- `linear.py` — 线性型 `LinearForm`（Fraction 系数）、等量代换、精确 RREF 高斯消元
- `parser.py` — 用 `ast`/`tokenize` 解析 `lhs = rhs` 等式；除以可能为 0 的项时产生非零条件
- `eliminator.py` — 消元主流程：解出定义 → 依赖图找环 → 无环代入 / 有环走线性代数 → 校验非零条件
- `main.py` — 命令行入口与内置样例
- `test_eliminator.py` — 17 个 unittest 用例

## 核心规则

- `x = y + 1`、`y = x - 1`：定义依赖图出现环 `x -> y -> x`，停止代入，高斯消元判定
  秩 1 < 2 变量，结论为**自由族（相容，非矛盾）**，`y` 为无法消去的自由变量。
- `x = y + 1`、`y = x`：同样成环，高斯消元化简出 `0 = 1`，判定**矛盾**。
- 除以可能为 0 的项（如 `x / (y - 1) = 2`）必须分支：主分支假设 `y - 1 != 0`，
  该假设被记录并在求解后化简为对自由变量的限制；`y - 1 = 0` 分支因原式无定义而舍弃。
- 除以非零常数（如 `y = 1/4`）记录适用域说明，无需分支。
- 同一变量被定义两次时，两定义相减得到新方程继续消元（可发现 `x=y+1, x=y+2` 的矛盾）。
- 非线性乘积（如 `x * y = 1`）明确报错，超出线性求解范围。

## 运行

```sh
python3.11 main.py                       # 运行全部内置样例
python3.11 main.py "x = y + 1" "y = x"   # 求解指定方程组
python3.11 -m unittest -v                # 运行测试
```

## 运行样例（节选）

```text
=== 样例1：自由族（相容，非矛盾） ===
  x = y + 1
  y = x - 1
-- 主分支 --
   3. [define] 解出 x = y + 1
      适用域: 两边同除以非零有理数 1（适用域：Q，恒合法）
   4. [define] 解出 y = x - 1
   5. [cycle] 检测到循环定义: x -> y -> x；停止代入，改用线性代数（高斯消元）处理，避免无限替换
   6. [gauss] 高斯消元：2 个方程、2 个变量（Fraction 精确计算，不调用符号代数库）
   8. [gauss] 行最简形的秩 = 1；自由变量: y
   9. [result] 无法消去的变量（自由变量）: y；方程组为自由族而非矛盾
  状态: 相容（自由族，非矛盾）
    x = y + 1
    自由变量: y

=== 样例2：矛盾方程组 ===
  x = y + 1
  y = x
   5. [cycle] 检测到循环定义: x -> y -> x；停止代入，改用线性代数（高斯消元）处理
   8. [gauss] 化简出现 0 = 1，方程组不相容
  状态: 矛盾（无解）

=== 样例3：除以可能为 0 的项（分支） ===
  x / (y - 1) = 2
  y = z + 3
-- 主分支 --
  假设: y - 1 != 0
   9. [condition] 保留条件 z + 2 != 0（对自由变量的限制）
  状态: 相容（自由族，非矛盾）
    x = 2*z + 4
    y = z + 3
    自由变量: z
    保留条件: z + 2 != 0
-- 分支 y - 1 = 0 --
  当 y - 1 = 0 时原方程含除零，表达式无定义，舍弃该分支

=== 样例5：有理数精确计算 ===
  2*x = 3*y
  y = 1/4
  状态: 相容（唯一解）
    x = 3/8
    y = 1/4
```

## 测试结果

```text
$ python3.11 -m unittest -v
test_acyclic_chain (test_eliminator.EliminatorTest) ... ok
test_conflicting_definitions ... ok
test_contradiction ... ok
test_division_branches ... ok
test_exact_rationals ... ok
test_free_family_not_contradiction ... ok
test_infeasible_condition ... ok
test_inconsistent_system (GaussTest) ... ok
test_rank_and_free_vars (GaussTest) ... ok
test_arithmetic_exact (LinearFormTest) ... ok
test_solve_for (LinearFormTest) ... ok
test_substitute (LinearFormTest) ... ok
test_constant_denominator_no_branch (ParserTest) ... ok
test_division_records_condition (ParserTest) ... ok
test_linear_equation (ParserTest) ... ok
test_nonlinear_rejected (ParserTest) ... ok
test_zero_denominator_rejected (ParserTest) ... ok
----------------------------------------------------------------------
Ran 17 tests in 0.007s

OK
```
