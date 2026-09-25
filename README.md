# markov — 稀疏 Markov 转移矩阵分析（纯标准库）

Python 3.11，仅使用标准库（`fractions`、`enum`、`dataclasses`、`math`），
不依赖 NumPy/SciPy。稠密 LU 分解为自写实现，用于小规模核验。

## 设计要点

- **显式尺寸与索引**：`SparseTransitionMatrix(n, lines)` 要求显式 `n` 和
  每个状态的显式概率行 `{source: {target: p}}`。
- **行/列约定统一**：`Convention.ROW`（行和为 1）与 `Convention.COLUMN`
  （列和为 1）在构造时统一为规范内部形式 `lines[source] = {target: p}`，
  后续算法与约定无关。
- **不静默修复**：缺概率行抛 `MissingLineError`；行和失配抛
  `LineSumError`（绝不静默归一化）；负概率抛
  `NegativeProbabilityError`；索引越界抛 `IndexOutOfRangeError`。
- **结构诊断**：Tarjan SCC 分解通信类，识别闭类、吸收态（单点闭类）、
  暂态，给出不可约性诊断（`chain.diagnostics()`）。
- **质量守恒**：`propagate` 每步输出 `mass_error = sum(d) - 1`，吸收态
  质量只累积不流失；分布校验要求非负且和为 1（容差内）。
- **吸收分析**：对每个闭类求解 `(I - Q) B = R` 得吸收概率，求解
  `(I - Q) t = 1` 得期望吸收步数。若可达非吸收闭类，报告相应闭类概率
  且期望步数为 `math.inf`。
- **精确有理参考**：`method="exact"` 用 `fractions.Fraction` 做精确 LU，
  作为小模型参考解。
- **迭代大模型**：`method="iterative"` 用稀疏 SOR，收敛只以真实残差
  `max |b + Qx - x| <= tol` 判定；增量小**不**作为收敛证明
  （`assess_convergence` 会对"增量小但残差大"给出警告）。不收敛抛
  `NotConvergedError` 并报告残差。
- **行修订重用分解**：`chain.revise_line(i, new_line)` 校验新行后，若
  暂态集合不变，把行修订推入缓存的 `LUSolver`，下次求解先用旧分解做
  迭代精化（残差下降则重用，否则自动重新分解），诊断中可见
  `reused_factorization` 标志。

## 文件结构

- `markov/sparse.py` — 稀疏转移矩阵、约定、校验
- `markov/classify.py` — Tarjan SCC、闭类/吸收态/不可约性
- `markov/dense.py` — 自写稠密 LU（float 与 Fraction 通用）
- `markov/solvers.py` — SOR 迭代求解、LU 行修订重用、收敛判定
- `markov/chain.py` — `MarkovChain`：传播、平稳迭代、吸收分析、行修订
- `tests/test_markov.py` — 29 个 unittest 用例
- `examples/demo.py` — 7 个场景的端到端样例

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 测试
python3.11 examples/demo.py                    # 样例
```

## 样例输出（节选）

```
1. Small chain: gambler's ruin (dense vs exact rational reference)
  state 1: P(absorbed at 4) dense=0.250000 exact=1/4  E[steps] dense=3.000000 exact=3
  state 2: P(absorbed at 4) dense=0.500000 exact=1/2  E[steps] dense=4.000000 exact=4

2. Non-absorbing closed class reachable -> infinite expectation
P(end in each closed class | start 4): {0: 0.5, 1: 0.0, 2: 0.5}
E[steps to absorption | start 4]: inf

3. Mass conservation while propagating a distribution
  step  100: mass error=-2.22e-16  mass at absorbing states=0.999999
  step  400: mass error=-3.33e-16  mass at absorbing states=1.000000

4. Large sparse model (n=300): iterative SOR with true residuals
  P(hit 300 | start 150) = 0.50000000 (closed form 0.5)
  E[steps | start 150] = 22500.00 (closed form 22500)
  rhs expected_steps: iterations=1398 residual=9.82e-11 ... converged=True

5. Row revision with factorization reuse
  before revision: reused_factorization = [False, False, False]
  after revision:  reused_factorization = [True, True, True]

7. Stationary iteration: residual, not increment, proves convergence
  converged=True after 136 iterations; residual=8.91e-13
```

## 测试结果

```
Ran 29 tests in 1.906s
OK
```
