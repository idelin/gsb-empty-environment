# 事件时间容差键连接（Interval Join + 撤回 + 水位清理）

按 key 连接左右两条变更流：当 `|left.ts - right.ts| <= tolerance`（闭区间，
恰容差边界包含）时建立配对。实现为纯 Python 3.11 标准库，输入全部来自
本地夹具，不依赖也不连接任何消息平台。

## 语义约定

- **一左多右**：一条左记录可与任意多条右记录配对（反之亦然，按 key 笛卡尔匹配）。
- **配对 ID**：`"{左主键}|{右主键}"`，稳定且全局去重；`live_pairs()` 返回
  当前仍存活的去重 ID 列表，`finalized_pairs()` 返回已最终化 ID。
- **changelog 去重**：输出 `PairEvent(sign='+'/'-')`，同一 ID 只在状态真正
  翻转时下发（连续重复的 `+` 或 `-` 不会出现）。
- **撤回（CDC before-image）**：
  - 配对尚未最终化：`DELETE` / `UPDATE` 的前值撤回活跃配对（`-`）。
  - 行已随水位清理、配对已最终化：迟到删除**不会**静默改写或重建结果，
    旁路报告 `LATE_DELETE`；删除从未存在的行报 `STALE_DELETE`。
- **水位与清理**：
  - 行在 `row.ts + tolerance <= watermark` 时封闭并释放状态（闭区间）。
  - 配对在 `max(left.ts, right.ts) + tolerance <= watermark` 时最终化；
    每次水位推进后有兜底扫描，保证一侧先冻结时配对仍能在对侧到期后最终化。
  - 水位单调递增；回退水位与重复提交序号（`seq`）幂等忽略。
- **迟到 / 越界**：`event_ts <= watermark` 的插入（含 UPDATE 后值）不入状态，
  旁路报告 `LATE_INSERT`；非左/右表报 `UNKNOWN_TABLE`。所有越界均显式报告，
  不静默丢弃。
- **变更结构**：`Change(seq, table, pk, before, after, op?)`，含提交序号、
  表名、主键、前值、后值；`op` 省略时按 before/after 自动推断
  （INSERT / UPDATE / DELETE）。

## 文件

| 文件 | 说明 |
| --- | --- |
| `event_join.py` | 引擎：`ToleranceJoin`、`Change`、`Watermark`、`PairEvent`、`LateReport` |
| `demo/fixtures.py` | 本地夹具（订单 × 发货，容差 5，5 个场景） |
| `demo/run_demo.py` | 演示运行器，逐条摄入并打印结果 |
| `test_event_join.py` | `unittest` 测试，35 个用例 |

## 运行

```bash
# 测试
python3.11 -m unittest test_event_join -v

# 演示
python3.11 demo/run_demo.py
```

引擎也可作为库直接使用：

```python
from event_join import ToleranceJoin, Change, Watermark

j = ToleranceJoin("orders", "shipments", key_col="oid", ts_col="ts", tolerance=5)
j.ingest(Change(1, "orders", "O1", None, {"oid": "A", "ts": 100}))
j.ingest(Change(2, "shipments", "S1", None, {"oid": "A", "ts": 105}))  # 恰边界
j.ingest(Watermark(3, 110))          # max(100,105)+5=110 → 最终化并清理
print(j.finalized_pairs())           # ['O1|S1']
j.ingest(Change(4, "shipments", "S1", {"oid": "A", "ts": 105}, None))  # 迟到删除
print(j.finalized_pairs())           # ['O1|S1']，结果不被静默撤回
print([r.reason for r in j.late])    # ['LATE_DELETE']
```

## 运行样例

`python3.11 demo/run_demo.py` 的完整输出（`demo/fixtures.py` 中的 5 个场景：
一左多右与边界、最终化前撤回、水位清理最终化、迟到/越界旁路、配对 ID 去重）：

```text
========================================================================
事件时间容差键连接演示  (左=orders 右=shipments 容差=5 闭区间)
========================================================================
[seq= 1] INSERT orders    pk=O1      +{'oid': 'A', 'ts': 100}
         # 左：订单 O1（A @100）
[seq= 2] INSERT shipments pk=S1      +{'oid': 'A', 'ts': 105}
         # 右：发货 S1（A @105，|dt|=5 恰边界→连接）  => 输出: +O1|S1
[seq= 3] INSERT shipments pk=S2      +{'oid': 'A', 'ts': 104}
         # 右：发货 S2（A @104→连接，一左多右）  => 输出: +O1|S2
[seq= 4] INSERT shipments pk=S3      +{'oid': 'A', 'ts': 106}
         # 右：发货 S3（A @106，|dt|=6 超界→不连接）
[seq= 5] INSERT orders    pk=O9      +{'oid': 'Z', 'ts': 200}
         # 左：订单 O9（键 Z，无关键，留存状态）
[seq=10] INSERT orders    pk=O2      +{'oid': 'B', 'ts': 100}
         # 左：订单 O2（B @100）
[seq=11] INSERT shipments pk=S4      +{'oid': 'B', 'ts': 102}
         # 右：发货 S4（B @102→连接）  => 输出: +O2|S4
[seq=12] DELETE orders    pk=O2      -{'oid': 'B', 'ts': 100}
         # 左：删除 O2（配对尚未最终化→撤回 O2|S4）  => 输出: -O2|S4
[seq=20] WATERMARK  ts=104  (seq=20)
         # 水位→104（O1 需 wm>=105 才封闭，暂不最终化）
[seq=21] WATERMARK  ts=105  (seq=21)
         # 水位→105（O1 到期冻结；配对须等较晚一侧 max(ts)+5）
[seq=22] WATERMARK  ts=109  (seq=22)
         # 水位→109（S2 到期：max(100,104)+5=109→O1|S2 最终化）
[seq=23] WATERMARK  ts=110  (seq=23)
         # 水位→110（S1 到期：max(100,105)+5=110→O1|S1 最终化）
[seq=24] WATERMARK  ts=111  (seq=24)
         # 水位→111（S3 无配对，直接清理）
[seq=30] INSERT orders    pk=O_LATE  +{'oid': 'A', 'ts': 100}
         # 迟到插入 A@100（ts<=wm 111→旁路报告）
[seq=31] DELETE shipments pk=S1      -{'oid': 'A', 'ts': 105}
         # 迟到删除已清理的 S1（已最终化结果保持不变）
[seq=32] DELETE shipments pk=S_GHOST -{'oid': 'A', 'ts': 100}
         # 删除从未存在的 S_GHOST（越界报告）
[seq=33] INSERT audit     pk=X1      +{'oid': 'A', 'ts': 111}
         # 非左/右表 audit 的变更（旁路报告，不处理）
[seq=40] WATERMARK  ts=112  (seq=40)
         # 水位→112
[seq=41] INSERT orders    pk=O7      +{'oid': 'C', 'ts': 300}
         # 左：订单 O7（C @300）
[seq=42] INSERT shipments pk=S7      +{'oid': 'C', 'ts': 302}
         # 右：发货 S7（C @302→连接）  => 输出: +O7|S7
[seq=43] INSERT orders    pk=O7      +{'oid': 'C', 'ts': 300}
         # 重复投递 O7（同键同时间→幂等，不产生新配对事件）
[seq=44] UPDATE orders    pk=O7      {'oid': 'C', 'ts': 300} -> {'oid': 'C', 'ts': 301}
         # 更新 O7：C@300→C@301（撤旧建新，最终仍是同一个配对 ID）  => 输出: -O7|S7, +O7|S7
[seq=41] INSERT orders    pk=O7      +{'oid': 'C', 'ts': 300}
         # 重放 seq=41（提交序号重复→忽略）
========================================================================
配对 changelog（去重后的 + 建立 / - 撤回）
------------------------------------------------------------------------
  + O1|S1        key=A  左ts=100 右ts=105 (seq=2)
  + O1|S2        key=A  左ts=100 右ts=104 (seq=3)
  + O2|S4        key=B  左ts=100 右ts=102 (seq=11)
  - O2|S4        key=B  左ts=100 右ts=102 (seq=12)
  + O7|S7        key=C  左ts=300 右ts=302 (seq=42)
  - O7|S7        key=C  左ts=300 右ts=302 (seq=44)
  + O7|S7        key=C  左ts=301 右ts=302 (seq=44)
------------------------------------------------------------------------
当前存活配对 ID（去重） : ['O1|S1', 'O1|S2', 'O7|S7']
已最终化配对 ID         : ['O1|S1', 'O1|S2']
最终水位                : 112
------------------------------------------------------------------------
迟到 / 越界旁路报告（未静默丢弃）
  seq=30 LATE_INSERT   INSERT orders.O_LATE key=A event_ts=100 wm=111  # 事件时间不晚于当前水位，属于迟到越界数据，不入状态
  seq=31 LATE_DELETE   DELETE shipments.S1 key=A event_ts=105 wm=111  # 目标行已随水位清理并最终化，迟到删除不会静默撤回结果
  seq=32 STALE_DELETE  DELETE shipments.S_GHOST key=A event_ts=100 wm=111  # 目标行不存在或已被删除
  seq=33 UNKNOWN_TABLE INSERT audit.X1 key=A event_ts=111 wm=111  # 非左/右表，已忽略
========================================================================
```

## 测试结果

`python3.11 -m unittest test_event_join -v` 运行 35 个用例，全部通过：

```text
test_different_keys_never_join (test_event_join.TestBasicJoin.test_different_keys_never_join) ... ok
test_match_in_both_arrival_orders (test_event_join.TestBasicJoin.test_match_in_both_arrival_orders) ... ok
test_match_within_tolerance_and_boundary_inclusive (test_event_join.TestBasicJoin.test_match_within_tolerance_and_boundary_inclusive) ... ok
test_negative_tolerance_rejected (test_event_join.TestBasicJoin.test_negative_tolerance_rejected) ... ok
test_outside_tolerance_no_match (test_event_join.TestBasicJoin.test_outside_tolerance_no_match) ... ok
test_zero_tolerance_requires_equal_timestamp (test_event_join.TestBasicJoin.test_zero_tolerance_requires_equal_timestamp) ... ok
test_explicit_ops_validated (test_event_join.TestChangeAndFixtureContract.test_explicit_ops_validated) ... ok
test_op_inferred_from_images (test_event_join.TestChangeAndFixtureContract.test_op_inferred_from_images) ... ok
test_update_event_ts_defaults_to_before_ts (test_event_join.TestChangeAndFixtureContract.test_update_event_ts_defaults_to_before_ts) ... ok
test_changelog_toggle_dedup_with_update (test_event_join.TestDeduplication.test_changelog_toggle_dedup_with_update) ... ok
test_duplicate_commit_seq_is_idempotent (test_event_join.TestDeduplication.test_duplicate_commit_seq_is_idempotent) ... ok
test_live_pairs_ids_unique (test_event_join.TestDeduplication.test_live_pairs_ids_unique) ... ok
test_pair_not_recreated_after_finalization (test_event_join.TestDeduplication.test_pair_not_recreated_after_finalization) ... ok
test_end_to_end_outcomes (test_event_join.TestDemoFixtureEndToEnd.test_end_to_end_outcomes) ... ok
test_pair_ids_are_unique_in_output_view (test_event_join.TestDemoFixtureEndToEnd.test_pair_ids_are_unique_in_output_view) ... ok
test_late_delete_after_finalization_keeps_result (test_event_join.TestLateAndOutOfBounds.test_late_delete_after_finalization_keeps_result) ... ok
test_late_insert_is_reported_not_joined (test_event_join.TestLateAndOutOfBounds.test_late_insert_is_reported_not_joined) ... ok
test_late_update_cannot_silently_rebuild (test_event_join.TestLateAndOutOfBounds.test_late_update_cannot_silently_rebuild) ... ok
test_on_time_delete_with_high_watermark_still_works (test_event_join.TestLateAndOutOfBounds.test_on_time_delete_with_high_watermark_still_works) ... ok
test_unknown_table_reported (test_event_join.TestLateAndOutOfBounds.test_unknown_table_reported) ... ok
test_many_left_one_right_also_matches (test_event_join.TestOneLeftManyRight.test_many_left_one_right_also_matches) ... ok
test_one_left_matches_many_rights (test_event_join.TestOneLeftManyRight.test_one_left_matches_many_rights) ... ok
test_delete_one_right_leaves_other_pairs (test_event_join.TestRetraction.test_delete_one_right_leaves_other_pairs) ... ok
test_delete_retracts_pending_pair (test_event_join.TestRetraction.test_delete_retracts_pending_pair) ... ok
test_delete_unknown_row_is_stale (test_event_join.TestRetraction.test_delete_unknown_row_is_stale) ... ok
test_reinsert_after_pending_delete_relinks (test_event_join.TestRetraction.test_reinsert_after_pending_delete_relinks) ... ok
test_update_changes_key_moves_pair (test_event_join.TestRetraction.test_update_changes_key_moves_pair) ... ok
test_update_same_key_same_ts_is_stable (test_event_join.TestRetraction.test_update_same_key_same_ts_is_stable) ... ok
test_exact_boundary_finalizes_both_sides (test_event_join.TestWatermarkAndFinalization.test_exact_boundary_finalizes_both_sides) ... ok
test_finalization_after_one_side_froze_first (test_event_join.TestWatermarkAndFinalization.test_finalization_after_one_side_froze_first) ... ok
test_non_monotonic_watermark_ignored (test_event_join.TestWatermarkAndFinalization.test_non_monotonic_watermark_ignored) ... ok
test_pair_finalizes_when_second_side_closes (test_event_join.TestWatermarkAndFinalization.test_pair_finalizes_when_second_side_closes) ... ok
test_pending_retraction_between_watermarks (test_event_join.TestWatermarkAndFinalization.test_pending_retraction_between_watermarks) ... ok
test_state_released_after_cleanup (test_event_join.TestWatermarkAndFinalization.test_state_released_after_cleanup) ... ok
test_unmatched_rows_are_cleaned_without_report (test_event_join.TestWatermarkAndFinalization.test_unmatched_rows_are_cleaned_without_report) ... ok
Ran 35 tests in 0.004s
OK
```

覆盖点：容差内/恰边界/超界、key 不同不连接、零容差、双向到达顺序、
一左多右（含多左一右）、changelog 翻转去重、重复提交序号幂等、
最终化后旧事件不重建、DELETE/UPDATE 撤回（含改 key、改 ts、删后重建）、
水位闭区间冻结、两侧不同时刻封闭的兜底最终化、挂起配对跨水位撤回、
状态释放、`LATE_INSERT` / `LATE_DELETE` / `STALE_DELETE` / `UNKNOWN_TABLE`
四类旁路报告、op 自动推断，以及完整夹具的端到端校验。
