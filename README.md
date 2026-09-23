# Python 参数化查询计划缓存示例

这是一个只使用 Python 3.11 标准库的本地关系执行器，不调用 SQL 引擎或外部数据库。项目通过谓词表达式 AST 模拟参数化查询的“绑定后化简、选择计划、执行”流程。

## 设计要点

- **参数绑定后化简**：每次执行先用当前参数折叠空值、类型不兼容、统计范围可证明的常量谓词。
- **计划不固化首参数值**：缓存计划中的访问条件保存 `param(index)`，执行时重新读取当前参数。
- **缓存键**：包含查询形状、目录统计版本、每个参数的逻辑类型，以及列比较中参数相对统计区间的范围类别，不包含具体参数值。
- **计划选择**：低选择度范围使用 `INDEX_RANGE_SCAN`，高选择度或非 SARG 谓词使用 `FULL_SCAN`，可证明无结果时使用 `EMPTY_RESULT`。
- **NULL 语义**：谓词采用三值逻辑：`true`、`false`、`unknown`；`WHERE` 只接受 `true`。
- **多重集合语义**：表是行的 multiset，重复行不被 `DISTINCT` 去重，索引扫描保留重复行和原始顺序。
- **统计失效**：`Catalog.replace_table()` 递增统计版本，下次执行清空旧计划并把失效计入缓存统计。

范围类别包括 `null`、`incompatible`、`empty_domain`、`below_min`、`within_low`、`within_mid`、`within_high`、`above_max`。因此同一逻辑类型但统计区间类别不同的参数会重新选择计划。

## 运行

```bash
python3.11 run_sample.py
python3.11 -m unittest discover -s tests -v
```

## 文件

- `miniplanner/expr.py`：参数、列、比较和布尔谓词 AST。
- `miniplanner/catalog.py`：本地 multiset 表、排序索引和列统计。
- `miniplanner/evaluator.py`：三值逻辑求值和参数绑定后的谓词化简。
- `miniplanner/planner.py`：缓存键、计划选择、缓存失效和执行器。
- `run_sample.py`：可运行演示。
- `tests/test_planner.py`：`unittest` 覆盖。
