# tablegrid

采集器把网页表格转成二维网格时，跨行跨列的单元格常被放错位置。`tablegrid`
是一个零依赖的 Go 包，输入已解析的节点树（实现 `tablegrid.Node` 接口即可），
按 HTML 规范的表格模型（table model）输出展开后的二维网格。

## 能力

- `colspan` / `rowspan`：按“逐行逐格找空槽位”的规范算法放置单元格；
- `rowspan=0`：延伸到单元格所在行组（thead/tbody/tfoot/隐式 tbody）的最后一行，不跨组；
- 行组：识别显式 `thead`/`tbody`/`tfoot`，行组外的裸 `<tr>` 归入隐式 `tbody`；
- 重叠冲突：单元格落到已占用槽位时插入新列并从头重走（rewalk）放置流程；
- 跨度钳制：非正或非法值回退为 1，超过 1000 的值钳制为 1000；
- 列路径：多行 `<th>` 表头合并为每列的 `HeaderPath`，连续重复表头自动去重；
- 来源坐标：每个单元格记录 `SourceRow`（tr 序号）与 `SourceCol`（tr 内单元格序号）；
- 嵌套表格作为外层单元格的普通内容，不参与外层网格计算。

## 用法

```go
type myNode struct { /* 实现 tablegrid.Node */ }

var node tablegrid.Node = parseTable()
grid := tablegrid.Build(node)

fmt.Println(grid.Rows, grid.Cols)
for _, c := range grid.Cells {
    fmt.Printf("text=%q at=(%d,%d) span=%dx%d source=(%d,%d) path=%v\n",
        c.Text, c.Row, c.Col, c.RowSpan, c.ColSpan, c.SourceRow, c.SourceCol, c.ColPath)
}

// 按网格坐标取覆盖该槽位的单元格（空槽位返回 nil）。
cell := grid.CellAt(2, 3)
matrix := grid.CellMatrix() // rows×cols 的指针矩阵副本
```

## 测试与朴素模型对照

- `naive_test.go` 用最直白的步骤重新实现了一遍规范算法：逐行处理、
  `rowspan=0` 在“后续行”中逐行延伸、冲突时整体重放；
- 规范示例（基础表、跨度表、重叠冲突表）有固定断言；
- 随机生成 12000+ 张激进跨度表格，生产实现与朴素模型在网格、尺寸、
  单元格几何、来源坐标上逐项一致，并校验结果确定性与行组区间。

```sh
go test ./...
go test -race ./...
go test -cover ./...
```
