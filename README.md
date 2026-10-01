# 分层与两阶段成组抽样估计库

Python 3.11,仅标准库,测试用 unittest。

## 设计原则

- **层权规范**:层权 `W_h = N_h / N`,权重之和恒为 1;大小不同的层按总体量加权,绝不简单平均层均值。
- **缺层拒绝**:`N_h > 0` 但 `n_h = 0` 抛 `MissingStratumError`;零/越界纳入概率抛 `ZeroInclusionProbabilityError`。
- **不伪造方差**:单样本层(`n_h=1 < N_h`)与单群层(`m_h=1 < M_h)均值照算,方差标记为不可估(`variance=None`)并附诊断。
- **全查层贡献 0**:`n_h == N_h`(或 `m_h == M_h`)时有限总体修正因子为 0,方差贡献为 0。
- **组内相关不独立化**:两阶段设计以"群"为最终分析单元(ultimate cluster),方差只在群层聚合,单元数不进入自由度。
- **随机源注入**:`random.Random` 由调用方构造并传入;`SamplingRecord` 记录 seed、方法、各层/各群样本量。
- **精确 vs 经验**:全设计枚举(`Fraction` 精确运算)给出的结论标记"精确";模拟给出的标记"经验"。

## 估计量

分层(`stratified.py`):

```
mean = Σ_h W_h · ȳ_h
var  = Σ_h W_h² · (1 - n_h/N_h) · s_h² / n_h
```

两阶段分层成组(`two_stage.py`),群 i 纳入概率 `π_i`、单元条件纳入概率 `π_{j|i}`:

```
z_i  = (1/π_i) · Σ_j y_ij / π_{j|i}        # 加权群总量(HT)
t_h  = Σ_{i∈s_h} z_i ;  mean = Σ_h t_h / N
var(t_h) = m_h · (1 - m_h/M_h) · s_z²       # z_i 已含权重,勿再乘 M_h²/m_h
```

## 增量撤回

`IncrementalStratifiedEstimator` 每层只维护 `(count, sum, sumsq)` 充分统计量;
样本身份变更时 `retract()` 精确撤回旧成员贡献、`add()` 追加新成员,
`estimate()` 与批量重算结果一致。

## 运行

```bash
python3.11 examples/run_examples.py   # 运行样例
python3.11 -m unittest discover -s tests -v   # 31 个测试
```

## 文件

- `stratified_sampling/stratified.py` — 分层均值/方差估计与诊断
- `stratified_sampling/two_stage.py` — 两阶段分层成组估计与可估性诊断
- `stratified_sampling/incremental.py` — 增量撤回统计
- `stratified_sampling/sampling.py` — 注入随机源的抽样与记录
- `stratified_sampling/enumeration.py` — 全设计枚举精确核验
- `tests/` — 31 个 unittest;`examples/run_examples.py` — 运行样例
