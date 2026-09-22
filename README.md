# mini-SQL：EXISTS / 标量聚合关联子查询改写

一个纯 Python 3.11 标准库实现的迷你 SQL 执行器（无任何 SQL 引擎）。核心能力是在
**执行前**把关联子查询去关联（decorrelate）：

- `EXISTS (...)` 改写为 **哈希半连接（semi-join）**：无论内层匹配多少行，外层行
  都不会被复制。
- 标量聚合子查询 `(SELECT agg(...) ... WHERE 内层列 = 外层列 ...)` 改写为
  **分组连接（group-join）**：按关联键对内层预聚合，外层探测哈希表。

关系以本地多重集合（multiset，`list[tuple]`）表示，`NULL` 即 Python 的 `None`，
比较遵循 SQL 三值逻辑（3VL）。

## 目录结构

```
minisql/
  lexer.py     词法分析
  parser.py    递归下降语法分析，生成 AST
  engine.py    名字绑定、子查询去关联（半连接/分组连接）、关系执行
sample.py      可运行样例
tests/         unittest 行为测试（53 个）
```

## 运行

```bash
python3 sample.py                    # 打印 5 个改写语义样例
python3 -m unittest discover -s tests -v
```

## 支持的语法（子集）

```
query     := SELECT item_list FROM table_ref (, table_ref)*
             [ WHERE expr ] [ GROUP BY col (, col)* ]
item_list := * | item (, item)*
item      := expr [AS alias]
table_ref := name [alias]
expr      := literal | col | ( expr )
           | expr  ( + | - | * | / )            expr
           | expr  ( = | <> | != | < | <= | > | >= ) expr
           | [NOT] expr IS [NOT] NULL
           | NOT expr | expr AND expr | expr OR expr
           | COUNT(*) | COUNT|SUM|AVG|MIN|MAX ( expr )
           | EXISTS ( query ) | NOT EXISTS ( query )
           | ( SELECT agg-expr FROM ... [WHERE ...] )     # 标量聚合子查询
```

标量、布尔值（`TRUE`/`FALSE`）、单引号字符串（`''` 转义）均为字面量。

## 明确拒绝的语法

以下构造要么直接是语法错误（`SQLSyntaxError`），要么识别得到但超出子集
（`UnsupportedSQLError`），绝不会“静默产生错误结果”：

- `JOIN`（内/外连接）、`UNION/INTERSECT/EXCEPT`、`ORDER BY`、`LIMIT`、
  `HAVING`、`DISTINCT`（含聚合内）、子查询出现在 `FROM`、无 `FROM` 的查询；
- `IN`、`LIKE`、`CASE`、`||`、未知函数；
- 非等值关联（如 `e.sal > d.x`）、用 `OR` 关联、对列做表达式后再关联
  （如 `e.x + 0 = d.x`）、非“内层裸列 = 外层裸列”的关联；
- 子查询内再嵌套子查询、子查询内 `GROUP BY`、`EXISTS` 体内出现聚合、
  非聚合标量子查询（如 `(SELECT e.name ...)`）、聚合直接出现在 `WHERE`。

## 关键语义

- **半连接不去重外行**：`EXISTS` 用 `set` 存“存在的键”，外层多重集合逐行探测，
  因此一个部门有 100 个员工也只输出一次，但外层自身的重复行会被保留。
- **无匹配的分组空值**：分组连接对每个外层键查聚合结果，未命中的键使用“空组”
  聚合值：`COUNT → 0`，`SUM/AVG/MIN/MAX → NULL`；`NULL + 1 → NULL`。
- **`NULL` 关联永不匹配**：`NULL = NULL` 在 3VL 下不是 `TRUE`，键中任一列为
  `NULL` 即不进入半连接集合 / 分组连接字典；`IS NULL` 可显式匹配。
- **聚合对 `NULL` 参数的处理**：`COUNT(*)` 计所有行；`COUNT(x)` 跳过 `NULL`；
  `SUM/AVG/MIN/MAX` 忽略 `NULL`；`AVG` 除以非空计数，除零产生 `NULL`。
- **空输入**：无 `GROUP BY` 的聚合恒返回一行（`COUNT(*) = 0`）；有 `GROUP BY`
  时返回零行。
- **作用域遮蔽**：列名按“最近作用域优先”解析。裸列 `x` 在内层存在 `x` 时绑定
  内层；限定列 `o.x` 同样在最近的、标签为 `o` 的作用域中解析。因此两层都有别名
  `o` 时，内层的 `o` 会遮蔽外层，不会错绑层级。想引用外层必须能按限定名唯一定位。

## 本地建表

```python
from minisql import Database, Table, execute

db = Database([
    Table("dept", ("deptno", "dname"), [(10, "SALES"), (40, "OPS")]),
    Table("emp",  ("empno", "deptno", "sal"), [(1, 10, 100)]),
])
result = execute(db, """
    SELECT d.deptno,
           (SELECT COUNT(*) FROM emp e WHERE e.deptno = d.deptno) AS n
    FROM dept d
""")
print(result.columns, result.rows)
```
