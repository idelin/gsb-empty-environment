# affine-dag: 线性/受限分段函数的误差传播 DAG（仿射算术）

纯 Python 3.11 标准库实现（不依赖 NumPy/SciPy），测试使用 `unittest`。

## 设计

- **变量携带区间与共享噪声符号**（`affine_dag/core.py`）：仿射形式
  `x = center + Σ terms[sid]·ε_sid`，所有 `ε_sid` 的半径存放在共享的
  `NoiseSymbols` 注册表中。同源引用共享符号 ⇒ `x - x = 0` 精确成立；
  独立变量使用不同符号 ⇒ 区间相同也不会抵消。
- **线性精确、非线性保守余项**：加/减/数乘精确；乘法与倒数采用公开的
  min-range（Chebyshev）仿射逼近与保守余项界（Stolfi & de Figueiredo,
  1997；de Figueiredo & Stolfi, 2004），余项放入新的噪声符号。
- **受限分段函数**（`affine_dag/piecewise.py`）：连续分段线性函数由显式
  断点与取值定义。输入区间不含内部拐点时精确线性传播（保留相关性）；
  跨拐点时扫描端点**和所有内部拐点**取精确值域，再用保守包络
  （中心 + 新噪声符号）表示——绝不止看端点。
- **DAG 与增量变更**（`affine_dag/dag.py`）：节点按拓扑序创建、显式整数
  索引。`set_symbol_radius` 只把受影响的下游节点标脏并重算，其余节点
  保留缓存形式。
- **预算截断 ⇒ 未完成证书**：`DAG(max_terms=k)` 限制每个仿射形式的噪声
  符号数；超限时结果与证书标记为 `complete=False`，验证器明确拒绝。
- **独立证书验证器**（`affine_dag/certificate.py`）：证书为 JSON 可序列化
  快照。验证器仅从序列化规范重推导：重算每个仿射形式并精确比对、核对
  区间与符号注册表、检查预算、拒绝未完成证书，并在变量域的确定性网格上
  做真实函数采样交叉校验。
- **显式尺寸与索引**（`affine_dag/dense.py`）：自写稠密 `matvec/matmul`，
  形状为显式入参并校验，用于小规模交叉核验。

## 运行测试

```sh
python3.11 -m unittest discover -s tests -v
```

实际运行结果见 `RESULTS.md`。

## 覆盖的关键情形

| 情形 | 测试 |
| --- | --- |
| `x - x` 精确为 0 | `tests/test_affine.py` |
| 独立 `x, y` 区间相同不抵消 | `tests/test_affine.py` |
| 分段函数跨拐点拆分/包络，非仅端点 | `tests/test_piecewise.py` |
| 改一个噪声界只更新受影响后继 | `tests/test_incremental.py` |
| 小域枚举全链包含性 | `tests/test_enumeration.py` |
| 坏证书被拒绝 | `tests/test_certificate.py` |
| 除数跨 0 明确报错 | `tests/test_divzero.py` |
| 符号额度超限 ⇒ 未完成证书 | `tests/test_budget.py` |
