# 按键列去重引擎（纯标准库，Python 3.11）

按指定键列对表去重，保留首条或末条。不依赖任何现成查询引擎，仅使用标准库。

## 语义约定

- **批次输入**：表以 `(columns, rows)` 批次追加，`columns` 为 `[(列名, 类型), ...]`，
  类型支持 `int / float / str / bool`；所有批次 schema 必须一致，否则抛 `SchemaError`。
- **null**：`None` 参与去重时按相等处理（null == null），任何列都允许为 null。
- **NaN**：与 null 不同；NaN == NaN（同组），`-0.0 == 0.0`（IEEE 语义）。
- **整数精确**：`int` 列用 Python 任意精度整数比较，不经浮点，2^63 与 2^63+1 可区分。
- **哈希碰撞**：自研链式哈希表（`HashTable`），哈希仅用于分桶，同桶内按 `==` 判定；
  即使注入 `hash_fn=lambda k: 0` 强制全部碰撞，不同值仍被区分。
- **事件序**：全局行序号按"批次到达序 + 批内行序"单调递增；`keep="last"` 时
  后到事件覆盖同键旧值（含跨批次更新旧 ID）。
- **结果顺序**：由被保留行的原始全局序号升序决定，与键无关。
- **空键列**：`key_columns=[]` 表示全表同一组，`first` 留首行、`last` 留末行。
- **类型严格**：值必须与声明类型一致（`bool` 不算 `int`，`int` 不算 `float`），
  否则抛 `RowTypeError`。

## 文件

- `dedup.py` — 引擎：`DedupEngine` / `HashTable` / `_normalize`
- `test_dedup.py` — unittest 测试（29 例）
- `demo.py` — 运行样例

## API 速览

```python
from dedup import DedupEngine

eng = DedupEngine(["id"], keep="last")        # 或 keep="first"
eng.add_batch([("id", "int"), ("v", "str")], [(1, "a"), (2, "b")])
eng.add_batch([("id", "int"), ("v", "str")], [(1, "a2")])   # 更新旧 id=1
eng.result()          # [(2, 'b'), (1, 'a2')] —— 按保留行原序号排序
eng.result_entries()  # [(1, (2, 'b')), (2, (1, 'a2'))] —— 带原始序号
```

## 运行样例（`python3.11 demo.py`）

```
== keep=first, key=[name], 两个批次（null 键相等） ==
  seq=0  row=(1, 'alice', 90.0)
  seq=1  row=(2, None, 80.0)
  seq=4  row=(5, 'bob', 60.0)

== keep=last, key=[id], 批次更新旧 ID ==
  seq=1  row=(2, 'bob', 80.0)
  seq=2  row=(1, 'alice', 95.5)

== keep=first, key=[id], 强制哈希碰撞 ==
  seq=0  row=(1, 'a', 1.0)
  seq=1  row=(2, 'b', 2.0)
  seq=3  row=(3, 'c', 3.0)

== keep=first, key=[score], null != NaN ==
  seq=0  row=(1, 'null', None)
  seq=1  row=(2, 'nan', nan)

== keep=first, key=[id], 大整数精确区分 2^63 与 2^63+1 ==
  seq=0  row=(9223372036854775808, 'x', 1.0)
  seq=1  row=(9223372036854775809, 'y', 2.0)

== keep=last, key=[]（空键列，全表一组） ==
  seq=2  row=(3, 'c', 3.0)
```

## 测试结果（`python3.11 -m unittest test_dedup -v`）

```
test_keep_first_basic ... ok
test_keep_first_across_batches ... ok
test_result_order_by_kept_row_seq ... ok
test_keep_last_basic ... ok
test_keep_last_batch_update_event_order ... ok
test_keep_last_result_order_uses_updating_row_seq ... ok
test_composite_key ... ok
test_nan_equals_nan ... ok
test_negative_zero_equals_zero ... ok
test_null_differs_from_nan ... ok
test_null_equal_under_forced_collision ... ok
test_null_keys_are_equal ... ok
test_forced_collision_still_distinguishes_values ... ok
test_forced_collision_keep_last ... ok
test_hash_table_unit ... ok
test_big_ints_exact ... ok
test_huge_int_no_float_rounding ... ok
test_empty_key_columns_single_group_first ... ok
test_empty_key_columns_single_group_last ... ok
test_bad_keep_value ... ok
test_bool_not_accepted_as_int ... ok
test_duplicate_column_names ... ok
test_int_not_accepted_as_float ... ok
test_none_allowed_in_any_column ... ok
test_row_width_mismatch ... ok
test_schema_mismatch_between_batches ... ok
test_type_mismatch ... ok
test_unknown_key_column ... ok
test_unsupported_column_type ... ok

Ran 29 tests in 0.003s

OK
```
