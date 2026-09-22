# IVM：过滤 / 投影 / 连接 / 分组的增量视图维护

纯 Python 3.11 标准库实现（不调用任何 SQL 引擎）。基表接收带行 ID 的
插删，算子以**多重数差量** `(row_tuple, count_delta)` 逐级传播，最终视图
物化为多重集合（bag）。任何多重数降为负即抛 `NegativeMultiplicityError`。

## 文件

| 文件 | 说明 |
|---|---|
| `ivm.py` | 引擎：`BaseTable` / `Filter` / `Project` / `Join` / `GroupBy` / `View` |
| `sample_data.py` | 本地合成的关系表（employees、departments） |
| `demo.py` | 可运行样例：`python3.11 demo.py` |
| `test_ivm.py` | 18 个 unittest：`python3.11 -m unittest -v` |

## 语义约定（显式）

- **多重集合**：所有关系都是 bag，每个不同元组携带多重数；投影不去重，
  多重数累加。
- **NULL（SQL 风格三值逻辑）**：
  - 连接：连接键含 NULL 永不匹配（NULL ≠ NULL）；
  - 过滤：谓词为三值，仅 `True` 保留行（`False`/`UNKNOWN` 丢弃）；
  - 分组：NULL 是普通分组键，所有 NULL 同组；
  - `SUM` 忽略 NULL，全 NULL 组的和为 NULL；`COUNT(*)` 计行数，
    `COUNT(col)` 计非 NULL 值。
- **连接差量**：一侧新增多重数为 m 的行，与另一侧多重数为 n 的重复行
  相乘，贡献 m×n。
- **撤回**：删除按行 ID 定位；最后一个支持行被撤回时，下游输出删除差量，
  视图中的行（或分组）消失。
- **批次乱序**：所有算子基于计数状态，合法批次序列按任意顺序应用后，
  最终视图一致（差量流顺序可不同，多重集合相同）。
- **错误**：`NegativeMultiplicityError`（负重数）、`DuplicateRowIdError`、
  `UnknownRowIdError`；基表批次先整体校验再传播。

## 样例视图

```sql
SELECT e.dept, d.building, COUNT(*), SUM(e.salary)
FROM employees e JOIN departments d ON e.dept = d.dept
WHERE e.age >= 30
GROUP BY e.dept, d.building
```

## 运行样例输出（`python3.11 demo.py`）

```
== batch 1: load departments + first employees ==
    delta +1 x ('eng', 'A', 1, 100)
    delta +1 x ('ops', 'B', 1, 80)
  view now:
    1 x ('eng', 'A', 1, 100)
    1 x ('ops', 'B', 1, 80)
== batch 2: more employees (duplicates, NULL salary) ==
    delta -1 x ('eng', 'A', 1, 100)
    delta +1 x ('eng', 'A', 2, 100)
    delta -1 x ('ops', 'B', 1, 80)
    delta +1 x ('ops', 'B', 2, 160)
  view now:
    1 x ('eng', 'A', 2, 100)
    1 x ('ops', 'B', 2, 160)
== batch 3: retractions ==
  after delete e6:
    delta -1 x ('ops', 'B', 2, 160)
    delta +1 x ('ops', 'B', 1, 80)
  after delete e3 (last ops supporter):
    delta -1 x ('ops', 'B', 1, 80)
  after delete e1, e5:
    delta -1 x ('eng', 'A', 2, 100)
    delta +1 x ('eng', 'A', 1, NULL)
    delta -1 x ('eng', 'A', 1, NULL)
  view now:
    <empty>
== negative multiplicity is an error ==
    raised NegativeMultiplicityError: result: multiplicity of ('ghost', 'X', 1, 1) would become -1
== batch reorder converges to the same final view ==
    order (0, 1, 2, 3): final = [(('eng', 'A', 2, 100), 1)]
    order (1, 0, 2, 3): final = [(('eng', 'A', 2, 100), 1)]
    order (2, 1, 0, 3): final = [(('eng', 'A', 2, 100), 1)]
    all orders agree: True
```

## 测试结果（`python3.11 -m unittest -v`）

18 个测试全部通过，覆盖：三值逻辑真值表、过滤/投影多重数累加、连接重复行
重数相乘、撤回最后支持行输出删除、NULL 连接键永不匹配、NULL 分组键同组、
SUM 忽略/回落 NULL、分组生命周期、负重数/重复行 ID/未知行 ID 报错、批次
乱序最终结果一致。

```
Ran 18 tests in 0.001s

OK
```
