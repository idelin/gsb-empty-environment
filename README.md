# vecplan — 向量化表达式引擎

用 Python 3.11 标准库实现（无第三方依赖，不调用现成查询引擎）。把算术、
比较、条件和显式转换表达式**编译**为向量执行计划：编译期完成类型检查，
执行期按列批次（RecordBatch）求值。

## 目录结构

```
vecplan/
  types.py      # 逻辑类型 BOOL / INT32 / INT64 / FLOAT64 / STRING，类型提升
  errors.py     # CompileError / ExecutionError / ErrorPolicy(STRICT|NULL)
  expr.py       # 表达式 AST：col/lit/if_/cast/and_/or_/not_/is_null + 运算符重载
  batch.py      # RecordBatch：带列类型的批次输入，校验值类型与整数范围
  compiler.py   # 类型检查 + 下推为执行计划（执行前报告所有类型错误）
  plan.py       # 向量化执行计划：Literal/Column/Binary/Not/IsNull/If/Cast
examples/demo.py    # 运行样例
tests/test_vecplan.py  # 40 个 unittest 用例
```

## 语义要点

- **编译期类型检查**：`compile(expr, schema)` 在执行前报告所有类型错误
  （`CompileError`），包括永远不会被执行的分支里的错误。
- **短路条件**：`IfPlan` 按条件把行分成选择向量，每个分支只在选中它的行
  上求值；未选分支完全不执行。因此 `if(false, 1/0, 7)` 结果为 `7`，
  条件为 null 的行两个分支都不执行，结果为 null。
- **null 与 NaN 不同**：null 用 `None` 表示；NaN 是普通的 FLOAT64 值，
  `is_null(NaN)` 为 false。NaN 比较遵循 IEEE 语义（`NaN != NaN`）。
- **三值逻辑**：null 参与的比较结果是 unknown（`None`）而不是 false；
  `and`/`or`/`not` 按 SQL 三值逻辑传播 unknown。
- **整数精确计算**：整数运算在任意精度上完成后按结果类型（INT32/INT64）
  边界检查溢出；整数除法/取模向零截断。
- **错误策略**：溢出、除零、非法转换按 `ErrorPolicy` 处理——
  `STRICT` 抛出 `ExecutionError` 并在消息和 `.rows` 中报告具体行号；
  `NULL` 把出错行置为 null 继续执行。

## 用法

```python
from vecplan import INT32, RecordBatch, col, compile, if_, lit

schema = {"a": INT32, "b": INT32}
batch = RecordBatch(schema, {"a": [10, 20, 30], "b": [2, 0, 5]})

plan = compile(if_(col("b") != 0, col("a") / col("b"), lit(-1)), schema)
plan.run(batch).values          # [5, -1, 6] —— b=0 的行不会触发除零

compile(if_(lit(False), lit(1) / lit(0), lit(7)), schema).run(batch).values
                                # [7, 7, 7]
```

## 运行样例

```
$ python3.11 examples/demo.py
input batch (typed columns):
  a INT32: [10, 20, None, 2147483647]
  b INT32: [3, 0, 5, 0]
  f FLOAT64: [1.5, nan, None, 2.5]
  s STRING: ['x', 'y', 'z', 'w']

a + b                                  -> [13, 20, None, 2147483647]
if(b != 0, a / b, -1)                  -> [3, -1, None, -1]
if(false, 1 / 0, 7)                    -> [7, 7, 7, 7]
strict a + a raises: integer overflow at row(s) [3] (rows=[3])
a + a with ErrorPolicy.NULL            -> [20, 40, None, None]
a = 10  (null -> unknown)              -> [True, False, None, False]
is_null(f)  (NaN is not null)          -> [False, False, True, False]
f = f  (NaN != NaN)                    -> [True, False, None, True]
int64: (2^53 + 1) + 1                  -> [9007199254740994]
strict cast(f as INT32) raises: invalid cast at row(s) [1] (rows=[1])
cast(f as INT32), NULL policy          -> [1, None, None, 2]
cast(a as STRING)                      -> ['10', '20', None, '2147483647']
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests
........................................
----------------------------------------------------------------------
Ran 40 tests in 0.007s

OK
```

覆盖：编译期类型错误（9 例）、短路求值（5 例）、溢出与错误策略（6 例）、
null/NaN 三值逻辑（6 例）、显式转换（7 例）、批次校验（5 例）、
端到端向量化查询（2 例）。
