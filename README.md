# viewopt

`viewopt` 是一个可导入的 Go 包，用于把多张相互重叠的物化视图组合成一条
`GROUP BY` 查询的替代读取路径，并对该路径做**对所有底层多重集都成立**的
等价性验证。若不存在等价计划，包会给出最小反例底表。

```go
import viewopt "prescreen.local/ps-9e7567910b"
```

## 数据模型

- `Row`：三列可空整数分组键、一个整数过滤坐标 `Filter`、一个可空整数度量
  `Measure`。任意整数线性过滤表达式都可以先计算到 `Filter` 上。
- `View`：`Dims` 是分组键投影（0..2 的子集），`Filter` 是整数闭区间，
  `ReadCost` 是一次读取费用。每个视图保留
  `SUM(Measure)`、`COUNT(*)`、`COUNT(Measure)`。
- `Query`：直接查询的 `Dims`、`Filter` 和可选 `Having`。
- `Plan`：对若干视图行乘以整数系数后相加，再投影合并到查询分组。
  `AVG` 不是计划项，只能由合并后的 `SUM / COUNT(非空)` 派生。

计划中被使用的视图必须按查询分组的**超集**分组；比查询更粗的视图无法拆回
查询键，优化器不会使用它们，`VerifyPlan` 也会拒绝。

NULL 分组键是独立分组；SQL 空输入的 `SUM` 是 `NULL`，不是 `0`。
视图缺组表示该视图中没有这个组，不会被当成一条计数为 0 的视图行。
`Agg.Avg()` 返回 `(float64, bool)`，第二个值为 `false` 表示 `AVG` 为 NULL。
本包按分组查询语义解释 `Query`：只有至少一条匹配行时才产生结果组；若需要
“无 GROUP BY 的全局聚合在空表上仍返回一行”的语义，请在调用方自行处理。

## 使用

```go
q := viewopt.Query{
    Dims:   []int{0},
    Filter: viewopt.Between(0, 1_000_000),
}

views := []viewopt.View{
    {Name: "left",  Dims: []int{0}, Filter: viewopt.Between(0, 500_000), ReadCost: 4},
    {Name: "right", Dims: []int{0}, Filter: viewopt.Between(500_001, 1_000_000), ReadCost: 3},
    {Name: "whole", Dims: []int{0}, Filter: viewopt.Between(0, 1_000_000), ReadCost: 20},
}

sol, err := viewopt.Optimize(viewopt.Problem{
    Views: views,
    Query: q,
    Options: viewopt.DefaultOptions(), // 系数 |c| <= 2，域 [0, 2^40]
})
if err != nil { panic(err) }
if !sol.Equivalent {
    fmt.Println(sol.Reason, sol.Counterexample.Table)
} else {
    fmt.Println(sol.Plan.Terms, sol.Cost)
}
```

验证一个已有计划：

```go
ver, err := viewopt.VerifyPlan(q, views, plan, viewopt.DefaultOptions())
if err != nil { panic(err) }
if !ver.Equivalent {
    // ver.Counterexample.Table 已按“行数最少、再按元组序最小”选出
    fmt.Println(ver.Reason, ver.Counterexample.Table)
}
```

独立解释器：

```go
direct, _ := viewopt.EvaluateQuery(q, table)          // 直接 GROUP BY
actual, _ := viewopt.EvaluatePlan(q, views, plan, table)
same := direct.Equal(actual)                          // 包含组存在性
```

小域全表穷举（与符号检查器相互独立）：

```go
domain := viewopt.Domain{
    Keys:     []viewopt.Key{viewopt.K(), viewopt.K(viewopt.Int(0))},
    Filters:  []int64{0, 1, 5, 6},
    Measures: []viewopt.Value{viewopt.Null(), viewopt.Int(0), viewopt.Int(1)},
}
ok, witness, err := viewopt.ExhaustiveVerify(q, views, plan, domain,
    viewopt.EnumerateOptions{MaxMultiplicity: 1, MaxTotalRows: 3})
```

## 等价性判定

对 `Options.Domain` 中每个过滤值 `f`，计划满足：

1. 每个被使用视图的分组维度覆盖查询分组维度；
2. 所有覆盖 `f` 的视图系数和等于 `1`（`f` 在查询区间内）或 `0`（区间外）。

区间端点把所有 `f` 划成有限个单元格，因此范围可达 `2^40` 也不会逐值枚举。
条件 1 保证不会把粗粒度组错误拆分；条件 2 是逐行的，所以对任意键值、NULL
分组、任意度量与任意重数都成立。`SUM` 的可空性由合并后的非空计数决定，
`AVG` 永远在最后派生，不会被当作线性量相加。

如果计划使用了查询区间之外的视图区域，即使那里系数和为 0，也可能产生一个
`COUNT=0, SUM=NULL` 的额外空组。该空组会被视为不等价；唯一的例外是
`HAVING` 对空组状态返回 `false`（例如 `HAVING COUNT(*) > 0`），此时它不可见。

优化器在 `[-MaxAbsCoeff, MaxAbsCoeff]` 内搜索整数系数。默认读取费用是
“每个被使用（系数非零）的视图读取一次”，即 `sum ReadCost_v`；若要把系数
幅度解释为重复扫描，可设置 `Options.WeightCostByCoefficient`，此时费用为
`sum |c_v| * ReadCost_v`。负系数用于减去重叠部分。视图数不超过 10，超过
阈值时使用折半枚举（meet-in-the-middle）。搜索本身只依赖区间单元格，不依赖
底表大小。

## 反例

`VerifyPlan` 在不等价时返回 `Counterexample`。搜索顺序为行数从少到多，行数
相同时按排序后的元组序列字典序取最小；`Options.MaxMultiplicity` 限制同一
元组的重数。绝大多数失败是逐行局部问题，一行反例即可；若 `HAVING` 过滤掉
一行反例，会在 `MaxTotalRows` 内尝试重复元组和两行组合。极端自定义 `HAVING`
下可能返回 `PreHaving=true` 的聚合关系反例。

## 测试

```sh
go test ./...
go vet ./...
```

测试覆盖：NULL 分组、全 NULL 度量的空 `SUM`、`AVG` 非线性、重叠视图相减、
额外空组、`HAVING` 过滤空组、负系数、10 个视图的折半搜索、`2^40` 稀疏端点、
最小反例与独立小域穷举。
