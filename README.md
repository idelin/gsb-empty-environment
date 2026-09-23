# MiniQA: 参数绑定与计划缓存

这是一个只使用 Python 标准库的本地关系代数小例子，不解析 SQL，也不调用任何 SQL 引擎。

## 语义

- 参数值只在执行期读取；物理计划中保留 `Param("...")`，不会把第一次绑定的值写死。
- 计划缓存键包含查询形状、统计版本、参数名、参数类型和影响访问路径的范围类别。
- 同类型且范围类别相同的不同值复用计划；范围变化或 `int`/`float` 类型变化会产生不同计划。
- 表统计版本变化时清空旧缓存，下一次执行重新规划并增加失效/构建统计。
- `NULL` 使用 SQL 三值逻辑：`TRUE`、`FALSE`、`UNKNOWN`；`WHERE` 只接受 `TRUE`。
- 结果和本地关系是多重集合，重复行用 `Counter` 的计数表示，`UNION ALL` 保留并累加重复。

## 文件

- `miniqa/model.py`：关系代数与谓词节点。
- `miniqa/table.py`：合成关系、列统计和本地有序索引。
- `miniqa/planner.py`：参数分类、谓词简化、物理计划和缓存。
- `miniqa/executor.py`：三值逻辑求值、顺序扫描、索引扫描和投影。
- `demo.py`：可运行样例。
- `test_miniqa.py`：`unittest` 测试。

## 运行

```bash
python3.11 demo.py
python3.11 -m unittest -v
```

## 本次样例输出

```text
amount >= 70: Counter({(3, 75): 1, (4, 90): 1, (5, 100): 1})
amount >= 25 (same int/in-range category): Counter({(2, 25): 2, (3, 75): 1, (4, 90): 1, (5, 100): 1})
status = PAID: Counter({(2, 'PAID'): 2, (1, 'PAID'): 1, (4, 'PAID'): 1, (5, 'PAID'): 1})
union all: Counter({(4,): 2, (5,): 2, (2,): 2, (1,): 1})
status = NULL: Counter()
cache stats before refresh: CacheStats(lookups=5, hits=1, misses=4, invalidations=0, builds=4)
amount >= 25 after stats change: Counter({(2, 25): 2, (3, 75): 1, (4, 90): 1, (5, 100): 1})
cache stats after refresh: CacheStats(lookups=6, hits=1, misses=5, invalidations=1, builds=5)
```

## 本次测试结果

```text
test_multiset_duplicates_and_union_all_are_preserved ... ok
test_equality_different_present_values_share_plan ... ok
test_null_column_is_unknown_even_under_negation ... ok
test_null_parameter_uses_three_valued_logic ... ok
test_parameter_on_left_uses_reversed_index_operator ... ok
test_range_category_changes_cause_separate_plan ... ok
test_same_type_different_values_use_cached_plan_without_baked_value ... ok
test_statistics_change_invalidates_and_replans ... ok
test_type_category_changes_cause_separate_plan ... ok
test_type_mismatch_is_unknown_including_nullable_column ... ok

----------------------------------------------------------------------
Ran 10 tests in 0.001s

OK
```
