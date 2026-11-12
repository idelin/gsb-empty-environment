# 烘焙光照时，三角形的UV落在同一图集像素，普通中心采样会漏掉细小面

烘焙光照时，三角形的UV落在同一图集像素，普通中心采样会漏掉细小面。实现供Go烘焙管线调用的保守覆盖包，用精确有理UV坐标求每个像素方格与三角形的交多边形及面积，输出覆盖比例与面积质心的重心坐标。共享边只作零面积接触，不重复贡献；重叠UV保留不同图元，不擅自平均。用随机小三角形验证总面积守恒、退化面零贡献及分块处理与全图一致。

## coverage 包

路径 `example.org/domainreissue/q037/coverage`，全部几何运算基于
`math/big.Rat` 精确有理算术，无浮点误差累积。

- `Rasterize(tri)`：枚举三角形包围盒内每个单位像素方格，用
  Sutherland–Hodgman 半平面裁剪求方格与三角形的交多边形，输出
  `PixelCoverage`（方格下标、精确面积/覆盖比例、面积质心、质心重心坐标、
  图元 ID）。
- `RasterizeBlock(tri, rect)`：分块入口，逐方格计算与 `Rasterize` 完全
  相同；互不重叠的分块拼接结果与整图光栅化逐格一致。
- 共享边/顶点接触裁剪后面积为零，直接跳过，不向任何像素重复贡献。
- 每次调用只描述一个三角形，无全局状态、不合并不平均；重叠 UV 的不同
  图元由调用方凭 `PrimitiveID` 分别保留与累加。
- 退化三角形（共线/重合顶点）返回 `ErrDegenerate`，零贡献。
- 需要接入浮点管线时使用 `CoverageFloat64`、`CentroidFloat64`、
  `BaryFloat64`，精确 `*big.Rat` 字段仍然保留。

```go
tri := coverage.Triangle{
    A:           coverage.NewRatPointInt64(0, 0),
    B:           coverage.NewRatPoint(big.NewRat(1, 3), big.NewRat(0, 1)),
    C:           coverage.NewRatPointInt64(0, 1),
    PrimitiveID: 42,
}
cells, err := coverage.Rasterize(tri)
if errors.Is(err, coverage.ErrDegenerate) {
    return // 退化面零贡献
}
for _, c := range cells {
    _ = c.CellX        // 像素方格下标
    _ = c.Area         // 精确覆盖比例（单位方格面积为 1）
    _ = c.Centroid     // 面积质心理想 UV
    _ = c.Bary         // 质心相对 A/B/C 的重心坐标，和为 1
}
```

## 验证

`go test ./...` 包含确定性手算用例与随机小三角形（带分数 UV）测试：

- 总面积守恒：所有方格 `Area` 之和精确等于三角形面积；
- 面积矩守恒：`Σ area*centroid` 精确等于三角形面积乘三角形质心；
- 退化面零贡献：共线、重合顶点一律返回 `ErrDegenerate` 且无方格；
- 分块一致：随机分块拼接与整图 `Rasterize` 逐格精确相等；
- 中心采样对照：像素中心在三角形外的细小面仍被保守捕获。
