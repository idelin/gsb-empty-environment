# incremental-groupby

纯标准库（Python 3.11+）实现的**增量分组聚合**：按行 ID 支持插入 / 更新 / 删除，
实时维护每组的 `count` / `sum` / `min`，不依赖任何现成查询引擎。

## 语义

- **带列类型的批次输入**：`Batch(schema, rows)` 在构造时逐格校验 `int` / `float` / `str`
  类型（`bool` 不算 `int`；`float` 列会把 `int` 提升为 `float`）。
- **count 是 count(\*)**：null 值行仍计入 `count`；`sum` / `min` 跳过 null。
  全 null 组 `sum=None`、`min=None`，但 `count` 仍反映行数。
- **null ≠ NaN**：null 是缺失；NaN 是真实浮点值——参与 `sum`（使结果为 NaN，
  删除该行后恢复），但被 `min` 忽略。NaN 键归并为同一个组，与 null 键组并列。
- **整数精确**：`int` 列用 Python 任意精度整数求和。
- **原子性**：每个批次先整体校验再统一提交，失败批次不留痕迹；
  更新把行从 A 组迁到 B 组是在同一次提交内"旧组移除 + 新组加入"，绝不双计。
- **空组回收**：组内行数归零即从结果中移除。
- **min 的增量维护**：最小堆 + 懒惰删除计数器，删除当前最小值后次小值 O(log n) 浮现。

## 用法

```python
from incremental_groupby import Batch, GroupedAggregate, INT, STR, FLOAT

t = GroupedAggregate(key_type=STR, value_type=FLOAT)
schema = [("id", INT), ("key", STR), ("value", FLOAT)]

t.insert_batch(Batch(schema, [(1, "a", 10.0), (2, "a", 4.0), (3, None, None)]))
t.update_batch(Batch(schema, [(1, "b", 10.0)]))   # 跨组原子迁移
t.delete_batch(Batch([("id", INT)], [(2,)]))

# 混合批次（op 列为 I/U/D），整批原子：
t.apply_batch(Batch([("op", STR)] + schema,
                    [("I", 9, "a", 1.0), ("U", 1, "a", 2.0), ("D", 9, None, None)]))

for g in t.result():      # GroupResult(key, count, non_null_count, sum, min)
    print(g)
```

## 运行

```bash
python3 demo.py                          # 运行样例
python3 -m unittest test_incremental_groupby -v   # 24 个测试
```

测试包含一个随机差分测试：300 个随机操作后与"从头重算"的参考实现逐步对比。
