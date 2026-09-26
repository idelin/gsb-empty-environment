# bayes — 有限假设序贯贝叶斯更新器

Python 3.11，仅标准库。观测可迟到、更正、撤回；似然贡献保存在线段树中，
撤回/更正不做除法（零似然无法靠相除恢复）。归一化在 log 域完成；
总证据为零时进入冲突状态并抛 `ConflictError`，绝不均匀重置。

## 结构

- `bayes/updater.py` — `BayesianUpdater`：贡献树、log 域归一化、每步证据历史、
  重复观测 ID 幂等、分批与连续等价、分片合并（`merge`）、原子模型/先验版本更新
  （`update_model`，冲突时不提交）。
- `bayes/exact.py` — `fractions.Fraction` 精确有理数参照实现，用于小模型对照。
- `bayes/simulation.py` — 调用方注入随机源；经验估计记录 seed 与样本量，
  精确结论与经验估计分别标注（`Estimate.label` 为 `"exact"` / `"empirical"`）。
- `bayes/demo.py` — 运行样例：`python3.11 -m bayes.demo`
- `tests/` — unittest 套件：`python3.11 -m unittest discover -s tests -t . -v`

## 关键语义

- 观测似然字典可缺省假设，缺省视为中性（似然 1）。
- `apply` 重复 ID 返回 `None` 且不重复计入；修改用 `correct`，撤销用 `retract`。
- `apply_batch` 与逐条 `apply` 的后验完全一致。
- 冲突状态下 `posterior()` / `log_evidence` 抛 `ConflictError`；
  撤回矛盾证据后从贡献树恢复。
- `update_model(priors, version)` 要求版本严格递增；全部节点重算成功才提交，
  返回新的归一化 log 证据。

## 运行样例（`python3.11 -m bayes.demo`）

```
== sequential updates (log-domain normalization) ==
prior                                        h1=0.5000, h2=0.3000, h3=0.2000
apply obs-1                                  h1=0.7627, h2=0.2034, h3=0.0339
apply obs-2                                  h1=0.4592, h2=0.4898, h3=0.0510
correct obs-1 (late fix)                     h1=0.3000, h2=0.6000, h3=0.1000
retract obs-2                                h1=0.6122, h2=0.3061, h3=0.0816

== contradictory evidence -> conflict -> recovery ==
apply e1 (kills h2)                          h1=1.0000, h2=0.0000
apply e2 (kills h1)                          CONFLICT (zero evidence, no uniform reset)
retract e2 (recovered via tree)              h1=1.0000, h2=0.0000

== atomic model/prior version update ==
update_model v2 (log evidence=-0.8440)       h1=0.2791, h2=0.5814, h3=0.1395

== shard merge (no double counting) ==
merged (4 unique obs)                        h1=0.5505, h2=0.2569, h3=0.1927
exact rational check: {'h1': '60/109', 'h2': '28/109', 'h3': '21/109'}

== evidence history (one record per step) ==
  seq=0 init     model v1       logZ=+0.000000 conflict=False
  seq=1 apply    obs-1          logZ=-0.527633 conflict=False
  seq=2 apply    obs-2          logZ=-1.629641 conflict=False
  seq=3 correct  obs-1          logZ=-1.609438 conflict=False
  seq=4 retract  obs-2          logZ=-0.713350 conflict=False
  seq=5 model    v2             logZ=-0.843970 conflict=False

== exact vs empirical (caller-injected RNG) ==
[exact]     P(MAP=loaded | loaded) = 0.8059 (16117913/20000000)
[empirical] P(MAP=loaded | loaded) = 0.8028 (seed=20260927, samples=20000)
```

## 测试结果（`python3.11 -m unittest discover -s tests -t .`）

```
Ran 26 tests in 0.238s

OK
```

覆盖：log 域归一化与精确有理数对照、每步证据记录、零证据冲突且不均匀重置、
全零似然不重置、矛盾证据撤回后恢复、零似然不经除法恢复、重复 ID 不双更新、
迟到观测顺序不变性、分批与连续一致、分片合并不重复计入且拒绝冲突重复、
模型/先验版本原子更新（失败保持原状态）、版本必须递增、经验估计记录
seed/样本量并与精确解一致、同 seed 可复现。
