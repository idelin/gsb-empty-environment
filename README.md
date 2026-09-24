# pivot_engine

纯标准库（Python 3.11）的增量长表转宽表引擎，不依赖任何现成查询引擎
（无 pandas / sqlite 等），状态仅用 `dict` 维护。

## 特性

- **带列类型的批次输入**：每个 `Batch` 声明 `key_type / category_type /
  value_type`（`int / float / str / bool`），逐行校验，类型不符抛 `TypeError`。
- **类别集合可增长、列序稳定**：类别列按首次出现顺序追加，schema 只增不减。
- **聚合冲突**：同键同类别多行按指定聚合合并（`sum` / `count`）。
- **撤回（retraction）**：行 op `"-"` 精确撤销一条存活插入；单元格清空即删，
  键的最后一个单元格消失时键行才删除，不影响其他类别。
- **schema 变更事件**：每次新增类别产生 `SchemaChanged` 事件
  （`engine.events`），批次结果携带 `schema_version`；
  `engine.align_values(values, from_version)` 把旧版本输出的值对齐到
  当前 schema，新增列补 `None`。
- **null 与 NaN 不同**：`None` 在 `sum`/`count` 中被跳过（全 null 的
  `sum` 结果为 null）；`NaN` 是真实浮点值，使 `sum` 结果为 `NaN`，
  且可被精确撤回。
- **整数精确计算**：int 求和永不经过 float（如 `2**70+1` 精确相加）；
  仅当浮点值存在时结果才为 float，浮点撤回后恢复精确 int。

## 用法

```python
from pivot_engine import Batch, PivotEngine

eng = PivotEngine(agg="sum")
r1 = eng.apply(Batch("str", "str", "int", rows=[
    ("+", "alice", "A", 10),
    ("+", "alice", "A", 3),      # 冲突 -> sum = 13
]))
r2 = eng.apply(Batch("str", "str", "int", rows=[("+", "bob", "B", 7)]))
print(eng.events)                # schema 变更事件
print(eng.snapshot().columns)    # ('key', 'A', 'B')
eng.align_values([13], from_version=r1.schema_version)  # -> [13, None]
eng.apply(Batch("str", "str", "int", rows=[("-", "alice", "A", 3)]))  # 撤回
```

## 运行

```sh
python3.11 demo.py                       # 运行样例
python3.11 -m unittest -v                # 运行测试
```
