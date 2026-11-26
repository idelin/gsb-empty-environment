# mvrewrite 使用指南

仓库根目录是可导入的 Go 包 `mvrewrite`，模块路径为
`prescreen.local/ps-b126d5e285`。

## 快速示例

```go
import mv "prescreen.local/ps-b126d5e285"

catalog := mv.NewCatalog().
    Table("a", mv.PKCol("id")).
    Table("b", mv.PKCol("id"), mv.NotNullCol("aid"), mv.Col("note"))

q := &mv.Query{
    Schema:     catalog,
    Root:       mv.Left(mv.Scan("a"), mv.Scan("b"), mv.On(mv.C("a", "id"), mv.C("b", "aid"))),
    NullReject: []mv.ColRef{mv.C("b", "id")},
    Select:     []mv.ColRef{mv.C("a", "id"), mv.C("b", "id")},
}

v := &mv.View{
    ID:     "left-join-view",
    Root:   mv.Left(mv.Scan("a"), mv.Scan("b"), mv.On(mv.C("a", "id"), mv.C("b", "aid"))),
    Select: []mv.ColRef{mv.C("a", "id"), mv.C("b", "id")},
}

report, err := mv.Analyze(catalog, q, []*mv.View{v})
ids := report.IDs()
sql := report.Decisions[0].Compensation.Filter.SQL()
```

## 判定结果

- `Report.Decisions` 按输入顺序返回每张视图的判定。
- `Decision.Compensation` 在可重写时给出 DNF 补偿条件、去重投影列和 SQL 文本。
- `Decision.Counterexample` 在不可重写时给出具体表实例、查询结果、视图结果、补偿后结果、缺失行和多余行。
- `Decision.RawCounterexample` 给出不加补偿时原始查询与视图输出不同的最小实例。
- `Decision.Ambiguity` 给出补空行与基表真值 NULL 的双实例不可区分证明。
- `Decision.ExhaustiveCheck` 报告有限域小实例搜索的实例数、域和完整性。

DNF 的 `Alternatives` 外层是 OR，每个内层 `[]Predicate` 是 AND。

## 语义入口

- `EvalQuery`：按集合语义执行查询并去重。
- `EvalView`：执行视图并对导出列去重。
- `Rewrite`：执行视图、应用补偿 DNF、再做去重投影。
- `CheckResult`：在一个实例上对照查询与补偿重写。
- `FindRewriteCounterexample`：枚举每表最多两行的合法小实例并最小化反例。

## 验收测试

```sh
go test -count=1 ./...
go test -race -count=1 ./...
```

测试覆盖上层空值拒绝退化 LOJ、缺少非空判别键导致真值 NULL/补空歧义、额外内连接或强谓词丢行、区间补偿，以及多层嵌套 LOJ 补偿失败。
