# plan_dag — 带类型与版本的查询计划 DAG

纯 Python 3.11 标准库实现（无第三方依赖、**不调用任何 SQL 引擎**）。支持：

1. 将查询计划 DAG 序列化为**带类型标签**（`@type`）与**格式版本**（`format_version: "1.0"`）的 JSON 文档；
2. 反序列化时做严格结构校验：**环**、**缺节点**、算子/表达式**输入个数**、未知键；
3. 执行前做**字段与类型绑定**：缺列、列歧义、比较/算术/聚合的类型错配全部在执行前发现；
4. **未知算子/表达式拒绝**：标签只经显式白名单解析，代码中没有 `eval` / `exec` / 动态导入 / pickle，无法执行任意代码；
5. 合成关系表由本地内存 `Catalog` 提供，关系是**多重集合（bag）**，重复行保留；
6. **NULL 语义明确**：比较与布尔运算遵循 SQL 三值逻辑；连接键 NULL 永不匹配；左外连接补 NULL；聚合 `count`/`sum`/`avg` 忽略 NULL，`count_star` 统计所有行；
7. **共享子节点**：执行按拓扑序记忆化，一个节点只执行一次；把分片文档按相同节点 ID 合并“恢复”DAG 后，相同 ID 只保留一份，共享关系保持不变；
8. **同 ID 不同内容冲突**：`find_id_conflicts` 检出、`merge_documents` 拒绝。

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 测试
python3.11 demo.py                            # 端到端样例（8 个场景）
```

已保存的输出：`docs/test_output.txt`、`docs/demo_output.txt`。

## 文档格式

```json
{
  "format_version": "1.0",
  "nodes": [
    {"id": "emp", "op": {"@type": "scan", "table": "employees"}, "inputs": []},
    {"id": "hi",
     "op": {"@type": "filter",
            "predicate": {"@type": "cmp", "op": "gt",
                          "left": {"@type": "column_ref", "name": "salary"},
                          "right": {"@type": "literal", "value": 60}}},
     "inputs": ["emp"]}
  ],
  "roots": ["hi"]
}
```

版本规则：主版本号不同直接拒绝（`FormatVersionError`）；同主版本较新次版本可读取。

## 代码结构

- `plan_dag/errors.py` — 错误层级（文档错误 / 校验错误 / 执行错误）。
- `plan_dag/datatypes.py` — `INTEGER/DOUBLE/TEXT/BOOLEAN/NULL`、`Field`、`Schema`。
- `plan_dag/tags.py` — 标签白名单注册表与严格字段检查（安全边界所在）。
- `plan_dag/expr.py` — 表达式树：`column_ref/literal/cmp/and/or/not/arith/is_null/coalesce`，绑定期做类型检查。
- `plan_dag/operators.py` — 算子：`scan/filter/project/join(inner,left_outer)/aggregate(count_star,count,sum,avg,min,max)/limit`。
- `plan_dag/plan.py` / `runtime.py` — DAG 结构、DFS 拓扑排序（环检测）、绑定驱动与已绑定计划。
- `plan_dag/serde.py` — 版本化文档 (反) 序列化、同 ID 内容冲突检测、分片合并。
- `plan_dag/executor.py` — 本地内存执行器（共享节点记忆化）。
- `plan_dag/catalog.py` / `sample_data.py` / `builders.py` — 本地关系表、样例数据与计划构建器。
- `tests/` — 三组 `unittest`：序列化安全、校验、执行语义。
