# dp-audit：可注入机制的经验审计（仅合成数据）

对**可注入机制**在一对**合成邻接数据集**上的输出分布做统计实验：
估计事件概率比 `r = P[M(D1) ∈ S] / P[M(D0) ∈ S]`，给出抽样置信区间，
并在发现明显违规时给出**可复核事件**（事件定义 + seed + 试验次数）。

> **免责声明**：本仓库只做统计实验。经验审计可以**发现**对差分隐私的
> 违反，但**永远不能证明**一个机制满足差分隐私。`consistent_not_proof`
> 结论只是有限样本下的统计证据，不构成任何形式化隐私证明。
> 仓库中所有数据均为合成数据，不处理任何真实数据。

## 定义

- **邻接数据集**：仅相差一条记录的两个数据集。这里每个数据集是单个比特，
  故 `D0 = 0`、`D1 = 1`。
- **事件 S**：输出空间的可测子集，以谓词 `event(output) -> bool` 加文字
  描述给出。
- **概率比**：`r = P[M(D1) ∈ S] / P[M(D0) ∈ S]`。若机制满足 ε-DP，则对
  任意事件都有 `r ≤ e^ε` 且 `1/r ≤ e^ε`。
- **判定**：
  - `violation_detected`：置信区间下界 > `e^ε`，在水平 α 下存在统计违规证据；
  - `consistent_not_proof`：置信区间上界 ≤ `e^ε`，未检测到违规（**不是证明**）；
  - `inconclusive`：区间跨过 `e^ε`，需要增大试验次数。

## 统计方法

- 两个事件概率各用 **Wilson 得分区间**，置信水平 `1 - α/2`；比值区间取
  `(lo1/hi2, hi1/lo2)`，由 Bonferroni 不等式保证联合覆盖率 ≥ `1 - α`。
- **零观测不等于真实概率 0**：`k = 0 / n` 时使用单侧 Clopper-Pearson 上界
  `1 - (α/4)^(1/n)`（尾部分配与双侧 Wilson 一致）；全部命中时对称处理。
- 每次实验完整记录 `seed`、`trials_per_dataset`、`alpha`、事件定义与命中数，
  相同 seed 可逐位复现结果。

## 参数

| 参数 | 含义 | 默认值（演示脚本） |
| --- | --- | --- |
| `eps_claimed` | 机制声称满足的隐私预算 ε | 1.0 |
| `trials_per_dataset` | 每个邻接数据集的独立调用次数 | 100000 |
| `alpha` | 比值置信区间的族误差水平 | 0.05 |
| `seed` | `random.Random` 种子 | 20260928 |
| `event` | 被审计的输出事件 S | `{output == 1}` |

## 文件结构

- `dp_audit/mechanisms.py`：被审机制（`randomized_response` 为 ε-DP；
  `deterministic_identity` 为明显违规的反例）。
- `dp_audit/audit.py`：统计核心（Wilson/Clopper-Pearson 区间、比值区间、判定）。
- `run_audit.py`：演示脚本。
- `tests/test_audit.py`：unittest 测试。

## 运行

```bash
python3.11 run_audit.py
python3.11 -m unittest discover -s tests -v
```

## 运行样例（实际输出，seed=20260928）

```text
========================================================================
Empirical DP audit (synthetic data only; NOT a formal privacy proof)
========================================================================

[1] 0.5-DP mechanism audited against claim eps=1.0
mechanism          : rr_half
claimed eps        : 1.0
event S            : S = {output == 1}
trials per dataset : 100000
seed               : 20260928
alpha              : 0.05
D0=0: hits 37734/100000  p_hat=0.377340  CI=[0.373911, 0.380782]
D1=1: hits 61999/100000  p_hat=0.619990  CI=[0.616544, 0.623424]
ratio p1/p0        : hat=1.643054  CI=[1.619152, 1.667309]  (e^eps=2.718282)
verdict            : consistent_not_proof
note               : empirical consistency is statistical evidence only and does NOT prove differential privacy

[2] broken mechanism: deterministic_identity, claims eps=1.0
mechanism          : deterministic_identity
claimed eps        : 1.0
event S            : S = {output == 1}
trials per dataset : 100000
seed               : 20260928
alpha              : 0.05
D0=0: hits 0/100000  p_hat=0.000000  CI=[0.000000, 0.000044]
D1=1: hits 100000/100000  p_hat=1.000000  CI=[0.999956, 1.000000]
ratio p1/p0        : hat=inf  CI=[22819.991146, inf]  (e^eps=2.718282)
verdict            : violation_detected
note               : zero observations on one side: upper bound is the one-sided Clopper-Pearson limit, NOT a true probability of 0
note               : reproducible violation: event 'S = {output == 1}' with seed=20260928, trials=100000
```

注意样例 [2]：D0 侧 10 万次试验零观测，但上界为 `4.4e-5` 而非 0——
少量样本的零观测不能当作真实概率 0。

## 测试结果（实际输出）

```text
test_broken_mechanism_violation_detected_and_reproducible ... ok
test_dp_mechanism_consistent_not_proof ... ok
test_invalid_arguments_raise ... ok
test_result_records_seed_and_trials ... ok
test_same_seed_reproduces_identical_result ... ok
test_all_successes_symmetric ... ok
test_interval_contains_point_estimate ... ok
test_interval_shrinks_with_more_trials ... ok
test_invalid_arguments_raise ... ok
test_zero_successes_not_treated_as_zero_probability ... ok

----------------------------------------------------------------------
Ran 10 tests in 0.036s

OK
```
