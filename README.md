# 有序流 Merge Join（可溢写）

纯 Python 3.11 标准库实现，不调用 SQL 引擎。`merge_join.merge_join` 对两个按键升序排列的本地关系或可迭代流执行 inner join / left outer join。

## 语义

- 输入必须按连接键非严格升序排列；重复键形成一个组。
- 匹配键组做多重集合笛卡尔积：左组 `m` 行、右组 `n` 行时输出 `m × n` 行，并保留重复行。
- `None` 表示 SQL `NULL`；标量 `NULL` 和元组键中的 `NULL` 都不匹配任何键，包括另一个 `NULL`。
- left outer join 的未匹配左行输出为 `(left_row, None)`；右表 `NULL` 组被跳过。
- `NaN` 没有稳定排序，遇到后抛出 `InvalidJoinKeyError`。
- 检测到任一输入流键倒退时抛出 `UnsortedStreamError`，而不是静默产出错误结果。
- 每个键组独立暂存；超过 `max_in_memory` 后用 pickle 顺序溢写到临时文件，组处理完即删除。
- `threading.Event` 或任何带 `is_set()` 的对象可作为协作取消信号；信号置位后在下一次读取或输出边界抛出 `JoinCancelledError`。

## 文件

- `merge_join.py`：核心连接、顺序校验、有界暂存和溢写实现。
- `demo.py`：本地合成关系表示例，覆盖 2×3 六行、左外连接、`NULL` 和超大组溢写。
- `test_merge_join.py`：标准库 `unittest` 测试。
- `demo_output.txt`：本次实际演示输出。
- `test_results.txt`：本次实际测试输出。

## 运行

```bash
python3.11 demo.py
PYTHONDONTWRITEBYTECODE=1 python3.11 -m unittest -v
```

最小用法：

```python
from merge_join import merge_join

rows = list(
    merge_join(
        left_rows,
        right_rows,
        lambda row: row[0],
        how="left",
        max_in_memory=1000,
    )
)
```

也可以把连接键列索引直接传给第三、第四个参数：

```python
rows = list(merge_join(left_rows, right_rows, 0, 1, how="inner"))
```

## 本次演示输出

```text
small left outer join:
  (None, 'unknown department') <- None
  (1, 'Alice') <- (1, 'Platform')
  (1, 'Alice') <- (1, 'Data')
  (1, 'Alice') <- (1, 'Tools')
  (1, 'Bob') <- (1, 'Platform')
  (1, 'Bob') <- (1, 'Data')
  (1, 'Bob') <- (1, 'Tools')
  (2, 'Cara') <- None
  (4, 'Dana') <- None

large bounded group:
  left rows: 150
  right rows: 3
  output rows: 450
  memory bound per group: 50 rows
  left spilled: True
  spill file existed while processing group: True
```

## 本次测试结果

```text
test_cancellation_is_checked_between_output_rows (test_merge_join.MergeJoinTest.test_cancellation_is_checked_between_output_rows) ... ok
test_composite_null_never_matches_but_is_preserved_on_left (test_merge_join.MergeJoinTest.test_composite_null_never_matches_but_is_preserved_on_left) ... ok
test_duplicates_have_multiset_semantics (test_merge_join.MergeJoinTest.test_duplicates_have_multiset_semantics) ... ok
test_group_larger_than_memory_bound_spills_and_completes (test_merge_join.MergeJoinTest.test_group_larger_than_memory_bound_spills_and_completes) ... ok
test_left_outer_null_and_unmatched_semantics (test_merge_join.MergeJoinTest.test_left_outer_null_and_unmatched_semantics) ... ok
test_multiple_null_rows_are_all_preserved_on_left (test_merge_join.MergeJoinTest.test_multiple_null_rows_are_all_preserved_on_left) ... ok
test_spill_files_are_removed_after_iteration (test_merge_join.MergeJoinTest.test_spill_files_are_removed_after_iteration) ... ok
test_two_by_three_duplicate_group_outputs_six_rows (test_merge_join.MergeJoinTest.test_two_by_three_duplicate_group_outputs_six_rows) ... ok
test_unsorted_left_raises_instead_of_emitting_wrong_result (test_merge_join.MergeJoinTest.test_unsorted_left_raises_instead_of_emitting_wrong_result) ... ok
test_unsorted_null_group_raises_before_left_outer_output (test_merge_join.MergeJoinTest.test_unsorted_null_group_raises_before_left_outer_output) ... ok
test_unsorted_right_raises_within_duplicate_group (test_merge_join.MergeJoinTest.test_unsorted_right_raises_within_duplicate_group) ... ok
test_unsorted_unmatched_left_group_raises_before_output (test_merge_join.MergeJoinTest.test_unsorted_unmatched_left_group_raises_before_output) ... ok

----------------------------------------------------------------------
Ran 12 tests in 0.005s

OK
```
