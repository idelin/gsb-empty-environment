# raster

离屏渲染服务的三角形光栅化包。针对共享边上的缝隙与重复着色、透视
插值错误、并行结果不确定等问题，给出确定性的实现。

## 设计要点

- **定点坐标与左上填充规则**：屏幕坐标统一量化为 1/256 子像素的
  `int64` 定点数（`fixed.go`），覆盖判定全程整数运算。三角形规整为
  顺时针（屏幕面积为正），边函数在内部严格为正；落在边上的像素仅当
  该边为顶边（水平且自右向左）或左边（向下延伸）时计入（`edge.go`）。
  每条共享边因此只归属一侧三角形，每个像素最多着色一次。
- **透视正确插值**：光栅化前在每个顶点预算 `1/w`、`z/w`、`attr/w`，
  逐像素用定点边函数求屏幕重心权重，聚合后除以 `1/w` 还原深度与属性
  （`screen.go`、`draw.go`）。`w=1` 时自然退化为仿射重心插值。
- **可配置深度测试**：`DepthFunc` 支持 Never/Less/Equal/LEqual/
  Greater/NotEqual/GEqual/Always，可独立开关测试与回写，深度缓冲
  存储 NDC z，清除值由调用方指定。
- **近平面裁剪**：在裁剪空间按 `w + z >= 0`（NDC `z/w >= -1`）做
  Sutherland–Hodgman 裁剪，按交点参数线性插值位置与全部属性，输出
  0/1/2 个三角形（`clip.go`）。
- **图块并行、结果与串行一致**：串行模式与并行模式共用同一个逐像素
  内核 `rasterRect`，差别仅是遍历的像素矩形。并行时先无锁分箱，再按
  互不重叠的图块分派给 worker goroutine；图块内严格按提交顺序处理
  三角形，写入位置互不相交，无需锁或原子操作，输出与串行逐位一致。

## 用法

```go
fb := raster.NewFramebuffer(640, 480, 1) // 清除深度为 1
r, err := raster.New(raster.Config{
    AttrCount:  1,
    Depth:      raster.DepthLess,
    DepthTest:  true,
    DepthWrite: true,
    TileSize:   32,
    Workers:    4, // 1 表示纯串行
})
if err != nil { /* ... */ }

// 裁剪空间顶点：Pos=(x,y,z,w)，颜色等放在 Attr。
verts := []raster.Vertex{ /* 数量必须是 3 的倍数 */ }
if err := r.Draw(fb, verts); err != nil { /* ... */ }

// 自定义着色器（必须无状态或并发安全）；nil 时输出第一个属性。
r.SetShader(func(attr []float64) float64 { return attr[0] })
```

`Framebuffer.Samples` 记录每个像素实际着色次数，`TotalSamples` 给出
全画面累计值，可直接用于核对“每个像素恰好着色一次”。

## 验证

`raster_test.go` 覆盖：

- 对角线拆四边形、相邻矩形共享竖直边：无重复无遗漏；
- 三角网格在多种分辨率/网格密度/图块尺寸（含不整除与奇数尺寸）下
  铺满整屏，每个像素恰好着色一次；
- 上百个随机三角形下，并行输出与串行逐位一致（颜色、深度、采样数）；
- 透视插值与独立参考实现逐位比对，`w=1` 时退化为仿射插值；
- 全部深度比较函数、测试开关与回写开关；
- 近平面后剔除、跨近平面裁剪（1 或 2 个顶点在近平面后）。

运行：

```sh
go test ./...
go test -race ./...
```
