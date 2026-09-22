# Pair-wise Mini SQL

Python 3.11 标准库实现的内存关系查询器，不调用任何 SQL 引擎。它支持一个刻意收窄的 SQL 子集，并把：

- `EXISTS` / `NOT EXISTS` 相关子查询改写为 `SemiJoin` / `AntiJoin`。
- 标量聚合相关子查询改写为 `GroupJoin`，每个外行只输出一个聚合标记值。

## 运行

```bash
python3.11 -m mini_sql.demo
python3.11 -m mini_sql examples/scalar_aggregates.sql
python3.11 -m mini_sql examples/exists_customers.sql --explain
python3.11 -m unittest discover -s tests -v
```

## 支持的语法

```sql
SELECT item [, item ...] | table.* | *
FROM table [AS alias]
[WHERE predicate [AND predicate ...]]
```

谓词和投影表达式支持：

- 字面量、列引用、括号、一元 `-` / `NOT`。
- `AND`、`OR`、`=`、`<>` / `!=`、`<`、`<=`、`>`、`>=`。
- `IS NULL`、`IS NOT NULL`、`+`、`-`、`*`、`/`。
- `WHERE EXISTS (SELECT * FROM ...)` 和 `WHERE NOT EXISTS (...)`，作为顶层 `AND` 合取项。
- 投影列表中的 `(SELECT COUNT|SUM|AVG|MIN|MAX(...) FROM ... WHERE ...)`。

## 明确拒绝

以下语法直接抛出 `UnsupportedSQL`：`JOIN`、`IN`、`LIKE`、`BETWEEN`、`DISTINCT`、`GROUP BY`、`HAVING`、`ORDER BY`、`LIMIT`、集合运算、非聚合标量子查询、嵌套子查询、多个表、聚合 `DISTINCT`、`EXISTS` 出现在 `OR` 中，以及聚合在外层查询中直接使用。

## 语义

- 关系是多重集合：基表和中间结果保留重复行。
- `SemiJoin` 只判断是否存在，不因为多个匹配而复制外行。
- `AntiJoin` 只保留无匹配外行；相关条件为 `FALSE` 或 `NULL` 都不算匹配。
- `COUNT(*)` 统计匹配行数；无匹配为 `0`。
- `COUNT(expr)` 忽略表达式值为 `NULL` 的匹配行；无匹配为 `0`。
- `SUM(expr)`、`AVG(expr)`、`MIN(expr)`、`MAX(expr)` 忽略 `NULL`；无匹配为 `NULL`。
- 比较和算术中的 `NULL` 按三值逻辑传播；`WHERE` 只接受确定的 `TRUE`。
- 未限定列从最近作用域向外层解析，内层列会遮蔽外层同名列；限定列严格绑定对应范围变量。

## 代码结构

- `mini_sql/parser.py`：词法、递归下降解析和 AST。
- `mini_sql/planner.py`：作用域绑定、相关谓词分类和子查询改写。
- `mini_sql/runtime.py`：三值逻辑、关系算子、聚合和逻辑计划执行。
- `mini_sql/sample_data.py`：本地合成关系表。
- `tests/test_mini_sql.py`：语义和拒绝语法的 `unittest`。
