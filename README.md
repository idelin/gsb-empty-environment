# minivmap — 纯函数图的批处理变换（Python 3.11，仅标准库）

把**单样本纯函数图**变换为**批处理图**，区分映射轴与共享参数，输出轴跟踪证书，
并对前向与反向结果同时做逐样本循环对照，外加有限差分独立数值验证。
不依赖任何自动微分或张量库：小张量的广播、转置、归约、batched matmul 全部手写。

## 设计

- `minivmap/tensor.py` — 扁平存储的小张量库（广播、transpose/reshape/reduce、
  batched matmul、moveaxis/stack/slice、unbroadcast）。
- `minivmap/graph.py` — 纯函数图 IR（`GraphBuilder`）、静态形状推断、前向求值。
- `minivmap/batch.py` — 批变换（vmap）：
  - 映射轴统一**重排到前导轴**（`in_axes` 可为每个输入指定不同轴，自动插入
    transpose 并记入证书）；共享参数（`in_axes=None`）保持原形状，靠前导轴广播复用。
  - 映射轴在 transpose / reduce / reshape 后正确传播（轴偏移重写 + 每节点轴跟踪表）。
  - 批大小不一致、形状不符在变换前**预先拒绝**。
  - 无法保持样本独立性的操作（`emit` 副作用、`cross_sample_sum` 跨样本集合操作）
    被拒绝，证书记录原因与 BFS 求得的**最小依赖路径**（映射输入 → 违规节点）。
  - 输出 `Certificate`（JSON 可序列化）：batch_size、in_axes、轴移动、
    每节点 batch_dims/shape、输出轴跟踪、拒绝记录。
  - 变换可嵌套：`batch_transform(batch_transform(g))` 即 vmap(vmap(f))，
    每层新增一个前导映射轴。
- `minivmap/grad.py` — 反向模式梯度，对单样本图与批处理图通用；
  广播感知 VJP（unbroadcast）使**共享参数梯度自动跨样本求和**，
  映射输入得到逐样本梯度。
- `minivmap/verify.py` — 逐样本循环参考实现（前向堆叠 + 逐样本反向，
  共享梯度求和、映射梯度堆叠并移回映射轴），以及中心有限差分独立验证。

## 运行

```bash
python3 -m unittest discover -s tests -v   # 18 个测试
python3 examples/demo_mlp.py               # 端到端演示（含证书与拒绝样例）
```

## 实测结果（本仓库运行输出）

测试：

```
Ran 18 tests in 0.032s
OK
```

演示要点（`examples/demo_mlp.py`）：

```
3. Verification: batched vs per-sample loop (forward AND reverse)
  forward:n9       0.000e+00
  grad:w1          0.000e+00   # 共享参数梯度 = 逐样本梯度跨样本求和
  grad:x           0.000e+00   # 映射输入梯度 = 逐样本梯度堆叠
4. Independent check: finite differences vs batched reverse mode
  |grad_w1 - finitediff|max = 1.916e-09
5. Nested batch mapping: vmap(vmap(f)) with x (2, 5, 3)
  output axis track = {'node': 'n9', 'batch_dims': [0], 'shape': [2, 5]}
6. Mixed mapped axes: x stored features-first (3, 5), mapped on axis 1
  axis_moves = [{'input': 'x', 'from_axis': 1, 'to_axis': 0, ...}]
7. Rejections
  side-effect op rejected:   dependency_path: ["x", "n0", "n1"]   # emit
  cross-sample collective:   dependency_path: ["x", "n0", "n1"]   # cross_sample_sum
  batch-size mismatch rejected early: input 'y' has 4 samples, expected 5
```
