# 多级表头列语义绑定

`taskpkg` 是供 Go 表格抽取宿主调用的列语义绑定包。它不再把第一行当成完整列名，而是把每列覆盖的所有表头祖先组合成有序路径。

## 输入模型

- `Grid.Rows` 是已经展开的矩形网格，每个槽位对应一个被占用的表格位置。
- `Cell.Source` 保存原始单元格来源。由 `rowspan`/`colspan` 展开出来的多个槽位共享同一个 `Source`。
- `Cell.IsRowHeader`、`Cell.IsColumnHeader` 由抽取宿主标记。左上角单元格可以同时具有两个属性。
- `CellRef.ID` 可选；存在时按 ID 匹配，否则按原始 `Row`/`Col` 匹配。

## 绑定规则

- 每列只从顶部表头区（连续带列头属性的行）读取祖先，按从上到下顺序生成 `ColumnBinding.Path`。
- 空文本表头不会从相邻列借用标签，也不会伪造路径节点。
- 路径按原始单元格来源去重：同一跨列/跨行表头只出现一次，不同来源的重复标签会保留。
- 每个数据列产生且只产生一个 `ColumnBinding`。相同 `PathText` 不会被合并；`SourceKey` 记录路径来源，`Identity` 还包含展开列号。
- 年份、单位只从 `BindConfig.HeaderSemantics` 明确点名的表头来源解析，不在普通表头文本上做推断。
- `HeaderSemantics.Value` 可直接给出语义值；否则年份按四位数年份记号解析，单位支持 `单位：万元`、`（万元）` 等常见写法。
- 表体中间重复表头会作为 `RowKindHeader` 保留，不会污染顶部列路径。
- 正文行由 `BindConfig.RowRules` 依序判定，支持行标签包含、任意单元格包含和自定义谓词；未命中时使用 `DefaultRowKind`（默认 `data`）。
- 每条 `RowBinding.Cells` 都带列绑定 `Identity` 与原始 `Source`；`RowBinding.Sources` 保留该行所有非空来源。

## 最小示例

```go
table, err := taskpkg.Bind(grid, taskpkg.BindConfig{
    HeaderSemantics: []taskpkg.HeaderSemantic{
        {Source: taskpkg.CellRef{Row: 0, Col: 1}, Role: taskpkg.HeaderRoleUnit},
        {Source: taskpkg.CellRef{Row: 1, Col: 1}, Role: taskpkg.HeaderRoleYear},
    },
    RowRules: []taskpkg.RowRule{
        {Kind: taskpkg.RowKindSubtotal, LabelContains: []string{"小计", "subtotal"}},
        {Kind: taskpkg.RowKindTotal, LabelContains: []string{"合计", "总计", "total"}},
    },
})
```

测试覆盖嵌套年度列、跨列单位、重复路径、组内小计、中间重复表头和缺失表头的来源完整性。
