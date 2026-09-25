# Pure-Python Global Assembler

Python 3.11，仅标准库（不依赖 NumPy/SciPy）。通过显式全局自由度映射把局部单元矩阵/向量装配进全局系统，支持共享节点、受约束自由度的线性消元、重复贡献累加、逐项来源追踪、单元贡献的增量撤回与更正，以及整批原子拒绝。

## 文件

- `assembler.py` — 核心库：`Assembler`、`Constraint`、`Contribution`、`solve_dense`（自写稠密高斯消元，供小规模核验）
- `example.py` — 运行样例（4 节点弹簧链）
- `test_assembler.py` — unittest 套件（14 个用例）

## 设计要点

- **显式尺寸与索引**：`Assembler(n_dofs)`，每个单元显式给出 `dof_map`、`ke`、`fe`。
- **共享自由度**：多个单元对同一全局项的贡献直接累加；每项都记录来源（单元 id、局部下标、权重、数值、约束版本）。
- **增量更新**：`update_element` / `remove_element` 只遍历该单元 dof_map 覆盖的条目（`touch_count` 可验证，与单元总数无关），不遍历所有单元。
- **约束线性消元**：约束 `u[c] = Σ t_k u[k] + g` 通过 `u = T v + g` 消元，得到 `A_red = Tᵀ A T`、`b_red = Tᵀ (b − A g)`——非常数约束对右端的修正项（`−Tᵀ A g`）完整保留并计入来源。
- **约束版本**：`set_constraints` 使 `constraint_version` 递增；旧的约束装配被撤销，化简系统从未触碰的单元级装配惰性重建（不重新遍历单元）。化简后每项来源都带版本号。
- **原子批处理**：`apply_batch` 先整体校验再应用；任一映射无效（越界、重复、形状不符、非数值）则整批拒绝，状态不变。

## 运行

```sh
python3.11 example.py      # 运行样例
python3.11 -m unittest -v  # 运行测试
```

## 样例输出（python3.11 example.py）

```
global stiffness (shared dofs accumulate):
        1.0000   -1.0000    0.0000    0.0000
       -1.0000    2.0000   -1.0000    0.0000
        0.0000   -1.0000    2.0000   -1.0000
        0.0000    0.0000   -1.0000    1.0000
global load: [   0.5000,    1.0000,    1.0000,    0.5000]

provenance of shared entry A[1][1] (bar0 + bar1):
    element='bar0' local=(1, 1) value=+1.0
    element='bar1' local=(0, 0) value=+1.0

constraint version 1, free dofs [1, 2]
reduced stiffness A_red = T^T A T:
        2.0000   -1.0000
       -1.0000    2.0000
reduced load b_red = T^T (b - A g): [   1.0000,    1.5000]
rhs provenance of b_red[1] (note the constraint correction term):
    kind=element   source='bar1' value=+0.5000 v1
    kind=element   source='bar2' value=+0.5000 v1
    kind=constraint source=3 value=+0.5000 v1
solution u: [   0.0000,    1.1667,    1.3333,    0.5000]

-- update bar1 stiffness to 2.0 (incremental, touches only bar1) --
entries touched: 12 (independent of element count)
solution u: [   0.0000,    1.2000,    1.3000,    0.5000]

-- change constraint u3: 0.5 -> 1.0 (old assembly undone, rebuilt) --
constraint version now 2
solution u: [   0.0000,    1.4000,    1.6000,    1.0000]

-- withdraw bar0, then batch with a bad mapping is rejected whole --
batch rejected: dof 9 out of range [0, 4)
elements still present: ['bar1', 'bar2']
```

## 测试结果（python3.11 -m unittest）

```
Ran 14 tests in 0.003s

OK
```

覆盖：共享项相加、重复贡献累加、正负抵消后来源保留、增量更新/撤回与独立全量装配对照、更新不遍历所有单元（touch_count 恒定）、整批原子拒绝与各类无效映射、约束消元与独立稠密化简对照、右端修正项不遗漏、约束变更后版本递增并与全新装配一致、非法约束无副作用拒绝、自写稠密求解器。
