# wrapcounter —— 乱序环绕计数器分析库

面向会发生 **8 位（任意位宽）计数器环绕**、**设备重置**、**乱序/迟到/撤回采样**
的累计计数场景。核心立场：

> 累计计数从 250 掉到 3，既可能是环绕，也可能是重置，还可能是缺测，
> **不能无条件解释成“增长 9”**。本库只在信息足够时给出确定增量，
> 否则明确标记不确定，并保证补样前后确定总量守恒、绝不靠丢弃不确定段伪造完整总数。

## 判定模型

设来源配置位宽 `width_bits`，模数 `M = 2**width_bits`，
最大可判定采样间隔 `max_gap_seconds`。相邻两采样（右端 `curr`、左端 `prev`）：

1. `curr.reset == True`：区间跨越显式重置，**恒不确定**（`reset_crossing`）；
   首尾读数恰好相等时细化为 `equal_after_reset`（0 / 一整圈 M / 重置后相等不可区分）。
2. `dt > max_gap_seconds`：无法排除中间多次环绕，**不确定**（`gap_too_large`）。
3. `dt <= max_gap_seconds` 且无 reset（合同：窗口内真实增量 `0 <= d < M`）：
   - `curr > prev`：`direct`，`delta = curr - prev`
   - `curr < prev`：`wrap`，`delta = M - prev + curr` —— 250→3（M=256）即 **9**
   - `curr == prev`：`zero_growth`，`delta = 0`

> 同值但带不带 reset 是两回事：`42→42` 无 reset 判 0 增长；
> 带 reset 判不确定。判定合同（不足一整圈）是来源级语义假设，由 `max_gap_seconds` 量化。

### 同时间决胜

全序键为 `(event_time, seq, sample_id)`：同一时间戳按 `seq` 升序，
再相同用 `sample_id` 兜底。因此 `250@seq0 → 3@seq1` 判环绕 +9，反序就是 247。
同 `(event_time, seq)` 重复送达支持 `ERROR / REPLACE / IGNORE` 三种策略。

## 乱序修订

`CounterAnalyzer` 增量维护每个来源的全序采样与相邻区间：

- **迟到插入**：先 `REVOKE` 被拆分的那一个旧相邻区间（或旧首锚点），
  再 `UPSERT` 左右两个新区间；远离插入点的区间 `interval_id` 与结果不变。
- **撤回采样**：`REVOKE` 其前后最多两个相邻区间；两侧都在时补发合并后的新区间。
- **配置更新**：旧 `config_version` 的全部区间作废（REVOKE 保留旧版本号），
  按新版本重判。
- 每条 `Revision` 带全局单调 `revision_id`、操作类型、被替换区间、原因、
  `config_version` 与触发采样 ID。

### 守恒与不伪造

- 有效中间采样补入前后，可确定总增量守恒：`delta(a→c) = delta(a→b) + delta(b→c)`
  （只要三段都可判定）。随机差分测试对每条修订逐步验证。
- `SourceTotal` 同时返回 `definite_delta`（只累加确定段）、
  `complete`（是否无洞）与 `uncertain_interval_ids`（作废区间清单）。
  **不确定段绝不计入总数，也不会被静默丢弃伪装成完整结果。**

## 结果溯源

每个 `IntervalResult` 携带 `interval_id = "<source>:<prev_id>-><curr_id>"`
（首采样锚点为 `<source>:ROOT-><id>`）、两端采样 ID、两端读数、`dt`、
状态、理由、环绕次数、`reset_boundary` 与判定所用 `config_version`。
`SourceTotal` 携带全序采样 ID 链、区间链与配置版本。

## 全量重算参考

`wrapcounter.recompute_source / recompute_all` 是无状态全量排序重算参考。
任何时刻都与增量引擎当前视图逐字段一致（测试中每一步操作后对照）。
`examples/demo.py` 演示全部场景；修订流本身也可在白纸上重放（见
`tests/test_revision_replay.py`）。

## 快速开始

```python
from wrapcounter import CounterAnalyzer, Sample, SourceConfig

engine = CounterAnalyzer()
engine.register_source(SourceConfig("dev", width_bits=8, max_gap_seconds=30))
engine.upsert_sample(Sample("a", "dev", 250, event_time=0.0))
engine.upsert_sample(Sample("b", "dev", 3,   event_time=10.0))
print(engine.source_total("dev").definite_delta)  # 9

revs = engine.upsert_sample(Sample("mid", "dev", 4, event_time=5.0))
# REVOKE dev:a->b; UPSERT dev:a->mid(+10); UPSERT dev:mid->b(+255)
print(engine.source_total("dev").definite_delta)  # 仍然是 9（守恒）
```

## 运行

```bash
python3 examples/demo.py
python3 -m unittest discover -s tests -v
WRAPCOUNTER_TEST_SEED=42 python3 -m unittest tests.test_random -v
```

仅依赖 Python 3.11 标准库；无第三方包。测试全部本地生成（固定样例 +
带隐藏真值的随机乱序/重置/撤回差分测试）。

## 代码结构

```
wrapcounter/
  model.py     数据模型与总量聚合
  judge.py     相邻差分判定（环绕/reset/间隔门限）
  reference.py 全量排序重算参考
  engine.py    增量引擎：乱序插入/撤回/配置修订 + Revision 流
tests/         固定样例、随机差分、修订流重放
examples/demo.py
```
