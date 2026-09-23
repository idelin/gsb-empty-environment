# CDC 副本共同水位对账

纯 Python 3.11 标准库实现，不连接消息平台；变更数据由本地夹具构造。

## 数据模型

`cdc_compare.py` 中的 `ChangeEvent` 包含：

- `transaction_id`：事务 ID
- `table` 与 `primary_key`：实体位置
- `before_value` / `after_value`：变更前后值
- `commit_seq`：提交序号，也是水位比较依据

比较前会按 `commit_seq <= common_watermark` 截取双方事件，再生成每个实体的有序事件摘要和最终值。

## 对账规则

- 共同水位高于任一副本的高水位时，结果只包含 `lagging` 和落后副本，不做事件或值比较。
- 未显式指定水位时，使用双方高水位的较小值，避免把较新副本的后续事件误报为错误。
- `missing_events`：同一事件键只出现在一侧。即使实体最终值相同，也会单独列出缺失事件。
- `order_conflicts`：同一事件键在双方的提交序号不同。
- `event_value_divergences`：共同事件的前值或后值不一致。
- `final_value_divergences`：共同水位处实体最终值不一致；实体只存在一侧时用 `ABSENT` 表示。
- `ComparisonSession` 记录同一副本对最近一次成功比较水位，较低水位会抛出 `WatermarkRegressionError`。

## 运行测试

```bash
python3.11 -m unittest discover -s tests -v
```

最近一次测试结果：

```text
test_consistent_at_explicit_common_watermark ... ok
test_default_uses_minimum_progress_without_false_positive ... ok
test_duplicate_event_identity_is_invalid_fixture ... ok
test_entity_absent_on_one_side_is_final_value_divergence ... ok
test_event_above_replica_water_mark_is_invalid_fixture ... ok
test_event_values_and_final_value_are_compared_separately ... ok
test_lag_does_not_advance_session_watermark ... ok
test_lagging_replica_reports_only_lag ... ok
test_missing_event_reported_even_when_final_value_matches ... ok
test_order_conflict_when_same_event_has_different_commit_sequence ... ok
test_session_rejects_watermark_regression ... ok
test_snapshot_hides_events_after_common_watermark ... ok

Ran 12 tests in 0.001s

OK
```

## 运行样例

```bash
python3.11 example_run.py
```

样例输出：

```text
== compare at common watermark 10 ==
status=divergent
left=west
right=east
common_watermark=10
missing_events=1
  - orders:same-final tx=tx-2 seq=2 missing_on=right before='created' after=None
order_conflicts=1
  - orders:reordered tx=tx-5 left_seq=6 right_seq=5
event_value_divergences=1
  - orders:value-split tx=tx-7 seq=7
    before left='created' right='created'
    after  left='shipped' right='cancelled'
final_value_divergences=1
  - orders:value-split left='shipped' right='cancelled'

== request watermark 11 while east remains at 10 ==
status=lagging
left=west
right=east
common_watermark=11
lag=right:east requested=11 hwm=10

== attempt to roll back to watermark 9 ==
blocked: common watermark for pair ['east', 'west'] cannot move backwards: 9 < 10
```
