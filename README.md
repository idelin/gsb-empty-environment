# ra：有限关系代数解释器与去相关改写器

演示查询优化器把相关子查询改写为连接时容易出错的两个经典问题——
**丢失无匹配的外层行**、**重复行被算多次**——并用可执行的方式验证改写正确性。

## 语义

- **bag 语义**：所有算子保留重复行；连接产生乘法重复。
- **三值逻辑**：`NULL` 参与的比较结果为 Unknown；`Filter`（WHERE）只保留 TRUE。
- **COUNT**：`COUNT(*)` 计行数；`COUNT(column)` 不计 NULL。
- **相关子查询**：`EXISTS` 与标量 `COUNT` 子查询可引用外层列（`outer`）。
- 连接谓词中，左列用原名或 `l.` 前缀，右列用原名（无冲突时）或 `r.` 前缀。

## 算子

`scan`、`project`、`filter`、`join`（`how: inner|left`）、`semijoin`（仅改写器生成）、
`groupby`（聚合目前仅 `count`）。

## 改写规则（去相关）

1. **EXISTS → 半连接**：`Filter(R, EXISTS(Filter(S, p)))` 中把 `p` 拆成
   相关等值（`outer.a = S.b`）与不引用外层的剩余谓词；剩余谓词下推到 S，
   等值条件变为半连接谓词。外层每行至多输出一次，保留外层重复度。
2. **标量 COUNT → 左连接 + GROUP BY**：先按内层键 `GROUP BY` 预聚合，
   再与外层左连接，SELECT 列表中的子查询替换为 `COALESCE(cnt, 0)`。
   左连接保留外层行多重性，未匹配行得到 `COUNT = 0`。

**明确拒绝**（报错并说明原因，不静默给出错误计划）：

- 相关条件不是外层键等值（如 `S.w > R.v`、`outer.k = COALESCE(S.k,0)`）；
- `NOT EXISTS`；
- WHERE 中嵌套的标量子查询（仅支持 SELECT 列表中的标量 COUNT）。

## 运行

```sh
go test ./...                 # 单元测试 + 有限域穷举性质测试
go run ./cmd/ra examples/count.json   # JSON 计划 CLI（参数为文件，缺省读 stdin）
```

CLI 输出：原计划、改写计划、适用条件（或拒绝原因）、两个计划的执行结果、
以及 `results_equal`（按行多重集比较）。

## 测试

`rewrite_test.go` 在含 NULL 的有限小域（键 ∈ {NULL,1,2}）上枚举两表全部
大小 ≤2 的行多重集（约 1.5 万组合），对 7 个查询变体逐组合比较多重集：

- 空表、重复键、NULL 键（NULL = NULL 不成立，计数为 0）；
- 带/不带剩余谓词（下推到内层）的 EXISTS 与 COUNT；
- `COUNT(*)` 与 `COUNT(w)`（NULL 不计）；
- 两事实表 `S⋈T` 上的 COUNT：连接乘法重复必须原样保留。

`interp_test.go` 覆盖三值逻辑、WHERE 只留 TRUE、左连接补 NULL、
连接条件在 ON 与 WHERE 的区别、bag 乘法重复。

## JSON 计划格式

```jsonc
{
  "tables": {"R": [{"k": 1, "v": 10}, {"k": null, "v": 30}]},
  "plan": {"op": "project", "input": {"op": "scan", "table": "R"},
    "exprs": [{"expr": {"kind": "count", "col": "*", "plan": {
        "op": "filter", "input": {"op": "scan", "table": "S"},
        "pred": {"kind": "cmp", "op": "eq",
                 "l": {"kind": "outer", "name": "k"},
                 "r": {"kind": "col", "name": "k"}}}},
      "as": "c"}]}
}
```

表达式 `kind`：`lit`、`col`、`outer`、`and`、`or`、`not`、
`cmp`（`op`: eq/ne/lt/le/gt/ge）、`isnull`、`coalesce`、`exists`、`count`。
