# 分区排序窗口引擎（SUM / RANK，ROWS / RANGE 帧）

纯 Python 3.11 标准库实现，不依赖任何查询引擎。表按**带列类型的批次**
输入，分区可横跨任意多个批次——`compute()` 统一计算，分块边界不会截断分区。

## 语义要点

- **peer 组**：重复排序值互为 peer。RANK 同组同名次并跳名次：键 `1,1,2`
  的 rank 为 `1,1,3`。
- **RANGE vs ROWS**：`RANGE ... CURRENT ROW` 的帧端点落在 peer 组边界上
  （两个键 1 都进入帧）；`ROWS CURRENT ROW` 只含当前行自身。
- **NULL 排序**：`OrderKey(nulls_first=...)` 显式控制；缺省按 PostgreSQL
  惯例（升序 NULLS LAST，降序 NULLS FIRST）。NULL 与 NULL 互为 peer。
- **NULL ≠ NaN**：`None` 是 NULL，不参与 SUM（全 NULL 帧结果为 `None`）；
  `float("nan")` 是普通值，排在所有非 NULL 数值之后、参与求和并使结果
  为 NaN，NaN 与 NaN 互为 peer。
- **整数精确**：int 列求和使用 Python 任意精度整数，无溢出。
- **类型校验**：批次按 schema（`int` / `float` / `str`）逐格校验，
  不符抛 `SchemaError`；非法窗口定义抛 `WindowSpecError`。

## 用法

```python
from window import WindowEngine, OrderKey, Frame, UNBOUNDED_PRECEDING, CURRENT_ROW

engine = WindowEngine(
    schema=[("grp", "str"), ("k", "int"), ("v", "int")],
    partition_by=["grp"],
    order_by=[OrderKey("k")],                      # 可加 descending / nulls_first
    sum_column="v",
    frame=Frame("range", UNBOUNDED_PRECEDING, CURRENT_ROW),
)
engine.add_batch([("a", 1, 10), ("a", 1, 20)])     # 批次 1
engine.add_batch([("a", 2, 30)])                   # 批次 2，同一分区跨批次
for row in engine.compute():
    print(row)   # {'row':..., 'sum':..., 'rank':...}，按输入顺序
```

帧构造：`Frame("rows"|"range", start, end)`，边界取 `UNBOUNDED_PRECEDING`、
`PRECEDING(n)`、`CURRENT_ROW`、`FOLLOWING(n)`、`UNBOUNDED_FOLLOWING`。
RANGE 偏移帧（如 `RANGE BETWEEN 1 PRECEDING AND CURRENT ROW`）要求单一
升序数值排序键，按排序键取值界定帧；NULL/NaN 键的行只与自己的 peer 同帧。

## 运行样例（`python3.11 example.py`）

```
== RANGE UNBOUNDED PRECEDING .. CURRENT ROW（分区跨批次） ==
row                                  sum  rank
('a', 1, 10)                          30     1
('b', 1, 100)                        100     1
('a', 1, 20)                          30     1
('a', 2, 30)                          60     3
('b', 2, 200)                        300     2

== ROWS CURRENT ROW（帧只含自身） ==
row                                  sum  rank
('a', 1, 10)                          10     1
('a', 1, 20)                          20     1
('a', 2, 30)                          30     3

== NULL vs NaN（nulls_first=False） ==
row                                  sum  rank
('a', None, 1.0)                     7.0     4
('a', nan, 2.0)                      6.0     3
('a', 1.0, None)                    None     1
('a', 2.0, 4.0)                      4.0     2

== ROWS 1 PRECEDING .. CURRENT ROW（大整数精确求和） ==
row                                  sum  rank
('a', 1, 1208925819614629174718521)1208925819614629174718521     1
('a', 2, 1208925819614629174718521)2417851639229258349437042     2
('a', 3, 7)                 1208925819614629174718528     3
```

## 测试结果（`python3.11 -m unittest`）

```
................................
----------------------------------------------------------------------
Ran 32 tests in 0.001s

OK
```

覆盖：rank peer 跳名次（1,1,3）、ROWS/RANGE 当前行帧差异、分区跨批次、
NULL 显式/默认排序、NULL 跳过求和与全 NULL 帧、NaN 排序/求和/peer、
大整数精确求和、ROWS 偏移帧与空帧、RANGE 数值偏移帧、类型与窗口定义校验。

## 文件

- `window.py` — 引擎实现
- `test_window.py` — unittest 测试（32 个）
- `example.py` — 运行样例
