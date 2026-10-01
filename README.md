# Exact Weighted Sampler (Python 3.11, stdlib only)

精确有理数权重的加权随机抽样：Fenwick 树维护累计权重，支持更新、
无放回连续抽取、事务回滚与快照版本。仅依赖标准库（`fractions`、`random`）。

## 文件

- `weighted_sampler.py` — `WeightedSampler`（Fenwick 树 + Fraction 权重）、`Transaction`、`ZeroTotalWeightError`
- `random_sources.py` — 可注入随机源：`SeededRandomSource` / `BitStreamSource` / `ScriptedSource`，拒绝采样 `rejection_randbelow`
- `test_weighted_sampler.py` — unittest 套件（21 个测试）
- `demo.py` — 运行样例

## 概率模型（精确）

- 有放回：`P(i) = w_i / T`，`T = Σ w_i`。
- 无放回（顺序条件定义）：有序批次 `(i_1..i_k)` 按“每步从当前剩余权重中按比例
  抽取并移除”产生，故
  `P(i_1..i_k) = Π_t w_{i_t} / (T − Σ_{s<t} w_{i_s})`；
  无序集合的概率为其全部排列之和。

## 精确性论证（不用频率近似）

1. 令 `D` 为权重分母的最小公倍数，`N = T·D`（整数）。抽取 `[0, N)` 上均匀
   整数 `r`，`r` 落入半开区间 `[cum_i, cum_i + w_i·D)` 时选中 `i`。
2. 均匀整数由**拒绝采样**产生（取 `k = n.bit_length()` 位，≥ n 则重抽），
   逐值概率为几何级数 `Σ_t (1−n/2^k)^t · 2^{−k} = 1/n`，精确均匀，无取模偏差。
3. 因此 `P(i) = w_i·D / N = w_i / T` 精确成立；权重 0 对应空区间，永不被选；
   总权重为 0 时 `draw()` 抛出 `ZeroTotalWeightError`（明确失败）。
4. 测试通过**全枚举**验证：枚举全部 `2^(kR)` 个比特串（每个值恰出现
   `(2^{kR} − m^R)/n` 次，`m^R` 个串未终止），以及枚举全部 `r ∈ [0, N)`
   对照朴素累积数组——均为精确整数等式，非频率接近。

## 事务与消耗规则

- `tx = sampler.begin()`：快照权重与随机源状态。
- `tx.commit()`：已抽中项权重保持为 0（权重被消耗），随机数已消耗。
- `tx.rollback()`：权重**与**随机源状态同时恢复——事务内消耗的随机数
  视为未消耗（计数器与内部状态均还原），后续抽样与从未开启事务完全一致。
- 事务开启期间请勿调用 `add()`（回滚只恢复 begin 时存在的下标）。

## 并发：快照版本

每次修改使 `version` 单调递增；`snapshot()` 冻结当前权重为独立副本，
后续对 live sampler 的更新不影响已有快照。

## 随机源注入与记录

随机源必须由调用方注入。`SeededRandomSource(seed)` 记录 `seed`、
`values_returned`（样本量）与 `bits_consumed`；固定 seed 完全可复现，
`last_draw_info` 记录 `(r, D, N, index)` 供手工复算。

## 结论标注约定

- `[exact]`：由构造保证的精确有理数结论（概率、区间、枚举计数）。
- `[empirical]`：有限次运行的经验估计，仅作示意，不构成无偏性论证。

## 运行

```sh
python3.11 -m unittest discover -v   # 21 个测试
python3.11 demo.py                   # 运行样例
```
