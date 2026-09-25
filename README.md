# sparse-truncator

连续矩阵链 `f(x) = M_L ... M_1 x` 的稀疏截断器：在**全局 L∞ 误差预算**内
贪心决定各层阈值与非零预算，输出**逐层保守误差传播证书**。纯 Python 3.11
标准库实现，不依赖 NumPy/SciPy；尺寸与索引全部显式输入。

## 误差模型（为什么证书是保守的）

设 `M_hat_i = M_i - E_i`（`E_i` 为被截断的条目），精确中间量为 `z_{i-1}(x)`，
则真实偏差有精确望远镜分解：

```
f(x) - f_hat(x) = sum_i  M_hat_L ... M_hat_{i+1}  E_i  z_{i-1}(x)
```

因此全局上界为逐层贡献之和（而非各层独立满足同一预算）：

```
|contribution_i| <= |M_hat_L| ... |M_hat_{i+1}|  |E_i|  mag(Z_{i-1})
bound           = || sum_i contribution_i ||_inf
```

- `Z_{i-1}`：用**原始矩阵**做区间前向传播得到的精确中间量包络；
  负系数通过端点交换正确传播（`Interval.scale`），输入区间不确定性随链传播。
- 后层大范数通过绝对值矩阵后缀积 `U[i+1]` 放大前层小误差，证书覆盖真实偏差。
- 证书纯先验（区间/范数），**从不使用观察误差代替上界**。

## 功能

- `ChainTruncator(matrices, input_box)`：`(nrows, ncols, [(r,c,v),...])` 显式输入。
- `allocate(error_budget, max_total_nnz=None)`：按边际误差增量贪心截断，
  等值按稳定坐标 `(layer, row, col)` 决胜；每次接受前重算精确证书。
  预算不可满足时回滚全部截断、保留精确部分并报告 `satisfied=False`。
- `certificate()`：逐层阈值、非零数、传播后 L∞ 贡献及全局上界。
- `drop(...) / undo() / reset()`：可撤销的近似选择，回滚恢复原矩阵与误差账本。
- `set_input_interval(j, lo, hi)`：局部输入修改，证书经前向区间传播增量更新，可撤销。
- `check_certificate(truncator)`：枚举输入盒全部 `2^n` 个顶点（偏差关于 x 线性、
  L∞ 关于 x 凸，最大值必在顶点），用自写稠密实现核验证书覆盖真实偏差。

## 运行

```bash
python3.11 examples/demo.py                  # 可运行样例
python3.11 -m unittest discover -s tests -t . -v   # 23 个测试
```

## 布局

- `sparse_truncator/intervals.py` — 区间算术（负系数端点交换）
- `sparse_truncator/dense.py` — 自写稠密矩阵运算（无 NumPy/SciPy）
- `sparse_truncator/chain.py` — 截断器、证书、贪心分配、撤销
- `sparse_truncator/verify.py` — 顶点枚举核验器
- `examples/demo.py` — 端到端样例
- `tests/test_sparse_truncator.py` — unittest 套件
