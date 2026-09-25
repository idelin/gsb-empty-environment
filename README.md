# tinymat — 跨步视图上的批量矩阵乘与自动微分（纯标准库，Python 3.11）

不依赖任何自动微分或张量库。`Tensor` 是共享扁平缓冲区上的**跨步视图**
（`data` / `shape` / `strides` / `offset`），广播用 stride-0 轴表示，
从不为绕过布局处理而完整复制广播输入。

## 功能

- `matmul(a, b, workspace_budget=...)`：批量 matmul，numpy 语义
  - 批维广播（右对齐，尺寸 1 可扩展）
  - 向量特例：`(n,)@(n,)`→标量、`(n,)@(…,n,m)`、`(…,m,n)@(n,)`
  - 内维 0 有定义：结果为零矩阵，梯度为零
  - 非连续输入（转置/切片/负步长/广播视图及其组合）按 stride 原地读取
  - 分块内核：A/B/C 三块 tile 是唯一工作区，`3*bs² ≤ workspace_budget`，
    块大小由额度推出；额度 < 3 抛 `ValueError`
- `matmul_backward(seed, a, b)`：VJP，梯度张量**沿用输入的布局**
  （shape/strides/offset），按物理存储单元累加 —— 广播批维的归约、
  stride-0 视图、重叠切片等物理共享存储的贡献自动求和；
  A 被广播到多个批次时 grad_A 即各批贡献之和
- `matmul_jvp(a, b, da, db)`：`dA@B + A@dB`
- `matmul_hvp(seed, a, b, u, v)`：标量化损失 `L=<seed,A@B>` 的 HVP，
  `H·(u,v) = (S@Vᵀ, Uᵀ@S)`，直接复用 VJP 机制，故广播/向量提升/
  视图轴变换全部可组合
- `tinymat.verify`：独立数值验证工具（物理单元中心差分、方向一阶/
  二阶差分、误差分类）

## 可微性与数值误差判定

`matmul` 对两个操作数都是双线性（多项式、C^∞）映射；向量提升只插入
尺寸 1 的轴，广播只是内存复用 —— **不存在不可微点**。内维 0 是常值零
映射，同样光滑。因此解析导数与有限差分的任何偏差只可能是：

- `"numerical"`：落在截断 + 舍入误差模型内（中心差分：
  一阶 `err ~ C·h²·|f'''| + ε|f|/h`，二阶 `err ~ C·h²·|f⁴| + 4ε|f|/h²`），
  由 `classify_discrepancy` 按保守 slack 判定；
- `"genuine"`：超出模型上界 —— 真正的实现 bug（对其他算子则可能是
  不可微点）。

测试对随机小矩阵在**物理存储单元**上做中心差分（共享单元被扰动时所有
逻辑元素同时变化，与物理累加的梯度严格对应），HVP 同时用梯度方向差分
和损失的方向二阶差分双重对照。

## 独立验证

- 前向：与 `tests/naive_ref.py` 中**共享零代码**的嵌套列表三重循环对照，
  核心形状 m,k,n ∈ {0,1,2,3} 穷举 + 广播批组合 + 向量特例 + 非连续视图
- 反向：物理单元中心差分（h=1e-5）逐单元比对
- JVP：方向差分对照；HVP：梯度差分 + 方向二阶差分对照
- 广播归约：与逐批梯度显式求和比对；共享存储：6 个别名单元的梯度 =
  6 份逻辑贡献之和（与 FD 一致）

## 运行

```bash
python3.11 -m unittest discover -s tests -t . -v   # 测试
python3.11 examples/run_example.py                 # 样例
```

## 运行样例（实际输出）

```
=== 1. Batched matmul, A broadcast to multiple batches ===
A (2,3) @ B (3,3,2) ->             shape (3, 2, 2)
  batch 0                          [[4.0, 5.0], [10.0, 11.0]]
  batch 1                          [[4.0, 8.0], [13.0, 20.0]]
  batch 2                          [[9.0, 1.0], [21.0, 4.0]]

=== 2. Backward: grad_A sums over the 3 broadcast batches ===
grad_A (reduced to (2,3))          [[13.0, 10.0, 10.0], [13.0, 10.0, 10.0]]
grad_B shape                       (3, 3, 2)

=== 3. Vector special cases (numpy matmul semantics) ===
v @ v (scalar)                     14.0
v @ B[0] ((3,)@(3,2))              [4.0, 5.0]
A @ v ((2,3)@(3,))                 [14.0, 32.0]

=== 4. Non-contiguous views compose (no copies) ===
view shape / buffer length         (5, 2, 3) / 24
view @ W (batch 4)                 [[44.0, -6.0], [50.0, -5.0]]

=== 5. JVP: d(A@B)[dA,dB] = dA@B + A@dB ===
JVP batch 0                        [[0.30000000000000004, 0.1], [0.5, 0.5]]

=== 6. HVP of L = <seed, A@B> along (U, V) ===
HVP w.r.t. A                       [[0.0, 0.0, 3.0], [0.0, 0.0, 3.0]]
HVP w.r.t. B batch 0               [[1.0, 1.0], [1.0, 1.0], [0.0, 0.0]]

=== 7. Edge cases ===
inner dim 0 -> zeros               shape (2, 4), all zero: True
grad of 6-way shared cell          [42.0] (= 6 logical contributions summed physically)
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests -t . -v
test_broadcast_gradient_sums_over_batches ... ok
test_grad_matches_finite_differences (dense|batched|broadcast_a|
  broadcast_both|vec_vec|vec_mat|mat_vec|views|broadcast_view|k_zero) ... ok
test_inner_dim_zero_gradients ... ok
test_physical_shared_storage_accumulates ... ok
test_jvp_vs_directional_fd / test_jvp_composes_with_views ... ok
test_hvp_vs_gradient_fd / test_hvp_vs_second_directional_difference /
  test_hvp_composes_with_views_and_broadcast ... ok
test_core_shapes_0_to_3 (64 穷举) / test_batch_broadcast_combos /
  test_vector_special_cases / test_inner_dim_zero_is_defined ... ok
test_transposed_operands / test_strided_slices /
  test_broadcast_views_not_copied / test_composed_axis_transform ... ok
test_budget_invariance (3..1e9) / test_budget_too_small_raises /
  test_batched_blocked ... ok
test_numerical_vs_genuine ... ok
----------------------------------------------------------------------
Ran 29 tests in ~1s

OK
```

（另用 numpy 对前向做了一次性交叉核对：广播/向量提升语义一致；
numpy 仅用于人工核对，不是依赖。）

## 结构

```
tinymat/tensor.py    跨步视图张量：transpose/slice_dim/broadcast_to/组合
tinymat/matmul.py    分块前向（工作区额度）、VJP、JVP、HVP
tinymat/verify.py    有限差分与误差分类工具
tests/               unittest：穷举对照、FD 验证、边界与视图组合
examples/run_example.py
```
