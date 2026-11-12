# RGB 三维 LUT 四面体插值

该包为 Go 视频渲染宿主提供规则 RGB 三维查找表。采样使用四面体插值，而不是三线性插值。单元内的三个分数坐标按从大到小排序，相等时按 R、G、B 通道顺序决胜，从而唯一选择共享黑色—白色对角线的六个四面体之一。

## 基本用法

```go
lut, err := taskpkg.NewLUT(sizeR, sizeG, sizeB, table)
if err != nil {
    return err
}

out, err := lut.Sample(r, g, b) // float64 线性 RGB，返回 RGB

// 原地处理一个 stride 为 4 的 RGBA 帧（alpha 不读取、不验证、不修改）。
if err := lut.ApplyFrame(frame, width, height, 4); err != nil {
    return err
}
```

- `table` 支持 `[]float32`、`[]float64`、`[][3]float32`、`[][3]float64` 和 `[]RGB`；长度必须与网格尺寸严格一致。
- 默认布局为红色变化最快（`.cube` 常见布局）。可用 `WithLayout(BlueFastest)` 选择蓝色变化最快的布局。
- 默认域为 `[0,1]^3`。可用 `WithDomain(lower, upper)` 指定规则网格的 RGB 定义域。
- 默认边界行为是 `BoundaryClamp`；传入 `Reject` 或 `WithBoundary(BoundaryReject)` 可拒绝越界颜色。
- 帧的 `stride` 是相邻两个像素 R 分量之间的浮点元素数，因此至少为 3；`stride=4` 时保留交错 alpha，更大的值可表示像素间填充。
- 原地处理先完整验证尺寸和所有 RGB 分量，再开始写入，所以任何错误都不会留下部分修改的帧。alpha 完全不参与验证或分级。

## 正确性验证

测试覆盖：

- 仿射颜色变换在随机采样点上的精确恢复；
- 每个内部单元面在共享面两侧的连续性（包括分量重合的决胜情形）；
- 每个单元所有角点与表节点的逐位一致恢复；
- 六种严格排序及 RGB 决胜规则；
- 夹取/拒绝边界、非连续 stride、原地处理、alpha 保留和失败原子性。
