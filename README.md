# 乱序计数器分析库

面向单调递增、固定位宽计数器的乱序采样分析。库只维护每个来源按观测顺序相邻的两个采样之间的差分；迟到插入或撤回时只替换相邻差分。

## 判定契约

每个来源配置：

- `width`：计数器位宽，模长为 `2 ** width`。
- `max_gap`：最大可判定采样间隔。
- `version`：配置版本，会复制到每条区间结果和修订记录。

本库采用严格的“最多一次环绕”判定前提：在间隔不超过 `max_gap` 时，真实增量必须小于 `2 ** width`。因此：

- 当前值不小于前值：增量为 `right.count - left.count`。
- 当前值小于前值：发生一次环绕，增量为 `right.count - left.count + 2 ** width`。
- 增量恰好为完整模长（前后读数相同但真实增长一圈）与零增量无法从两次采样区分；严格契约禁止在 `max_gap` 内发生完整模长增长。
- 相邻时间差大于 `max_gap`：结果为 `unknown_gap`，`delta=None`。
- 右侧采样带显式 `reset=True`：结果为 `reset`，`delta=None`。不能把 reset 后读数解释成环绕。

例如 8 位计数器 `250 -> 3` 且间隔不超过 `max_gap` 时，确定增量是 `3 - 250 + 256 = 9`。

## 顺序与决胜

同一来源按以下键升序排序：

1. `event_time`
2. `seq`
3. `sample_id`

`sample_id` 只是最后一层确定性决胜键，用于保证结果可复现；业务语义应把 `seq` 作为同时间事件的序号。同一来源出现相同 `(event_time, seq)` 会被拒绝，即使 `sample_id` 不同。

## 修订协议

迟到插入采样 `x` 时，若它位于 `a` 和 `b` 之间：

1. 删除并作废 `a -> b`。
2. 发布 `a -> x`。
3. 发布 `x -> b`。

撤回 `x` 时：

1. 删除并作废 `a -> x`。
2. 删除并作废 `x -> b`。
3. 若两侧都存在，重新发布 `a -> b`。

修订记录包含：

- 触发采样 `trigger_sample_id`
- `upsert` 或 `delete`
- 被替代的 `supersedes` 区间 ID
- 新区间和左右采样 ID
- `config_version`

下游可以按修订 ID 顺序应用。只有被删除的相邻区间失效，不相关区间不会重发。

## 总数与守恒

`known_delta` 只汇总 `definite` 区间：

- `reset` 是分段边界，不推算 reset 前到 reset 后的增长。
- `unknown_gap` 不计入总数。
- `exact=True` 只表示当前活跃采样之间没有未知间隔；它不跨越 reset 伪造一个连续计数值。
- 存在 `unknown_gap` 时，`known_delta` 是可观测增量的下限，不是完整总增量。

对于一个原本确定的区间，若插入的有效采样不引入 reset，并且两个新区间也都可判定，则：

```text
delta(a, b) = delta(a, x) + delta(x, b)
```

撤回同理。不能通过删除或忽略 `unknown_gap` 把不完整结果报告成完整总数。

## 使用方式

```python
from disordered_counter import CounterAnalyzer, Sample, SourceSpec

engine = CounterAnalyzer(
    {"device-a": SourceSpec(width=8, max_gap=10)},
    config_version="v1",
)

engine.add_samples([
    Sample("p1", "device-a", 0, 250),
    Sample("p2", "device-a", 4, 3),
])

print(engine.active_intervals[0].delta)  # 9
print(engine.summary().known_delta)      # 9
```

参考重算与逐步修订对照：

```python
reference = engine.recompute_reference()
diff = engine.reference_diff()
assert diff.matches
```

`recompute_intervals` 是独立的全量排序参考：它从当前活跃采样集合重新排序并重算所有相邻区间。随机测试在每次插入、撤回后都会：

- 重放修订日志构造当前区间；
- 与全量排序参考比较；
- 比较两边的区间字段和 `known_delta`；
- 检查确定区间拆分/撤回前后的增量守恒。

## 本地命令

```bash
PYTHONPATH=src python3 -m unittest discover -s tests -v
PYTHONPATH=src python3 examples/demo.py
```

代码只依赖 Python 3.11+ 标准库。
