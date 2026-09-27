# importance_sampling

Python 3.11，仅标准库。输入目标分布密度与提议分布密度，计算普通（未归一化）
重要性采样估计、自归一化估计与有效样本量（ESS），权重全程在对数域稳定化，
并对缺覆盖、无限权重、低 ESS 给出明确诊断。支持多提议混合与自适应提议，
每个样本的权重分母始终使用**生成该样本时的真实提议密度**。

## 布局

- `importance_sampling/distributions.py` — `DiscreteDistribution`（有限离散域 pmf）、`MixtureProposal`（混合提议）
- `importance_sampling/estimators.py` — 普通/自归一化估计、ESS、覆盖与权重诊断、`estimate()` 主入口
- `importance_sampling/adaptive.py` — `AdaptiveIS` 自适应多轮提议 + JSON checkpoint 保存/恢复
- `importance_sampling/exact.py` — 有限离散域上的精确期望参考值
- `examples/run_example.py` — 运行样例
- `tests/` — unittest 测试（29 个）

## 估计量与性质（精确结论）

- 普通 IS：`μ̂ = (1/n) Σ wᵢ f(xᵢ)`，`wᵢ = p(xᵢ)/q(xᵢ)`。**无偏**（前提是提议覆盖目标，
  即 `p(x)>0 ⇒ q(x)>0`），要求目标密度已归一化。
- 自归一化 IS（SNIS）：`μ̃ = Σ wᵢ f(xᵢ) / Σ wᵢ`。对任意有限 n 一般**有偏**
  （偏差量级 O(1/n)），但 n→∞ 时一致（收敛到真值），且均方误差常低于普通估计；
  目标密度无需归一化。测试中用枚举所有样本的方式精确验证了这两个偏差性质的区别。
- ESS = `(Σw)² / Σw²`：等权时 ESS 恰好等于样本数 n；单一权重主导时 ESS≈1。
- 对数稳定化：权重以 `log w = log p − log q` 计算，指数化前先做 max-shift，
  log 权重相差数百乃至上千也不会上溢/下溢。

## 诊断

- `ZERO_COVERAGE`（error）：提议概率为 0 而目标为正 → 估计**不可用**（`available=False`，估计值为 `None`）。
- `INFINITE_WEIGHT` / `NAN_WEIGHT` / `ALL_WEIGHTS_ZERO`（error）：权重数值异常 → 不可用。
- `LOW_ESS`（warning）：ESS 低于 n 的 10%（可配置）→ 估计可用但需警惕。

## 混合与自适应提议

- `MixtureProposal`：样本由混合密度 `Σ αₖ qₖ(x)` 生成，权重分母自动使用该混合密度
  （而非某个分量的密度）。
- `AdaptiveIS`：每轮可更换提议，每个样本的 log 权重在抽取时对**当轮**提议计算一次并冻结存储。
  各轮提议均覆盖目标时，混合所有样本的普通 IS 估计仍无偏（逐项无偏）。
- checkpoint：JSON 保存样本、冻结权重、seed 与样本量；恢复后旧权重**逐字复用**，
  绝不用新提议密度重算。

## 随机源

所有采样通过调用方注入的 `random.Random` 实例进行；结果对象记录调用方传入的
`seed` 标签与样本量 `n`，相同 seed 完全可复现。

## 运行

```bash
python3.11 -m unittest discover -s tests -t . -v   # 测试
python3.11 examples/run_example.py                 # 样例
```

## 样例输出（实际运行）

```
=== EXACT (closed-form reference over the finite domain) ===
    E_target[f] = 0.7

=== EMPIRICAL (single run, seed=2024, n=2000) ===
    ordinary IS estimate        : 0.707300
    self-normalized IS estimate : 0.719057
    ESS = 1224.5 of n = 2000 (seed=2024)
    diagnostics: none

=== EMPIRICAL (bias comparison, 2000 replications, n=25 each) ===
    mean ordinary estimate        = 0.698724  (unbiased: matches EXACT 0.7)
    mean self-normalized estimate = 0.723428  (finite-n bias, O(1/n); consistent as n grows)

=== EMPIRICAL (mixture proposal, seed=7, n=2000) ===
    ordinary=0.686151  self-normalized=0.689077  ESS=1430.0

=== EMPIRICAL (adaptive proposals + checkpoint resume) ===
    checkpoint saved after iteration 0 (n=500, seed=555)
    after resume with new proposal: n=1000, ordinary=0.706750, self-normalized=0.640623, ESS=218.0

=== DIAGNOSTIC (zero coverage -> unavailable) ===
    available=False, ordinary=None, self_normalized=None
    diagnostics: [error] ZERO_COVERAGE: proposal assigns zero probability where the target is positive at [1, 2]; ...

=== DIAGNOSTIC (low ESS warning) ===
    available=True, ESS=12.4 of n=400
    diagnostics: [warning] LOW_ESS: effective sample size 12.4 is below 10% of n=400; ...
```

## 测试结果（实际运行）

```
Ran 29 tests in 0.046s
OK
```

覆盖：精确枚举验证普通 IS 无偏、SNIS 有限样本偏差存在且随 n 缩小；等权 ESS == n；
对数稳定化不溢出；零覆盖/无限权重/低 ESS 诊断；混合密度作为权重分母；自适应逐轮
提议分母；checkpoint 恢复不重算旧权重；seed 与样本量记录；经验收敛（均标注为
EMPIRICAL，与 EXACT 结论区分）。
