# counter-analysis

乱序计数器分析库：把原始计数读数解释为**环绕（wrap）**、**显式重置（reset）**或**不可知间隔（uncertain）**，并在迟到插入 / 撤回时只修订相邻差分，明确告诉下游哪些区间结果作废。

Python 3.11，零第三方依赖（测试用 pytest）。

## 核心语义

每个来源配置 `SourceConfig(bit_width, max_interval, config_version)`：

- `bit_width`：计数器位宽，模为 `2**bit_width`（如 8 位 → 256）。
- `max_interval`：最大可判定采样间隔。间隔 ≤ 该值时假设"最多一次环绕"，增量确定。
- `config_version`：配置版本，每个差分结果都记录判断时使用的版本。

相邻样本 `left -> right` 的判定规则（优先级从高到低）：

1. `right.reset` 为真：信任显式标记，计数器从 0 重启，增量 = `right.count`（与间隔长短无关）。
2. `left` 不存在：流起点（ORIGIN），基线未知，delta 为 `None`。
3. 间隔 > `max_interval`：UNCERTAIN，delta 为 `None`——可能是多次环绕或设备重置，不能都解释成增长。
4. 否则 DETERMINED，delta = `(right.count - left.count) mod 2**bit_width`。

于是同样是 250 → 3：间隔内无标记 → 确定的 +9；带 reset 标记 → +3；长时间缺测 → 不可知。

## 排序与同时间决胜

同一来源内样本按 `(event_time, seq, sample_id)` 排序：

- 主键 `event_time`；
- **`event_time` 相同时 `seq` 大者视为更晚的观测**（`seq` 是每源单调递增的发送序号）；
- 两者都相同则以 `sample_id` 字典序兜底，保证完全确定。同一 `sample_id` 重复插入会被拒绝。

## 增量修订

`CounterStore.insert / retract` 返回 `Revision(invalidated, emitted)`：

- 迟到插入只作废被劈开的那个区间，发出两个相邻新区间；
- 撤回只作废两个相邻区间，重建合并后的区间；
- 下游凭 `Diff.key == (left_id, right_id)` 精确作废旧结果，无需全量重算。

`reference_diffs` 是全量排序重算参考实现；随机测试逐步对照增量修订序列与全量重算，保证两者始终一致。

## 总量与守恒

`store.totals(source)` 只累加 DETERMINED / RESET 增量：

- 不确定段**不会**被静默丢弃：列在 `uncertain_intervals` 中且 `complete=False`，部分总量不会被伪装成完整总数；
- 向确定段补入有效中间采样前后，确定总量守恒（子段环绕数之和 = 整段环绕数）；
- 向不确定段补样只会把原本不可知的部分变为可知，不确定段随之收缩。

每个 `Diff` 都保留判断所用的 `left_id` / `right_id` 采样 ID 与 `config_version`。

## 运行

```bash
python3.11 -m pytest tests/          # 场景 + 守恒 + 随机对照测试
python3.11 examples/scenarios.py     # 四个 250 -> 3 场景演示
```
