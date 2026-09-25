# markov — 稀疏 Markov 链工具包（纯标准库）

Python 3.11+，仅使用标准库（`fractions`、`dataclasses`、`unittest`），
不依赖 NumPy/SciPy。尺寸与索引全部显式输入，小规模用自写稠密实现核验。

## 设计要点

- **稀疏转移矩阵** `SparseTransitionMatrix(size, convention)`：
  `convention="row"`（行随机，P(i→j) 存于 (i,j)）或 `"column"`
  （列随机，P(j→i) 存于 (i,j)），约定统一封装在 `outgoing()` 之后。
- **校验纪律**：行/列和偏离 1 超过容差即报 `ValidationError`，
  绝不静默归一化；缺概率行报错而非自动补齐；负值、越界索引同理。
- **闭类识别**：迭代版 Tarjan SCC，无出边的 SCC 即闭类；
  单点闭类即吸收态；`is_irreducible()` 给出不可约性诊断。
- **吸收分析** `absorption_analysis(method=...)`：
  - `"exact"`：`fractions.Fraction` 精确有理解（小模型参考）；
  - `"iterative"`：Gauss-Seidel，逐系统报告残差与可靠误差界
    （严格对角占优时 `‖x−x*‖∞ ≤ ‖r‖∞/d`，否则只信残差）；
  - `"lu"`：自写稠密 LU。
  - 非吸收闭类可达时，报告相应概率并将期望步数记为 `inf`。
- **收敛判据**：只认残差，不把"变化很小"当收敛证明
  （`assess_convergence` 在增量小但残差大时给出警告）。
- **质量守恒**：`propagate()` 每步输出质量守恒误差；
  分布非负、和保持 1（容差内），漂移越界即抛错。
- **行修订重用分解**：`chain.revised({row: {...}})` 重新全量校验后，
  `revised.transient_solver(base=old_solver)` 通过 Woodbury 秩-k 更新
  复用旧 LU 分解；瞬态集改变时自动回退重新分解。

## 运行

```bash
python3 examples/run_examples.py     # 运行样例
python3 -m unittest discover -s tests -v   # 测试（29 个用例）
```

## 样例输出（节选）

```text
1. Exact rational reference: gambler's ruin on states 0..4
  start 1: P(ruin at 0)=3/4  P(reach 4)=1/4  E[steps]=3
  start 2: P(ruin at 0)=1/2  P(reach 4)=1/2  E[steps]=4

2. Iterative analysis of a 41-state chain, with residuals
start 20: P(absorb at 0)=0.4999999677  E[steps]=399.9999999679 (exact 400)
  system expected_steps   iters= 3770  final residual=9.942e-11
  step 400: absorbed mass=0.630454  mass error=+0.00e+00

3. Row revision with factorization reuse (Woodbury)
reuses base factorization: True
E[steps] after revising row 1 -> {0: 3/4, 2: 1/4}: [1.8, 3.2, 2.6]  (exact 9/5, 16/5, 13/5)
row-sum mismatch correctly rejected: line 1 sums to 0.9, not 1 ...; refusing to normalize silently

4. Reachable non-absorbing closed class -> infinite expectation
  state 2: P per class={0: 1/2, 1: 1/2}  E[steps]=inf
```

## 测试结果

```text
Ran 29 tests in 0.010s
OK
```

## 文件结构

- `markov/sparse.py` — 稀疏转移矩阵、显式尺寸/索引、校验（不静默归一化）
- `markov/dense.py` — 自写稠密 LU（Woodbury 行修订重用）、精确有理解
- `markov/iterative.py` — Gauss-Seidel / 幂迭代，残差判收敛、误差界
- `markov/chain.py` — 闭类识别、吸收概率、期望步数、传播质量守恒
- `tests/test_markov.py` — 29 个 unittest 用例
- `examples/run_examples.py` — 5 个运行样例
