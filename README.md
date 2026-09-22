# 可回溯修订的滚动漂移检测

这是一个无第三方依赖的 Python 3.11 示例组件。它解决的问题是：异常段不是在样本到达时立即知道，而是可能稍后补录；补录后，已经发布的滚动基线和漂移判定必须沿真实依赖回溯纠正，并保留修订链。

## 语义

- 样本键：`(source, seq)`；同 `source` 内 `seq` 严格递增，事件时间非递减。
- 时间窗：判定 `(source, t)` 时，基线取同源且事件时间落在 `[t-lookback, t)` 的样本；目标样本自身不进入自身基线。
- 异常段：同一 `source` 下的闭区间 `[start_time, end_time]`，由 `segment_id` 唯一标识。
- 有效异常段：已补录但未撤回。样本被任意有效段覆盖时不判定、不进入后续基线。
- 多段重叠：取覆盖该样本的有效段 ID 集合；撤回其中一个不会误撤回其他段。
- 重复标记：同一 `segment_id` 且完全相同的段是幂等操作；同 ID 不同区间报错。
- 撤回：撤回是墓碑，不删除历史；撤回后值重新纳入。撤回同一段重复执行也保持幂等。

## 判据

默认使用公开、确定性的严格总体 3σ 判据：

```text
n >= min_baseline
delta = x - mean
delta^2 > 3^2 * population_variance
```

- 均值：`mean = Σx / n`
- 总体方差：`population_variance = Σ(x - mean)^2 / n`
- 边界相等不是漂移；只有严格大于才漂移。
- 全部计算使用 `fractions.Fraction`。输入接受 `int`、有限十进制字符串、`decimal.Decimal` 或 `Fraction`；拒绝 `float`、`NaN` 和无穷。
- 实现中比较 `delta²` 与 `threshold² × variance`，不开平方，因此临界值不会由浮点偶然舍入决定。
- 有效基线为空时返回 `UNDECIDABLE`；这包括窗口内原本有值、但后来全部被异常段排除的情况。
- 基线非空但少于 `min_baseline` 时返回 `INSUFFICIENT`。

## 依赖传播

段 `[a, b]` 的补录或撤回只会改变以下现有同源结果：

```text
事件时间位于 [a, b + lookback]
```

原因：

- `[a, b]` 内的目标样本可能直接变成 `EXCLUDED` 或恢复判定。
- `[a, b + lookback]` 内目标样本的基线窗口可能包含该段。
- 晚于 `b + lookback` 的结果在该时间窗下不可能依赖该段，因此不重算。

每个结果都有逐键版本号。只有实质字段变化才生成新版本；状态未变但基线组成或统计量变了，也会作为修订保留下来。

## 快速运行

```bash
python -m examples.local_demo
python -m unittest discover -s tests -v
```

当前环境可能使用较新的 Python 解释器；代码只使用 Python 3.11 标准库语法和 API。

## 本地样例覆盖了什么

`examples/local_demo.py` 逐步演示：

1. 极端值 `1,000,000` 先作为普通样本进入后续滚动基线。
2. 事后补录异常段，输出每个受影响结果的旧版本/新版本、状态、均值、方差、纳入/排除键集合。
3. 将补录后的状态与“该段从一开始就存在”的参照检测器比较，证明当前结果等价于极端值从未纳入。
4. 撤回异常段，值重新纳入并再次修订后续结果。
5. 每一步都把增量修订状态与全量重放比较，最终比较完整修订链。

## API 简要示例

```python
from fractions import Fraction
from drift_revision import DriftConfig, DriftDetector

detector = DriftDetector(DriftConfig(lookback=5, min_baseline=2, threshold=Fraction(3)))
detector.add_sample("sensor-a", 0, 0, 10)
detector.add_sample("sensor-a", 1, 1, 10)
detector.add_sample("sensor-a", 2, 2, 1_000_000)

# 事后补录，自动沿依赖传播并返回修订报告。
report = detector.mark_anomaly("sensor-a", "impact", 2, 2)

# 当前判定、逐版本历史和全局修订日志。
decision = detector.decision("sensor-a", 3)
history = detector.decision_history("sensor-a", 3)
revisions = detector.revision_log()

# 撤回墓碑，重新纳入。
detector.withdraw_anomaly("sensor-a", "impact")

# 全量重放操作日志，验证增量结果和修订链。
assert replay_matches(detector)
```

最后一行也可直接使用模块函数：

```python
from drift_revision import replay_matches, assert_replay_matches
assert_replay_matches(detector)
```

## 需要保留的证据

要支持“事后标记、撤回、解释为什么前后判定不同”，至少要保留以下证据，不能只保存最新均值：

1. **原始样本**：`source`、`seq`、事件时间、原始精确数值或无损十进制表示、到达顺序。
2. **异常段墓碑**：段 ID、来源、闭区间、补录操作序号、补录原因；撤回操作序号、撤回原因和撤回状态。撤回不能物理删除旧段。
3. **判据配置**：窗口长度、最小基线数量、阈值、使用总体方差还是样本方差、严格/非严格比较。配置应按版本冻结或与判定版本关联。
4. **逐版判定证据**：状态、版本、父版本、基线 `n`、均值、方差、纳入样本键、排除样本键、每个被排除基线样本对应的覆盖段 ID、目标样本覆盖段 ID、当时有效的操作序号和人类可读判据说明。
5. **操作日志与修订链**：样本到达、补录、撤回的完整顺序；每个受影响键从旧版本到新版本的字段差异及触发原因。
6. **不可变发布记录**：旧报告若已对外发布，应保留旧版本和发布时间；修订产生新版本而不是覆盖旧版本，以便审计“当时为什么这么判”。

仅有滚动均值/方差的聚合值不足以重建历史：一旦某个旧值被排除，无法从旧均值中拆出其他成员，也无法证明哪些判定实际依赖该值。

## 文件结构

```text
drift_revision/engine.py     # 状态化增量检测器和全量重放
examples/local_demo.py       # 本地前后差异演示
tests/test_drift_detector.py # 极端值、全排除、重叠、重复、撤回、临界值、随机重放测试
```
