# 纯 Python 二阶反向微分与 HVP

这个小型示例在 CPU 上用嵌套 Python 列表表示张量，不依赖 NumPy、PyTorch 或任何自动微分/张量库。反向传播使用显式计算图，支持在求一阶梯度时选择性构建梯度图，再对梯度图反向传播得到 Hessian 向量积。

## 文件

- `mini_ad.py`：张量、算子、VJP、反向传播和 `hvp`。
- `example_hvp.py`：二次函数的一阶梯度与 HVP 运行样例。
- `test_mini_ad.py`：使用标准库 `unittest` 的独立数值验证。

## 设计要点

- `grad(..., create_graph=True)` 返回的梯度仍是带图的 `Tensor`，可再次反向传播。
- 反向传播顺序为前向 DAG 的逆拓扑序，共享节点只遍历一次，保留共享子表达式结构。
- 梯度状态是每次 `grad` 调用内的局部字典，不写入 `x.grad`，重复调用不会累加上次结果。
- 原始数值辅助函数总是生成新列表，梯度累加不原地修改输入或返回缓冲。
- 常数、非追踪输入的贡献不会连接到叶子；因此常数梯度再次求导为零。

测试中的独立有限差分验证没有调用本库的梯度实现，而是用中心差分分别计算方向扰动后的梯度差：

```text
H v ≈ (∇f(x + h v) - ∇f(x - h v)) / (2h)
```

## 运行

```bash
python3.11 -m unittest -v
python3.11 example_hvp.py
```

## 本次样例输出

```text
test_constant_gradient_has_zero_second_derivative ... ok
test_quadratic_hvp_matches_analytic_and_numeric_values ... ok
test_repeated_differentiation_has_no_accumulated_residue ... ok
test_returned_gradient_buffers_are_independent ... ok
test_shared_subexpression_is_traversed_once ... ok

----------------------------------------------------------------------
Ran 5 tests in 0.008s

OK
```

```text
--- example ---
f(x)        = 5
grad f      = [-2.0, -2.0, 6.0]
expected    = [-2.0, -2.0, 6.0]
H v         = [8.0, 0.0, -8.0]
expected Hv = [8.0, 0.0, -8.0]
```
