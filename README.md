# vector_expr — 编译为向量执行计划的表达式引擎

纯 Python 3.11 标准库实现（测试用 `unittest`），不依赖任何现成查询引擎。
表达式先**编译**成类型化的向量执行计划，再对**带列类型的批次**执行；
所有类型错误在执行前报告。

## 功能与语义

- **表达式种类**：算术 `+ - * / %`、比较 `= != < <= > >=`、逻辑
  `AND OR NOT`（Kleene 三值逻辑）、条件 `if(cond, a, b)`、显式转换
  `CAST(x AS INT64|FLOAT64)`。
- **编译期类型检查**：`1 + true`、`if(1,2,3)`、INT64 与 FLOAT64 混合运算
  等都在编译时报错（带源码位置指示），不会触碰任何数据。
- **短路条件**：`if` 按行掩码向量化求值——then 分支只在条件为 TRUE 的行
  上求值，else 分支只在 FALSE 的行上求值，因此未选分支的除零不会触发：
  `if(false, 1/0, 7)` 结果为 `7`。
- **null 与 NaN 不同**：null 用 `None` 表示缺失；NaN 是 FLOAT64 的普通值。
  `null = null` 的结果是 **unknown（null）而非 false**；`NaN = NaN` 为
  `false`（IEEE-754）。
- **整数精确计算**：INT64 范围内精确运算；溢出（含 `INT64_MIN / -1`）
  按错误策略抛出 `IntegerOverflowError` 并**报告具体行号**。整数除法向零
  取整，除零抛 `DivisionByZeroError`（含行号）；浮点除零遵循 IEEE
  （`±inf` / `NaN`）。
- **显式转换**：`CAST` 支持 INT64↔FLOAT64；FLOAT64→INT64 向零截断，
  NaN 输入报 `CastError`、超范围报 `IntegerOverflowError`，均含行号。

## 目录结构

```
vector_expr/
  parser.py      # 手写递归下降解析器 -> 无类型 AST
  compiler.py    # 类型检查 + 生成计划（执行前报类型错误）
  plan.py        # 向量化执行节点（按行掩码求值，实现短路）
  batch.py       # Batch/Column：带列类型的批次输入
  types.py       # INT64 / FLOAT64 / BOOL
  errors.py      # CompileError 与含行号的 EvaluationError 家族
tests/test_vector_expr.py
examples/demo.py
```

## 用法

```python
from vector_expr import Batch, Column, Type, compile_expression

schema = {"salary": Type.INT64, "active": Type.BOOL}
plan = compile_expression("if(active, salary / 2, 0)", schema)  # 编译期类型检查

batch = Batch({
    "salary": Column(Type.INT64, [1000, 2500, None]),
    "active": Column(Type.BOOL, [True, False, True]),
})
print(plan.execute(batch).values)  # [500, 0, None]
```

## 运行样例

```
$ python3.11 examples/demo.py
== input batch ==
  id        INT64    [1, 2, 3, 4]
  salary    INT64    [1000, 2500, None, 4000]
  bonus_pct FLOAT64  [0.1, nan, 0.25, None]
  active    BOOL     [True, False, True, None]

== arithmetic / comparison / logic ==
expr: salary + 500
  type:   INT64
  values: [1500, 3000, None, 4500]
expr: salary * 2 > 3000
  type:   BOOL
  values: [False, True, None, True]
expr: active AND salary > 1500
  type:   BOOL
  values: [False, False, None, None]
expr: CAST(salary AS FLOAT64) * bonus_pct
  type:   FLOAT64
  values: [100.0, nan, None, None]

== null vs NaN (row 1 bonus is NaN, row 3 bonus is null) ==
expr: bonus_pct = bonus_pct
  type:   BOOL
  values: [True, False, True, None]

== short-circuit IF ==
expr: if(false, 1/0, 7)
  type:   INT64
  values: [7, 7, 7, 7]
expr: if(active, salary / 2, 0)
  type:   INT64
  values: [500, 0, None, None]

== errors ==
compile-time type error (before any execution):
  cannot mix INT64 and FLOAT64 in '+'; add an explicit CAST
  salary + 0.5
         ^

integer overflow reported with row:
  row 1: 9223372036854775807 + 1 = 9223372036854775808 overflows INT64

division by zero only fires on selected rows:
  row 1: integer division by zero (100 / 0)

NaN -> INT64 cast error with row:
  row 1: cannot CAST NaN to INT64
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests
.............................................
----------------------------------------------------------------------
Ran 45 tests in 0.016s

OK
```

覆盖：整数精确运算与溢出（含行号）、除零（含行号）、向零取整除法/取模、
IEEE 浮点除零、null 传播、三值逻辑（`FALSE AND NULL = FALSE`、
`TRUE AND NULL = NULL` 等）、null 比较得 unknown、NaN≠null、
`if` 短路（含逐行掩码与选中分支报错行号）、显式 CAST 及其错误、
全部编译期类型错误、批次模式校验、计划跨批次复用。
