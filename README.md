# DAG 符号求导器

纯 Python 3.11+（仅标准库）实现的符号求导器，支持加、乘、幂及限定初等函数
（`sin` / `cos` / `tan` / `exp` / `ln`），不调用任何符号代数库。

## 特性

- **DAG 共享子表达式**：哈希一致化（hash-consing），结构相同的子式全局只有
  一个节点；求导与化简均按节点记忆化，共享子图只处理一次。
- **指数不展开**：幂规则直接生成 `u^(n-1)`，`(x+1)^50` 的导数是
  `50*(x+1)^49`，不会展开成 50 项乘积链。
- **有理数精确计算**：所有常数用 `fractions.Fraction` 表示，`(1/3)x³` 的
  导数精确为 `x²`，无浮点误差。
- **来源规则轨迹**：每一步求导 / 化简都记录规则名、作用子式与适用域。
- **化简不改变定义域**：`0·ln(x)` 不消去、`ln(x)^0` 不化简为 1、
  `x·x⁻¹` 不化简为 1；仅对全定义域有定义的子式应用零因子 / 零次幂规则。
- **常数幂 0 语义明确**：约定 `a^0 = 1`（含 `0^0 = 1` 的形式约定），
  每次使用该约定都在适用域中明确标注。
- **ln 保留定义域**：`(ln u)' = u'/u` 的适用域标注 `u>0`。

## 文件

- `differentiator.py` — 核心实现（DAG、求导、化简、打印）
- `test_differentiator.py` — 20 个 unittest 用例
- `demo.py` — 7 个运行样例

## 运行

```sh
python3 demo.py                      # 运行样例
python3 -m unittest test_differentiator -v   # 运行测试
```

## 运行样例（节选）

样例 2：共享子图 `x²+1` 被 `g³` 与 `g²` 共同引用，只求导一次
（7 次节点计算 < 9 个 DAG 节点），指数不展开：

```
f(x) = (x^2 + 1)^3 + (x^2 + 1)^2
求导计算的不同节点数: 7（f 的 DAG 节点数: 9）
求导规则轨迹:
  [4] 和规则 (u+v)' = u'+v'
      作用于: x^2 + 1
      适用域: 在 u、v 定义域的交集上成立
  [5] 幂规则 (u^n)' = n·u^(n-1)·u'，n=3
      作用于: (x^2 + 1)^3
      适用域: 恒成立
  [6] 幂规则 (u^n)' = n·u^(n-1)·u'，n=2
      作用于: (x^2 + 1)^2
      适用域: 恒成立
f'(x) = 3 * (x^2 + 1)^2 * 2 * x + 2 * (x^2 + 1) * 2 * x
```

样例 3：ln 导数保留 `x>0` 定义域，非整数次幂要求 `u>0`：

```
f(x) = ln(x) + x^(1/2)
  [2] 链式规则 (ln u)' = u'/u
      作用于: ln(x)
      适用域: 要求 u>0（保留 ln 的定义域）
  [3] 幂规则 (u^n)' = n·u^(n-1)·u'，n=1/2
      作用于: x^(1/2)
      适用域: 要求 u>0（非整数次幂）
f'(x) = x^(-1) + 1/2 * x^(-1/2)
```

样例 4：常数幂 0 的语义（约定 `a^0=1`，含 `0^0=1`）：

```
f(x) = x^0 + 0^0
  [1] 常数幂 0：约定 u^0 = 1（含 0^0 = 1），常数导数为 0
      作用于: x^0
      适用域: 约定 a^0=1（含 0^0=1 的形式约定），在 u 的定义域上成立
f'(x) = 0
```

样例 5：有理数精确计算，`(1/3)x³ + (1/2)x²` 的导数精确为 `x² + x`：

```
f(x) = 1/3 * x^3 + 1/2 * x^2
化简步骤:
  [3] 系数合并（乘法结合律）
      1/3 * 3 * x^2  ==>  1 * x^2
      适用域: 恒等变换，定义域不变
f'(x) = x^2 + x
```

完整输出见 `python3 demo.py`。

## 测试结果

```
$ python3 -m unittest test_differentiator -v
test_chain_sin ... ok
test_const_and_var ... ok
test_exp_and_tan ... ok
test_general_power_domain ... ok
test_log_keeps_positive_domain ... ok
test_noninteger_and_negative_power_domain ... ok
test_power_rule_exact_fraction ... ok
test_sum_and_product ... ok
test_exponent_not_expanded ... ok
test_interning ... ok
test_shared_subgraph_derived_once ... ok
test_fraction_coefficients_exact ... ok
test_fraction_fold ... ok
test_rational_power_fold ... ok
test_coefficient_merge ... ok
test_diff_of_pow_zero_is_zero ... ok
test_no_domain_widening ... ok
test_pow_zero_convention ... ok
test_zero_factor_requires_entire ... ok
test_zero_power_zero ... ok

----------------------------------------------------------------------
Ran 20 tests in 0.001s

OK
```
