# Certified-Envelope Rejection Sampling

Python 3.11，仅标准库 + `unittest`。实现带显式包络常数、分区包络、
预算与取消、可恢复随机源的拒绝采样器。

## 设计要点

- **显式包络常数**：每次尝试都检查 `target(x)/proposal(x) ≤ 包络界`；
  违界立即抛 `BoundViolationError`，**绝不把比率裁到 1**，违界后不再
  输出任何声称无偏的样本。
- **分区包络与自适应细分**：`Envelope` 按 `Partition` 设置上界，
  `Envelope.refine()` 支持自适应细分。每个分区的上界必须满足二者之一：
  调用方证书（`Certificate`）或精确有限域验证（`verify_envelope_exactly`
  / refine 时传入 `target/proposal/domain`）。无证书且未验证的分区
  在使用时抛 `UncertifiedBoundError`。
- **无偏性**：分区界用于违界检查；接受概率使用全局包络常数
  （各分区界最大值），保证输出分布严格正比于目标。细分收紧所有相关
  分区后可降低全局常数、提升接受率。
- **预算与取消**：`max_attempts` 预算耗尽返回 `BUDGET_EXHAUSTED`，
  `CancellationToken` 取消返回 `CANCELLED`；两者都只返回已完成样本，
  并给出 `remaining`（未完成数）。
- **可恢复性**：随机源由调用方注入（`rng` 或 `seed`），结果记录
  `seed`、样本量与每条已确认样本的 `(value, attempt_index, draws_used)`；
  `recover_confirmed_samples()` 用同一 seed 重放即可恢复已确认样本位置，
  不一致时抛 `RecoveryMismatchError`。
- **统计口径**：接受率分母为**尝试数**（`accepted / attempts`）。
- **精确 vs 经验**：`ExactAnalysis`（`kind="exact"`，枚举有限域得到
  接受概率、期望拒绝次数、精确输出分布）与 `EmpiricalReport`
  （`kind="empirical"`，模拟估计）始终分别标注。

## 文件

- `rejection_sampling.py` — 库
- `test_rejection_sampling.py` — 31 个 unittest 用例
- `demo.py` — 运行样例

## 运行

```bash
python3.11 demo.py
python3.11 -m unittest -v
```

## 最小用法

```python
from rejection_sampling import (
    Certificate, Envelope, Partition, RejectionSampler, UniformFiniteProposal,
)

domain = list(range(10))
weights = [1, 2, 3, 4, 5, 4, 3, 2, 1, 0.5]
target = lambda x: weights[x]
proposal = UniformFiniteProposal(domain)
envelope = Envelope([
    Partition("all", lambda x: True, 50.0,
              certificate=Certificate("max ratio is 50")),
])
result = RejectionSampler(target, proposal, envelope, seed=42).sample(
    100, max_attempts=1000)
print(result.status, result.accepted, result.acceptance_rate)
```

## 运行样例（`python3.11 demo.py`，节选）

```
[exact] envelope verification: {'low': True, 'high': True}, all_ok=True
[exact]     acceptance probability : 0.510000
[exact]     expected rejections/sample: 0.9608
[empirical] seed=20260928, samples=5000, attempts=9872, rejections=4872
[empirical] acceptance rate (accepted/attempts): 0.506483
[compare]   total variation exact-vs-empirical: 0.0168
records: [(3, 1), (6, 2), (5, 3), (0, 6), (4, 7), (1, 8)]
recovered values: [3, 6, 5, 0, 4, 1]
budget: status=budget_exhausted, sampled=5, remaining=995, attempts=12
cancel: status=cancelled, sampled=0, remaining=1000
BoundViolationError: target/proposal ratio 50.0 exceeds envelope bound 20.0 at 4; aborting instead of clamping
[exact] acceptance before refine: 0.318750
[exact] acceptance after  refine: 0.510000
```

## 测试结果（`python3.11 -m unittest`）

```
Ran 31 tests in 0.916s

OK
```

覆盖：完成/预算/取消三种状态、违界即报错（不裁剪）、无证书分区拒绝、
精确验证通过与失败、自适应细分（证书/精确验证/缺信任材料/留空域）、
接受率分母为尝试数、seed 重放与已确认样本位置恢复、篡改记录检测、
精确分布与经验分布对照（总变差 < 0.05）、精确与经验标签分离。
