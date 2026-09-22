# counter-analysis

乱序回绕计数器分析库（Python 3.11+，零依赖）。区分「计数器环绕」与「设备重置」，
对每个相邻采样区间给出**确定 / 不确定**的增量判定，支持迟到插入与撤回，并向
下游精确报告哪些区间结果作废。

## 判定模型

每个 source 配置 `SourceConfig(bit_width, max_interval)`：

- `bit_width`：计数器按 `2**bit_width` 取模环绕。
- `max_interval`：最大可判定采样间隔。只有相邻采样间隔 `<= max_interval`
  才满足「间隔内最多环绕一次」，增量可判定。

对排序后相邻的两个采样 `left -> right`：

| 条件 | 判定 | delta |
|---|---|---|
| `right.reset == True` | 确定（显式重置） | `right.count`（重置后累计值，与间隔无关） |
| `gap <= max_interval` | 确定（最多一次环绕） | `(right.count - left.count) mod 2**w` |
| `gap > max_interval` | **不确定** | `None`；`min_delta = (r-l) mod 2**w` 为下界，真值是 `min_delta + k*2**w`（k>=0 未知） |

例：8 位计数器 `250 -> 3`，间隔 5s <= max_interval 时增量确定为 9；
若右侧采样带 `reset=True`，同样的数字增量是 3（设备重启）；若间隔
100s > max_interval，则不确定——可能是 9，也可能是 265、521……

## 排序与决胜规则

同一 source 的采样按 `(event_time, seq, sample_id)` 全序排列：
先比事件时间；**同时间由较小的 seq 决胜**（seq 是产生顺序号）；
`(time, seq)` 仍相同则由 `sample_id` 保证确定性。撤回后重插同 id 视为非法
（id 在存活集合内唯一）。

## 增量修订与作废报告

`IncrementalEngine.insert(sample)` / `retract(sample_id)` 只重算受影响
的相邻区间，返回 `Revision`：

- `removed`：作废的区间判定（含旧结果，下游据此撤回派生状态）；
- `added`：新产生的区间判定；
- `definite_total_delta`：该 source 确定总增量的变化量。

每个 `IntervalResult` 都携带 `left_id` / `right_id`（判定使用的采样 ID）
和 `config_version`（判定使用的配置版本）。

## 总量与守恒

`engine.total(source)` 返回 `SourceTotal`：

- `definite_sum`：仅对可判定区间求和；
- `min_sum`：含不确定区间下界的总和下界；
- `uncertain`：不确定区间 ID 列表；
- `complete`：仅当无不确定区间时为 `True`。**不确定段存在时
  `definite_sum` 只是部分量，不能当作完整总数对外发布**——库不提供
  「丢掉不确定段凑出总数」的接口。

守恒性：补入有效中间采样后，确定总增量的变化恰好等于被修订区间的
差分之和（`definite_total_delta`），且与全量排序重算（`reference.recompute`）
逐步一致——随机差分测试（20 种子 × 120 操作）对此逐步断言。

## 运行测试

```bash
python3 -m pytest -q
```

- `tests/test_scenarios.py`：250→3 确定环绕、带 reset 的相同数字、
  长时间缺测、中间补样守恒、同时间 seq 决胜、撤回修订。
- `tests/test_randomized.py`：本地生成的随机操作序列，增量引擎与
  全量重算参考逐步对照。
