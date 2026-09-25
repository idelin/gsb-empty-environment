# 多聚合查询的公共中间项去重执行器

纯 Python 3.11 标准库实现，不依赖任何现成查询引擎。多个聚合查询共享一张
表达式 DAG：相同的纯表达式（含聚合）只计算一次，副作用函数禁止复用，
并输出执行依赖图与每个节点的实际计算次数。

## 文件

- `ir.py` — 带类型的表达式 IR。`Builder` 对纯节点做哈希 consing（结构相同的
  表达式共享同一节点）；`pure=False` 的函数调用每次新建节点，不参与共享。
- `exec_engine.py` — 批式执行引擎。`Batch` 携带列类型并校验值；`Engine.run`
  按拓扑序逐批执行 DAG，聚合用可合并的部分状态跨批合并；`Counts` 记录每个
  节点的计算次数；`render_plan` 输出依赖图与计数。
- `demo.py` — 运行样例：`python3.11 demo.py`
- `test_engine.py` — 25 个 unittest：`python3.11 -m unittest test_engine -v`

## 语义约定

- **null 与 NaN 不同**：`None` 表示 null（SQL 三值逻辑，聚合跳过，过滤谓词
  为 null 的行被排除）；NaN 是普通浮点值（计入 count，使 sum 结果为 NaN，
  `is_null` 为 False、`is_nan` 为 True）。
- **整数精确计算**：int 列的 sum/avg 用 Python 整数与 `Fraction`，无精度
  损失；`sum(x)`（int）与 `sum(cast(x) AS float)`（float）是不同节点，
  不会错误合并，大整数下结果可区分（见
  `DedupTest.test_int_exact_where_float_loses_precision`）。
- **过滤差异保留**：filter 是聚合节点结构的一部分，仅 null 处理不同的两个
  过滤条件生成不同聚合节点，分别计算。
- **副作用禁止复用**：`pure=False` 的函数节点不 intern，出现两次就执行两次
  （demo 中 `trace` 调用 14 次 = 2 节点 × 7 行）。
- **去重执行**：`sum(x)` 被 Q1/Q2/Q3 三处引用，图中只有节点 N2，每批计算
  一次（`evals=2` 对应 2 个批次），三个查询共享同一聚合状态。

## 计数口径

- `evals`：该节点被执行的批次数（每批至多一次）。
- `row_ops`：该节点累计处理的行数（列读取与字面量记 0）。
