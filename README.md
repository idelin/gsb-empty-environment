# 多变量稀疏多项式库（Python 3.11，仅标准库）

有理数精确系数的多变量多项式：按单项式指数向量稀疏存储，合并同类项并
canonical 排序，支持加法、乘法（含内存额度与外存归并）、偏导、惰性展开
与预算恢复，并附独立稠密小域参考实现的交叉检验。不调用任何符号代数库。

## 文件

| 文件 | 内容 |
| --- | --- |
| `poly.py` | 核心 `Polynomial`：指数向量→`Fraction` 字典，零项回收，加/乘/偏导，外存归并乘法 |
| `lazy.py` | 惰性节点（`Leaf/Add/Mul/Deriv`）、`Budget` 配额求值、可恢复的 `Partial` |
| `dense_ref.py` | 独立稠密 GF(p) 数组参考实现，用于交叉检验 |
| `test_poly.py` | unittest 套件（23 个测试） |
| `demo.py` | 运行样例（每步变换标注适用域） |

## 设计要点

- **存储**：`{指数向量 tuple: Fraction}`；构造与每步运算后系数为零的项立即回收。
- **canonical 序**：分级字典序（先总次数后字典序），输出按次数降序，同一数学对象字符串唯一。
- **负指数拒绝**：构造期校验，负指数/非整数指数抛 `ValueError`；零变量常数（`nvars=0`，指数 `()`）有定义。
- **内存额度与外存归并**：`multiply(p, q, mem_budget=k)` 乘积项数超额度时，乘积项流式生成、缓冲区满即排序落盘为有序归并段，最后 `heapq.merge` k 路归并，重复单项式精确相加、零项回收；归并段用后删除。
- **限制项数膨胀**：`Budget(max_terms=...)` 为硬上限，任一累加器超过即抛 `TermLimitExceeded`。
- **延迟展开与可恢复**：`lazy.evaluate(node, Budget(quota=q))` 预算耗尽时返回 `Partial(acc, rest)`——已累积部分 + 可继续的惰性表达式（非截断）；`lazy.resume` 继续推进，`evaluate_full` 循环至完成并返回峰值项数。所有改写均为分配律/求导线性性的恒等变换，故任意暂停/恢复切片下 canonical 结果相同。
- **独立参考检验**：`dense_ref.DenseModP` 用稠密数组在 GF(p) 上独立实现加/乘/偏导，与稀疏有理结果 mod p 投影逐项比对，并验证偏导交换关系 `∂i∂j = ∂j∂i`。
- **适用域标注**：每个公开运算的 docstring 首行注明适用域（任意环 / 任意交换环 / 特征 0 整环等），`demo.py` 每步打印。

## 运行

```bash
python3.11 demo.py                 # 功能演示
python3.11 -m unittest test_poly -v  # 测试
```

## 运行样例（`python3.11 demo.py` 节选）

```
--- 3. 乘法：交叉项精确合并 (x+y)(x-y) = x^2 - y^2
    适用域: 任意交换环（分配律）
    (x0 + x1) * (x0 - x1) = (1)*x0^2 + (-1)*x1^2   （项数 2）

--- 6. 稀疏乘法：内存额度受限时外存归并
    适用域: 任意交换环；归并段排序 + k 路归并
    a 项数=9, b 项数=11, 乘积项数=88
    外存归并(mem_budget=8) 与内存结果一致: True

--- 7. 惰性节点：预算不足返回 Partial（可继续），非截断
    quota=5 首切片 -> Partial(可继续)，已累积项数=0
    quota=5 连续恢复 -> 完成，切片数=274，峰值项数=82
    与一次性求值一致: True

--- 8. 随机暂停/恢复：canonical 输出不变，记录峰值项数
    10 组随机配额 canonical 全部一致: True

--- 9. 项数膨胀硬上限：超过即 TermLimitExceeded
    max_terms=4 -> TermLimitExceeded: 项数 9 超过膨胀上限 max_terms=4

--- 10. 独立稠密 GF(101) 参考：交叉检验运算与偏导交换
    加法一致=True  乘法一致=True  乘积偏导一致=True
    稠密侧偏导交换 d/dx0 d/dx1 == d/dx1 d/dx0: True
```

## 测试结果（`python3.11 -m unittest test_poly -v`）

```
test_add_associative_commutative ... ok
test_combine_like_terms_and_canonical_order ... ok
test_deriv_commutes ... ok
test_deriv_known_value ... ok
test_leibniz_product_rule ... ok
test_mul_cross_terms_merge ... ok
test_mul_known_value_and_distributive ... ok
test_negative_exponent_rejected ... ok
test_nvars_mismatch_rejected ... ok
test_x_minus_x_is_zero ... ok
test_zero_term_reclaimed_on_construction ... ok
test_zero_variable_constant_defined ... ok
test_add_mul_crosscheck ... ok
test_dense_zero_variable ... ok
test_deriv_crosscheck_and_commutation ... ok
test_external_matches_inplace ... ok
test_external_spill_dir_cleaned ... ok
test_external_zero_reclaim ... ok
test_budget_exhausted_returns_resumable_not_truncated ... ok
test_deriv_node_distributes_over_partial ... ok
test_lazy_matches_eager_random ... ok
test_random_pause_resume_canonical_identical ... ok
test_term_inflation_hard_limit ... ok
----------------------------------------------------------------------
Ran 23 tests in ~3-5s

OK
```
