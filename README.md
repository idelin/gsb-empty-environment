# stratified_sampling

分层抽样与两阶段分层成组抽样的设计无偏估计库。Python 3.11，仅使用标准库，
测试基于 `unittest`。

## 设计原则

- **层权重规范为 1**：`W_h = N_h / N` 归一化，大小不同的层绝不简单平均层均值。
- **缺层与零纳入概率明确拒绝**：`MissingStratumError`、
  `ZeroInclusionProbabilityError`，绝不静默插补。
- **方差可估性诚实报告**：单样本层（`n_h = 1, N_h > 1`）与单群层
  （`m_h = 1, M_h > 1`）的方差贡献记为不可估（`None`），不假装有精确方差；
  全量抽样层经有限总体修正贡献恰为 0。
- **组内相关不当独立**：两阶段估计以群总量为单位，组内相关由群间方差分量
  承担；纳入概率与两级有限总体修正均进入方差公式。
- **随机源由调用方注入**：抽样函数接收 `random.Random`，`SamplingRecord`
  记录 seed 与各阶段样本量。
- **精确与经验分别标注**：有限总体枚举给出 `kind="exact"` 的结论，
  蒙特卡洛复制给出 `kind="empirical"` 的估计，二者不混用。
- **增量撤回**：`IncrementalStatistics` 支持 `add` / `retract`，样本身份
  变更时增量撤回统计贡献，无需全量重算。

## 估计公式

一阶段分层（层内 SRSWOR）：

```
mean = sum_h W_h * ybar_h
var  = sum_h W_h^2 * (1 - n_h/N_h) * s_h^2 / n_h
```

两阶段分层成组（两级 SRSWOR，Horvitz-Thompson 展开）：

```
t_hat_h = (M_h/m_h) * sum_i N_hi * ybar_hi
var_h   = M_h^2 (1 - m_h/M_h) s_t^2 / m_h
        + (M_h/m_h) * sum_i N_hi^2 (1 - n_hi/N_hi) s_hi^2 / n_hi
```

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 测试
python3.11 examples/run_examples.py           # 样例
```

## 测试结果（最近一次运行）

```
Ran 34 tests in 1.9s
OK
```

覆盖：不等层加权、权重归一、全量层零贡献、单样本层不可估、缺层/零纳入
概率拒绝、两阶段分量手算核对、组内相关不当独立、增量撤回一致性、
枚举精确无偏性与方差公式核验（分层 30 个设计、两阶段 243 个设计）、
经验复制与精确结果一致、seed 可复现。

## 目录结构

```
stratified_sampling/
  errors.py       异常类型
  incremental.py  增量 add/retract 统计
  stratified.py   一阶段分层均值与方差估计
  cluster.py      两阶段分层成组估计与可估性诊断
  sampling.py     注入式随机抽样与 SamplingRecord
  enumeration.py  有限总体枚举（exact）与蒙特卡洛（empirical）核验
examples/run_examples.py
tests/
```
