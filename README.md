# 精确有理 Newton / Hermite 插值器

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理运算，**不调用任何
符号代数库**），用汇合差商（confluent divided differences）构造插值多项式，
支持：

- 增量加入观测（函数值 / 任意阶导数值）与重复节点检查；
- 输出 **Newton 形式**（节点 + 对角线差商系数）或**规范幂基**；
- Hermite 重复节点的导数约束（同一节点的 0..m 阶导数构成连续汇合块）；
- 增量**撤回**任意观测，并复用差商表前缀、只重建后缀子三角；
- 重复 `x` 不同 `y` 的冲突诊断（同值幂等、异值给异常）；
- 结构错误诊断（缺低阶导数、把同节点块拆开等）；
- **独立线性方程求解器**（高斯–若尔当消元）参考核验所有值与导数约束，
  不相容时输出见证向量（`w·A=0` 而 `w·b≠0` 的证书）；
- 报告每个阶段分子 / 分母的实际最大二进制位长。

## 文件

- `rational_interp.py` — 核心模块（`Poly`、`Interpolator`、`linear_solve`）。
- `demo.py` — 运行样例：`python3 demo.py`。
- `sample_output.txt` — 样例实际运行输出。
- `test_interp.py` — 27 个 `unittest` 用例：`python3 -m unittest -v`。

## 快速使用

```python
from fractions import Fraction as F
from rational_interp import Interpolator, DuplicateConflictError

itp = Interpolator()
itp.observe(1, 1)                 # P(1) = 1
itp.observe(2, F(1, 2))           # P(2) = 1/2
itp.observe(2, F(-1, 4), 1)      # P'(2) = -1/4 （同一节点块内连续给）
nodes, coeffs = itp.newton_form() # Newton 形式
print(itp.polynomial())           # 规范幂基
print(itp.verify().ok)            # 全部节点精确核验
print(itp.reference_check().agree)  # 独立线性求解器交叉核验
```

撤回（不止删最后一个系数——内部复用前缀、重算后缀）：

```python
itp.forget(2)                  # 撤回内部节点 x=2 的函数值及其高阶块
itp.forget(0, derivative_order=2)          # 只撤回某节点最高一阶导数
itp.forget(0, cascade=True)    # 撤回函数值并连同其高阶导数一起撤回
```

## 数据模型与每步变换的适用域

观测是一组约束 `(x, k) -> y`，表示 `P^(k)(x) = y`（`k=0` 即函数值）。
内部把每个不同节点 `x` 排成一个**连续块**，块内依次携带 0..m_x 阶导数；
序列 `z_0,...,z_{n-1}` 中同节点出现 `m+1` 次。

1. **汇合差商表** `tab[i][j] = f[z_i,...,z_{i+j}]`
   - 两端点不同（普通差商）：`(tab[i+1][j-1] - tab[i][j-1]) / (x_{i+j}-x_i)`。
     适用域：分母 `x_{i+j} ≠ x_i`，即两端点必须是不同节点。
   - 两端点相同（汇合项）：`f^(j)(x) / j!`。
     适用域：同一节点的 0..j 阶导数值都已给出（连续块前提），特征 0 域
     （`j! ≠ 0`，有理数域满足）。
   - 填充顺序：必须按阶数 `j` 升序（列优先），因为第 j 列只依赖第 j-1 列。
2. **Newton → 规范幂基**：`P = Σ c_j ∏_{i<j}(x-z_i)`，用精确的
   `(x-r)` 乘法逐级展开。适用域：节点可任意排序（含重复汇合节点）；
   表示与节点顺序无关，换序只改变 Newton 系数而不改变最终幂基多项式。
3. **增量加 / 撤的复用**：变化从位置 `p` 开始时，`i+j < p` 的旧表项描述的
   是未变化前缀的差商，数学上可整体复用；只重算 `i+j ≥ p` 的子三角。
   因此撤回一个内部观测后，受影响的是**后缀全部系数**，不能只删末项。
4. **独立参考核验**：把未知幂基系数 `a_0..a_{n-1}` 代入约束
   `P^(k)(x) = Σ_{t≥k} a_t·t!/(t-k)!·x^{t-k}`，得到 Hermite–Vandermonde
   线性系统，用独立的精确高斯–若尔当消元求解并与差商结果逐项比较。
   适用域：任意 m×n 有理增广矩阵（超定 / 欠定均可）。不相容时通过对
   "行来源向量"同步做行组合，产出见证 `w` 满足 `w·A=0`、`w·b≠0`。
5. **位长统计**：记录输入、差商、参考矩阵、参考解四个阶段所有有理数
   分子 / 分母的最大 `bit_length`，用于观察精确运算的位长增长。

## 诊断规则

- 同一 `(x,k)` 再次加入相同值 → 幂等（无操作）；不同值 → `DuplicateConflictError`。
- 先给高阶而缺低阶导数；或同节点块被后续其它节点打断 → `StructureError`。
- 撤回不存在的观测 → `UnknownObservationError`。
- 撤回带高阶导数的函数值、或撤回中间阶导数 → 默认拒绝，需 `cascade=True`。

## 运行

```bash
python3 demo.py
python3 -m unittest -v
```
