# ra：有限关系代数解释器与相关子查询改写器

演示查询优化器把相关子查询（EXISTS / 标量 COUNT）去关联化为连接，同时保证：

- **bag 语义**：所有算子保留重复行；改写不改变外层行的重复度；
- **SQL 三值逻辑**：`NULL` 参与的比较得 `unknown`，`WHERE`/`ON` 只保留 `true`；
- **COUNT 语义**：`COUNT(*)` 计行数，`COUNT(col)` 不计 `NULL`；无匹配时标量 COUNT 为 0。

## 算子

| op | 说明 |
|---|---|
| `scan` | 读表，列名限定为 `表.列` |
| `filter` | 只保留谓词为 `true` 的行 |
| `project` | 表达式求值 + 重命名（标量 COUNT 子查询只能出现在这里） |
| `join` (`inner`/`left`) | 嵌套循环连接，ON 只认 `true`；左连接未匹配补 NULL |
| `semijoin` | 改写产物：EXISTS 的去关联化结果，每个外层行最多保留一次 |
| `aggregate` | 改写产物：按关联键预聚合 `count(*)` / `count(col)` |

表达式：`col`、`lit`、`cmp`(eq/ne/lt/le/gt/ge)、`and`/`or`/`not`、`isnull`、`coalesce`、`exists`、`count`（标量子查询）。

## 改写规则与适用条件

- `EXISTS (SELECT ... FROM S WHERE S.k = R.k AND <内层条件>)` → **半连接**；
  内层条件下推到半连接右侧。
- 投影中的标量 `COUNT` → 先按内层关联键 `aggregate`，再 **LEFT JOIN**，
  输出列替换为 `COALESCE(agg.cnt, 0)`。聚合键唯一 ⇒ 左连接不增不减外层行；
  两个事实表各做一次聚合再分别连接，避免乘法重复计数。
- 子查询只支持 `FILTER over SCAN` 形态；相关条件只允许 `内层列 = 外层列` 等值。
- 明确拒绝：非等值相关、OR/NOT 之下的子查询、谓词或表达式内嵌套的 COUNT、
  只引用外层列的谓词、子查询内嵌套子查询、自相关（需别名）。

## 运行

```sh
go test ./...                 # 单元测试 + 有限域穷举等价性验证
go run . -f testdata/example.json   # 打印原计划、改写计划、适用条件、两边结果并比较多重集
go run . -f testdata/exists.json
go run . -f testdata/rejected.json  # 演示明确拒绝改写
```

输入 JSON：`{"tables": {"R": {"cols": [...], "rows": [[...]]}}, "plan": {...}}`；
值只能是 `null`、整数、字符串。

## 测试覆盖

- 空表、重复键、NULL 等值不成立（NULL 键永远不匹配，COUNT=0）；
- `COUNT(*)` vs `COUNT(col)` 的 NULL 处理；
- ON 与 WHERE 中过滤条件对 LEFT JOIN 结果的区别；
- 两个事实表分别计数，验证不发生乘法重复；
- 在 `{NULL,0,1}` 域上穷举两表（含重复行的多重集，规模 ≤2）及三表组合，
  对 6 种计划形态逐例比较原计划与改写计划的行多重集。
