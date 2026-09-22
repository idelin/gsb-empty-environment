# plan-dag

把查询计划 DAG 序列化为**带类型与版本**的文档，反序列化时完成全部结构/类型检查，
再在本地内存关系表上执行。仅使用 Python 3.11 标准库，无任何 SQL 引擎，
无 `eval` / `exec` / pickle，未知算子一律拒绝，不能执行任意代码。

## 运行环境

- Python 3.11（在 3.11.15 上验证），仅标准库
- 测试框架：`unittest`

```bash
python3.11 -m unittest discover -s tests -v   # 运行测试
python3.11 demo.py                            # 运行样例
```

## 文档格式

```json
{
  "doc_type": "plan-dag",
  "version": 1,
  "root": "root",
  "nodes": [
    {"id": "s", "op": "scan", "version": 1, "inputs": [], "table": "orders"},
    {"id": "f", "op": "filter", "version": 1, "inputs": ["s"], "predicate": {"e": "bin", "op": ">", "left": {"e": "col", "name": "amount"}, "right": {"e": "lit", "value": 10, "type": "INT"}}}
  ]
}
```

- 文档信封带 `doc_type` + `version`；每个算子节点带 `op` + 节点级 `version`。
- 表达式是封闭的标签联合（`lit` / `col` / `bin` / `not` / `isnull` / `coalesce`），
  只接受 JSON 数据，不接受任何可执行字符串。

## 反序列化检查（全部发生在执行之前）

`from_document` / `from_json` 依次保证：

1. **版本与类型**：`doc_type`、文档 `version`、节点 `version` 不支持即拒绝。
2. **未知算子**：`op` 不在静态注册表
   `{scan, filter, project, hashjoin, aggregate, unionall}` 中即
   `UnknownOperatorError`；载荷里的任何 `code`/命令字符串都不会被执行。
3. **缺节点**：边指向不存在的 id、root 不存在 -> `MissingNodeError`。
4. **环**：Kahn 拓扑排序发现环（含自环）-> `CycleError`。
5. **同 ID 冲突**：同一 id 出现两次且内容不同 -> `NodeConflictError`；
   内容完全相同则去重接受。
6. **字段绑定**：列名不存在 -> `BindingError`；join 后重名列 ->
   `AmbiguousFieldError` 语义的绑定错误；未知 JSON 字段 -> `DocumentFormatError`。
7. **错列类型**：比较/算术/聚合/join key/union 分支类型不一致 ->
   `TypeMismatchError`，在读取任何表数据之前抛出。

## 语义

- **NULL（SQL 三值逻辑）**：单一 `NULL` 哨兵；比较含 NULL 得 NULL；
  `a AND b`、`a OR b`、`NOT` 按 3VL 真值表；`IS NULL` 恒为 TRUE/FALSE；
  `NULL` 无法被强制为 Python 布尔。NULL 字面量必须显式声明类型。
- **多重集合（bag）**：关系是行的有序列表，重复行保留；`unionall` 直接拼接；
  聚合按 bag 计数（`count(*)` 含 NULL 行，`count(col)` 忽略该列 NULL）；
  `sum/min/max` 忽略 NULL，空集得 NULL，`avg` 恒为 FLOAT 且空集得 NULL。
- **共享子节点**：执行器按节点 id memoize，一个节点即使被多个父节点引用，
  也只求值一次；序列化 -> 反序列化后引用关系保持共享。
- **关系表**：由 `Catalog`/`Table` 在进程内提供，建表时逐值校验类型，
  全程不调用任何 SQL 引擎。

## 代码结构

- `plan_dag/types.py`：类型、NULL、Schema、Relation（multiset）
- `plan_dag/catalog.py`：本地内存表
- `plan_dag/expr.py`：表达式代数与三值逻辑
- `plan_dag/nodes.py`：六个算子、绑定与执行
- `plan_dag/codec.py`：序列化、严格反序列化、环/缺失/冲突/类型检查
- `plan_dag/engine.py`：带 memoize 的执行器与统计
- `demo.py`：运行样例；`tests/`：62 个 unittest 用例

## 样例结果（摘录）

`python3.11 demo.py` 的结果：

```
customer:TEXT | orders:INT | total:INT
--------------------------------------
Ann | 1 | 100
Bo  | 2 | 100
Cy  | 1 | 70
```

- Ann 有一行 `amount = NULL`：`NULL >= 50` 为 NULL，行被过滤，故只有 1 单。
- Bo 有两行金额 50：重复行保留，订单数为 2。
- 共享的 `open_orders` 节点被两个聚合共用，求值统计中只出现 1 次。

非法文档的拒绝示例：

```
UnknownOperatorError: unknown operator 'os.system'; allowed: ['aggregate', ...]
CycleError: cycle detected involving nodes [...]
MissingNodeError: node 'open_orders' references missing input 'ghost'
NodeConflictError: node id 'scan_orders' appears multiple times with different content
TypeMismatchError: node 'open_orders' predicate: cannot compare TEXT with INT
BindingError: node 'open_orders' predicate.left: unknown column 'does_not_exist'
UnsupportedVersionError: unsupported document version 2; supported: 1
```

测试结果：

```
Ran 62 tests in 0.014s

OK
```
