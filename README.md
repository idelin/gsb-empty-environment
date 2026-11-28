# federate：联邦查询下推规划与多分片模拟执行

`federate` 是一个可直接导入的 Go 包，用来规划并执行跨远程数据分片的查询，同时提供一个“全量拉回单机求值”的参考执行器，用于逐行校验下推计划是否保持语义。

```go
import "prescreen.local/ps-523914d1f0/federate"
```

## 能力

- `TOP-K LIMIT/OFFSET` 修正下推：分片只接收 `ORDER BY k LIMIT (L+O)` 且 `OFFSET=0`，协调节点归并后再截取 `[O, O+L)`。
- 无 `ORDER BY` 的 `LIMIT/OFFSET`：按确定性分片顺序收集，并可安全下推 `L+O`，不会漏行。
- 分组聚合两阶段重写：
  - `GROUP BY` 覆盖分片键：单阶段完整聚合，`HAVING` 可下推。
  - 未覆盖分片键：分片 `PartialAgg` + 协调端 `FinalAgg`，`HAVING` 永不提前下推。
  - `COUNT->SUM`、`AVG->(SUM,COUNT)`、`MIN/MAX->MIN/MAX`。
  - 非分片键 `COUNT(DISTINCT x)` 以精确 distinct 集合上传后合并，绝不提前裁剪或合并。
- Co-located 等值连接下推：校验分片函数/映射、物理节点位置、连接等值键覆盖分片键、值波动性、外连接空值语义和字符串 `COLLATE`。
- 谓词按 `IMMUTABLE` / `STABLE` / `VOLATILE` 分级：`VOLATILE`（如 `random()`）绝不重复下推或跨连接重排。
- 支持 `Hash`、`Range`、`Replicated` 三种分布及多节点模拟。
- `Verify` 并排运行分布式计划与全量参考器，逐列逐行比较，返回首个差异。

## 快速使用

```go
schema := &federate.Schema{Columns: []federate.Column{
    {Name: "id", Type: federate.TypeInt},
    {Name: "grp", Type: federate.TypeString},
    {Name: "val", Type: federate.TypeInt},
}}

orders, _ := federate.NewTable("orders", schema,
    federate.HashDist([]string{"id"}, 4),
    []federate.Row{
        {Values: []federate.Value{federate.Int(1), federate.Str("a"), federate.Int(10)}},
        {Values: []federate.Value{federate.Int(2), federate.Str("b"), federate.Int(20)}},
        // ...
    })
cat, _ := federate.NewCatalog(orders)

q := federate.NewQuery("orders")
q.Select = []federate.SelectItem{
    federate.Select(federate.Col("id"), "id"),
    federate.Select(federate.Col("val"), "val"),
}
q.OrderBy = []federate.OrderItem{
    federate.Asc(federate.Col("val")),
    federate.Asc(federate.Col("id")),
}
q.Limit, q.Offset = 5, 10

plan, _ := federate.NewPlanner(cat).Plan(q)
// plan.TopK.ShardLimit == 15, plan.TopK.ShardOffset == 0

verification, err := federate.Verify(context.Background(), cat, q)
if err != nil { /* ... */ }
if !verification.Match {
    log.Fatal(verification.Diff)
}
```

## 下推边界

| 场景 | 计划行为 |
| --- | --- |
| `ORDER BY ... LIMIT L OFFSET O` | 每分片 `LIMIT L+O, OFFSET 0`，协调端全局归并后截取 |
| 无 `ORDER BY` 的 `LIMIT/OFFSET` | 按分片确定顺序收集；可安全下推 `L+O` |
| 存在未下推的 `WHERE`/`HAVING` | 禁止分片 `LIMIT`，避免过滤前裁掉行/组 |
| `GROUP BY` 覆盖分片键 | 单阶段聚合，`HAVING` 可下推 |
| `GROUP BY` 未覆盖分片键 | `PartialAgg` + `FinalAgg`，`HAVING` 仅在协调端 |
| 非分片键 `COUNT(DISTINCT x)` | 分片上传精确 distinct 集合，协调端并集 |
| 分片键 `COUNT(DISTINCT x)` | 可局部去重后 `SUM` |
| 两表相同分片函数与映射、连接键覆盖分片键、同节点、无 `VOLATILE` | 可下推为分片本地连接 |
| 异 `COLLATE` 或非二进制字符串等价 | 禁止连接下推 |
| 外连接保留侧为复制表、可空外连接键 | 保守禁止连接下推 |
| `VOLATILE` 谓词 | 只留在协调端，且阻止 Top-K 下推 |
| `STABLE` 谓词 | 可下推到表扫描；模拟执行使用同一语句快照 |

## 验证

测试覆盖 Hash/Range/Replicated 多分片表上的 Top-K、无排序分页、非分片键 `HAVING`、`AVG`/`COUNT(DISTINCT)` 两阶段、Co-located 与异映射连接、异 `COLLATE`、`VOLATILE`、外连接空值、别名排序、空表全局聚合等场景，均与参考执行器逐行一致。

```bash
go test ./...
go test -race ./...
```

## 说明

- `Query.Limit < 0` 表示无 `LIMIT`；`NewQuery` 会将 `Limit` 初始化为 `-1`。
- 查询模型支持一个主表和一个可选连接表；Range 分布当前使用单个区间键。
- `VOLATILE` 函数（例如 `random()`）在模拟器中使用稳定行标识派生确定性伪随机值，使参考执行器和分布式执行器可以在同一查询上逐行比较，同时规划器仍会拒绝其下推。
- `Stats` 记录分片扫描行数、网络传输行数/部分聚合组数和最终结果行数，可用于观察下推收益。
