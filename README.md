# gridfill

按非零绕数规则（non-zero winding）判定整数网格单元中心 `(i+0.5, j+0.5)`
是否落在轴对齐绘图路径的填充区内。

- 路径只由水平/垂直线段组成，可自交、可重复走边；反向重复边相互抵消。
- 恰好落在边上的中心单独分类为 `Boundary`（精确 float64 比较）。
- 提供两种实现并互相核对：
  - `ScanlineFill`：扫描线批量填充，按行收集竖直边交点、从右向左累积绕数；
  - `FillGridPointwise` / `ClassifyPoint`：逐点直接射线法（+x 方向，半开区间
    `[lo,hi)` 保证共享端点只计一次）。

## 用法

```go
segs, err := gridfill.SegmentsFromPath([]gridfill.Point{
    {0, 0}, {4, 0}, {4, 4}, {0, 4}, {0, 0},
})
g := gridfill.ScanlineFill(segs, -1, -1, 5, 5)
fmt.Println(g.At(2, 2)) // Inside
```

## 测试

```
go test ./...
```

覆盖：共享端点/共线拆分边、反向重复边抵消、嵌套反向环（打孔）、自交路径、
开放路径、边上中心边界，以及 200 组随机路径下扫描线与射线法的全网格交叉核对。
