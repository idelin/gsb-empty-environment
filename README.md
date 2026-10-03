# sdf — 动态距离场栅格化采样与等值面提取器

纯 Go 标准库实现的二维/三维有向距离场（SDF）库：解析几何图元 + CSG 建模、
多核并行栅格化采样、基于 SDF 梯度的极速光线行进（sphere tracing）、
Marching Squares / Marching Tetrahedra 等值面提取，以及三角网格 SDF。

## 功能

- **SDF 计算器（2D/3D）**：圆、矩形、线段胶囊、球、盒、平面、圆柱；
  并/交/差/平滑并 CSG 组合；平移变换。内部为负、外部为正。
- **栅格化采样**：`Sample2` / `Sample3` 将任意场景并行采样为
  64×64（或 64×64×64）距离场网格；网格本身通过双线性/三线性插值
  实现 `SDF2`/`SDF3` 接口，可直接被光线行进或二次采样（动态场每帧重采样）。
- **光线行进**：`RayMarch2` / `RayMarch3` 每步按当前点 SDF 值安全跨越
  最大空闲距离（sphere tracing），极少迭代即收敛于碰撞表面；
  `Normal2` / `Normal3` 由中心差分梯度估计法线。
- **等值面提取**：`Grid2.Contour`（Marching Squares，含鞍点歧义消解）、
  `Grid3.Surface`（Marching Tetrahedra，法线朝外）。
- **三角网格 SDF**：最近点距离 + 广义环绕数（solid angle）判号，
  闭合网格可直接作为距离场参与采样与光线行进。

## 运行

```sh
go test ./...        # 运行全部测试
go run ./cmd/sdfdemo # 运行样例（ASCII 可视化 + 光线行进 + 等值面提取）
```

## 示例

```go
scene := sdf.Union3(
    sdf.Sphere{Center: sdf.V3(0, 0, 0), Radius: 1},
    sdf.Box3{Center: sdf.V3(1.3, 0, 0), Half: sdf.V3(0.5, 0.5, 0.5)},
)
grid := sdf.Sample3(scene, 64, 64, 64, sdf.V3(-2, -2, -2), sdf.V3(2.4, 2, 2))

res := sdf.RayMarch3(grid, sdf.V3(4, 1.2, 0.8), sdf.V3(-1, -0.3, -0.2).Normalize(),
    sdf.MarchOptions{StepScale: 0.9})
// res.Hit == true, res.Steps 仅十余步

tris := grid.Surface(0) // marching tetrahedra 提取零等值面
```

> 注意：对栅格化网格（插值场）做光线行进时建议 `StepScale: 0.9`，
> 因为插值值在棱边附近可能略微低估真实距离；解析场用默认 1.0 即可。

## 样例输出（节选）

```
=== 2D: 64x64 signed distance field ===
rasterized 64x64 grid in 1.583ms (4096 samples)
     ..::-==++*#*@@@@@@@@@@@@@@**#*@@@@@@@@@@@@@@***+==-::.
    ..:--=++***@@@@@@@@@@@@@@@@@@**@@@@@@@@@@@@@@***+==-::.
    ..:--=++***@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@***+==-::.
marching squares: extracted 208 contour segments (iso=0)
ray march on grid: hit=true steps=8 t=1.2692 point={0.5066 0.5010}

=== 3D: 64x64x64 signed distance field ===
rasterized 64x64x64 grid in 7.429ms (262144 samples)
ray march (analytic field):    hit=true steps=14 point={1.6693 0.5008 0.3339}
ray march (rasterized 64^3):   hit=true steps=18 point={1.6695 0.5009 0.3339}
marching tetrahedra: extracted 35384 triangles (iso=0)
mesh SDF (cube): distance(3,0,0)=+2.000  distance(0,0,0)=-1.000  ray hit steps=2
```

## 测试结果

```
$ go test ./...
ok  	sdf	0.568s
```

25 个测试全部通过，覆盖：图元/CSG 距离正确性、64×64 与 64³ 验收网格
（尺寸、内外符号、对称性）、插值精度 O(h²)、光线行进命中距离/步数上限/
未命中、栅格网格上的光线行进、法线方向、等值线长度≈圆周、等值面面积
≈球面、三角形法线朝外、网格 SDF 符号约定与网格→栅格→等值面回环。

## 代码结构

| 文件 | 内容 |
| --- | --- |
| `vec2.go` / `vec3.go` | 向量数学 |
| `sdf2.go` / `sdf3.go` | SDF 接口、解析图元、CSG 组合 |
| `grid2.go` / `grid3.go` | 并行栅格化采样 + 双/三线性插值 |
| `raymarch.go` | sphere tracing 光线行进 + 梯度法线 |
| `contour2.go` | Marching Squares 等值线提取 |
| `surface3.go` | Marching Tetrahedra 等值面提取 |
| `mesh.go` | 三角网格 SDF（最近点 + 环绕数判号） |
| `cmd/sdfdemo/main.go` | 运行样例 |
