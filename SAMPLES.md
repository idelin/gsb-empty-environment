# 运行样例与测试结果

环境：Python 3.11.15（macOS, arm64），仅标准库。

## `python3.11 demo.py`

```
========================================================================
样例 1：x->y、y->x 同时代换 = 交换；顺序代换则被污染
========================================================================
### 同时代换（正确：交换）
原式      : x + 2*y
同时代换  : {'x': 'y', 'y': 'x'}
  步骤1    : 同时代换：一次性遍历，叶子变量各查一次映射表，右值整体植入、互不递归
    适用域: Q[x1,...,xn] 上的环同态；映射值可为任意多项式（含变量）
  步骤2    : 规范化：常数合并、同类项合并、幂合并；映射为 0 的因子按零吸收律消去整项
    适用域: Q[x1,...,xn]；Q 特征为 0，系数相等/相消均可精确判定
  步骤3    : 膨胀估计：展开后项数 <= 2（保守上界，纯结构公式，未生成单项式）
    适用域: 组合上界：加法取和、乘法取积、幂取 C(总次数+变量数, 变量数)
  步骤4    : 完全展开：2 项，系数全部为精确有理数
    适用域: Q[x1,...,xn]；单项式按变量名排序，系数为 fractions.Fraction
展开结果  : 2*x + y  （2 项）

对照·顺序代换: x->y 得 3*y，再 y->x 得 3*x  <- 顺序污染，全部塌缩成 x

========================================================================
样例 2：代换使因子为零 -> 零吸收律消去整项
========================================================================
### x -> y，(y-y)*z 整项消去
原式      : (x - y)*z + x*y
同时代换  : {'x': 'y'}
  步骤1    : 同时代换：一次性遍历，叶子变量各查一次映射表，右值整体植入、互不递归
    适用域: Q[x1,...,xn] 上的环同态；映射值可为任意多项式（含变量）
  步骤2    : 规范化：常数合并、同类项合并、幂合并；映射为 0 的因子按零吸收律消去整项
    适用域: Q[x1,...,xn]；Q 特征为 0，系数相等/相消均可精确判定
  步骤3    : 膨胀估计：展开后项数 <= 3（保守上界，纯结构公式，未生成单项式）
    适用域: 组合上界：加法取和、乘法取积、幂取 C(总次数+变量数, 变量数)
  步骤4    : 完全展开：1 项，系数全部为精确有理数
    适用域: Q[x1,...,xn]；单项式按变量名排序，系数为 fractions.Fraction
展开结果  : y**2  （1 项）

========================================================================
样例 3：有理数精确计算（Fraction，无浮点误差）
========================================================================
### x -> y + 1/3
原式      : 2/3*x*y + 1/2*x**2 - y**2
同时代换  : {'x': '1/3 + y'}
  步骤1    : 同时代换：一次性遍历，叶子变量各查一次映射表，右值整体植入、互不递归
    适用域: Q[x1,...,xn] 上的环同态；映射值可为任意多项式（含变量）
  步骤2    : 规范化：常数合并、同类项合并、幂合并；映射为 0 的因子按零吸收律消去整项
    适用域: Q[x1,...,xn]；Q 特征为 0，系数相等/相消均可精确判定
  步骤3    : 膨胀估计：展开后项数 <= 8（保守上界，纯结构公式，未生成单项式）
    适用域: 组合上界：加法取和、乘法取积、幂取 C(总次数+变量数, 变量数)
  步骤4    : 完全展开：3 项，系数全部为精确有理数
    适用域: Q[x1,...,xn]；单项式按变量名排序，系数为 fractions.Fraction
展开结果  : 1/18 + 5/9*y + 1/6*y**2  （3 项）

========================================================================
样例 4：膨胀估计超预算 -> 停止展开，保留未展开的精确形式
========================================================================
### (a+b+y+z)**10 保守上界 1001 > 预算 100
原式      : (x + y + z)**10
同时代换  : {'x': 'a + b'}
  步骤1    : 同时代换：一次性遍历，叶子变量各查一次映射表，右值整体植入、互不递归
    适用域: Q[x1,...,xn] 上的环同态；映射值可为任意多项式（含变量）
  步骤2    : 规范化：常数合并、同类项合并、幂合并；映射为 0 的因子按零吸收律消去整项
    适用域: Q[x1,...,xn]；Q 特征为 0，系数相等/相消均可精确判定
  步骤3    : 膨胀估计：展开后项数 <= 1001（保守上界，纯结构公式，未生成单项式）
    适用域: 组合上界：加法取和、乘法取积、幂取 C(总次数+变量数, 变量数)
  步骤4    : 上界 1001 超过预算 100，停止展开，返回未展开的精确形式
    适用域: 预算策略；未展开 AST 与原多项式严格相等，不是截断近似
预算中断  : 估计项数上界 1001 > 预算 100
未展开形式: (a + b + y + z)**10  （精确等式，非截断）

对照·预算 2000 时正常展开：286 项（保守上界 1001，上界偏保守是安全的，宁停勿错）
  展开式: 720*a*b*y*z**7 + 2520*a*b*y**2*z**6 + 5040*a*b*y**3*z**5 + 6300*a*b*y**4*z**4 + 5040*a*b*y**5*z**3 + 2520*a*b*y**6*z**2 + 720*a*b*y**7*z + 90*a*b*y**8 + 90*a*b* ...（共 286 项，此处截断显示）
```

## `python3.11 -m unittest discover -s tests -v`

```
test_abort_is_not_a_truncated_equality (test_simsub.BudgetTest.test_abort_is_not_a_truncated_equality) ... ok
test_abort_returns_unexpanded_exact_form (test_simsub.BudgetTest.test_abort_returns_unexpanded_exact_form) ... ok
test_budget_boundary_allows_exact_fit (test_simsub.BudgetTest.test_budget_boundary_allows_exact_fit) ... ok
test_negative_budget_rejected (test_simsub.BudgetTest.test_negative_budget_rejected) ... ok
test_fraction_coefficients_are_exact (test_simsub.ExactRationalTest.test_fraction_coefficients_are_exact) ... ok
test_no_float_anywhere (test_simsub.ExactRationalTest.test_no_float_anywhere) ... ok
test_parse_rejects_imprecise_or_invalid_input (test_simsub.ExactRationalTest.test_parse_rejects_imprecise_or_invalid_input) ... ok
test_format_poly_is_deterministic (test_simsub.FormatTest.test_format_poly_is_deterministic) ... ok
test_mapping_may_introduce_new_variables (test_simsub.SimultaneousSubstitutionTest.test_mapping_may_introduce_new_variables) ... ok
test_partial_substitution_keeps_other_variables (test_simsub.SimultaneousSubstitutionTest.test_partial_substitution_keeps_other_variables) ... ok
test_rotation_of_three_variables (test_simsub.SimultaneousSubstitutionTest.test_rotation_of_three_variables) ... ok
test_sequential_substitution_would_be_contaminated (test_simsub.SimultaneousSubstitutionTest.test_sequential_substitution_would_be_contaminated) ... ok
test_swap_x_y_is_exchange_not_collapse (test_simsub.SimultaneousSubstitutionTest.test_swap_x_y_is_exchange_not_collapse) ... ok
test_aborted_run_mentions_budget_stop (test_simsub.StepsNarrationTest.test_aborted_run_mentions_budget_stop) ... ok
test_every_step_states_its_domain (test_simsub.StepsNarrationTest.test_every_step_states_its_domain) ... ok
test_bound_never_underestimates (test_simsub.TermBoundSoundnessTest.test_bound_never_underestimates) ... ok
test_substitution_homomorphism_on_random_points (test_simsub.TermBoundSoundnessTest.test_substitution_homomorphism_on_random_points) ... ok
test_cancellation_after_substitution (test_simsub.ZeroCancellationTest.test_cancellation_after_substitution) ... ok
test_everything_cancels_to_zero_polynomial (test_simsub.ZeroCancellationTest.test_everything_cancels_to_zero_polynomial) ... ok
test_substitute_zero_constant (test_simsub.ZeroCancellationTest.test_substitute_zero_constant) ... ok
test_zero_factor_kills_whole_term (test_simsub.ZeroCancellationTest.test_zero_factor_kills_whole_term) ... ok

----------------------------------------------------------------------
Ran 21 tests in 0.541s

OK
```
