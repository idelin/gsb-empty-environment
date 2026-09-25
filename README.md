# minigrad — 反向传播 + Hessian 向量积（纯标准库）

Python 3.11，仅使用标准库。CPU 小规模稠密张量（扁平 `float` 列表 + shape），
不依赖任何自动微分框架或张量库。

## 设计要点

- **反向计算**：动态建图，拓扑序反向扫描（`minigrad.py` 中 `_reverse_sweep`）。
- **可选梯度图**：`grad(..., create_graph=True)` 让反向传播本身也被记录为图，
  从而支持高阶导数；`hvp()` 即"先建梯度图，再对 `sum(grad * v)` 求导"。
- **保留共享结构**：共享子图只建一次，多个使用点的梯度向同一节点累加。
- **不原地污染**：梯度累加一律生成新张量（`old + new`），从不就地修改已有
  梯度缓冲；每次 `grad` 调用先丢弃旧 `.grad` 再重算，重复求导不会累加上次残留。

## 文件

- `minigrad.py` — 引擎：`Tensor`、`grad`、`hvp`，算子 `add/mul/matmul/sum/exp/T`
- `test_minigrad.py` — 12 个 unittest 用例（含独立有限差分数值验证）
- `demo.py` — 运行样例

## 用法

```python
from minigrad import Tensor, grad, hvp

A = Tensor([2.0, 0.5, -1.0,
            0.5, 3.0, 0.25,
            -1.0, 0.25, 1.5], (3, 3))      # 对称矩阵
b = Tensor([1.0, -2.0, 0.5], (3, 1))
x = Tensor([0.5, -1.0, 2.0], (3, 1), requires_grad=True)
v = Tensor([1.5, 0.5, -1.0], (3, 1))

f = (x.T @ A @ x).sum() * 0.5 + (b * x).sum()
g = grad(f, x)                 # 一阶梯度 = A x + b
hv = hvp(f, x, v)              # Hessian 向量积 = A v
```

## 运行样例（`python3.11 demo.py`）

```
== 1. Quadratic: f(x) = 0.5 x'Ax + b'x, HVP should equal A v ==
  HVP (engine)                [+4.25000000, +2.00000000, -2.87500000]   expected [+4.25000000, +2.00000000, -2.87500000]   max|err|=0.00e+00
== 2. Nonlinear: f(x) = exp(w.x) + sum(x^3), HVP vs finite differences ==
  HVP (engine)                [-19.86654743, +43.03309486, -97.46618971]   expected [-19.86655898, +43.03311751, -97.46623588]   max|err|=4.62e-05
== 3. Linear: f(x) = c'x, Hessian is zero ==
  HVP (engine)                [+0.00000000, +0.00000000, +0.00000000]   expected [+0.00000000, +0.00000000, +0.00000000]   max|err|=0.00e+00
== 4. Repeated differentiation leaves no residue ==
  grad stable across calls:     True
  hvp  stable across calls:     True
  input data unpolluted:        True
```

## 测试结果（`python3.11 -m unittest test_minigrad -v`）

```
test_second_derivative_is_zero ... ok
test_second_grad_of_scalar_is_zero ... ok
test_grad_matches_finite_differences ... ok
test_hvp_matches_finite_differences ... ok
test_input_data_not_polluted ... ok
test_plain_backward_returns_plain_tensor ... ok
test_repeated_grad_does_not_accumulate ... ok
test_repeated_hvp_does_not_accumulate ... ok
test_gradient_matches_analytic ... ok
test_hvp_matches_analytic ... ok
test_hvp_shared_subgraph ... ok
test_shared_subgraph_first_and_second_order ... ok

----------------------------------------------------------------------
Ran 12 tests in 0.001s

OK
```

覆盖要点：

- 二次函数 HVP 与解析值 `A v` 完全一致（误差 0）
- 常数梯度（线性函数）二阶导为 0
- 独立数值验证：纯 `float` + `math.exp` 的中心差分，不经过 Tensor 引擎
- 共享子图一阶/二阶导数正确（`z = y*y + y`, `y = x*x`）
- 重复求导不累加残留，旧梯度缓冲与输入数据均不被原地污染
