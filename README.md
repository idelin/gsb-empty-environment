# grouped-agg

纯标准库（Python 3.11）实现的分组增量聚合表，维护每组的 `count(*)` / `sum` / `min`，
支持行级插入、更新、删除。不依赖任何现成查询引擎。

## 语义

- **类型化批次输入**：`Batch` 携带列类型（`INT` / `FLOAT` / `STR` / `BOOL`），入库前逐值校验。
- **NULL ≠ NaN**：`None` 是 SQL 风格 NULL，不参与 `sum`/`min` 但计入 `count(*)`；
  `float('nan')` 是普通值，参与 `sum`（传播 NaN），`min` 排在所有非 NaN 之后。
- **全 NULL 组**：`sum`/`min` 为 `None`，`count(*)` 仍计行数。
- **整数精确**：Python 任意精度 int，和式增删用精确减法维护。
- **跨组原子迁移**：`update` 改组键时先整批校验、再统一应用，同一行 ID 从 A 组迁到 B 组不会双计；
  任何校验失败整批回滚，状态不变。
- **空组回收**：组内行数归零即从结果中移除并释放内部结构。
- **min 的维护**：每组一个有序多重集合，删掉当前最小值后次小值立即可得。

## 用法

```python
from grouped_agg import Batch, ColType, GroupedAggregate

agg = GroupedAggregate(key_type=ColType.STR, value_type=ColType.INT)
agg.insert_batch(Batch.from_rows(
    {"id": ColType.INT, "key": ColType.STR, "value": ColType.INT},
    [(1, "A", 10), (2, "A", 20), (3, "B", 5)],
))
agg.update(1, key="B")      # 原子迁移 A -> B
agg.delete(3)               # 删除 B 的最小值，次小值上浮
agg.result()                # [GroupRow('A', 1, 20, 20), GroupRow('B', 2, 30, 10)]
```

`update(row_id, key=..., value=...)` 缺省参数表示保持不变；显式传 `None` 表示置为 NULL。

## 运行

```sh
python3.11 demo.py          # 运行样例
python3.11 -m unittest -v   # 35 个单元测试
```

## 文件

- `grouped_agg.py` — 实现（类型、批次、有序多重集合、分组状态、聚合表）
- `test_grouped_agg.py` — unittest 测试套件
- `demo.py` — 运行样例
