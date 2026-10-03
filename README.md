# sdf — 动态距离场栅格化采样与等值面提取器

纯 Go 标准库实现的二维/三维有向距离场（SDF）组件：几何图元建模、
距离场栅格化采样、以及基于 SDF 梯度的极速光线行进（Sphere Tracing）。

## 功能

- **SDF 图元**（`sdf/primitive.go`）：圆、矩形、线段胶囊（2D）；球、
  盒、平面（3D）；并集 / 差集 CSG 组合。约定内部为负、外部为正。
- **栅格化采样**（`sdf/grid.go`）：`Rasterize2` / `Rasterize3` 把任意
  几何模型采样为 Nx×Ny（×Nz）距离场网格；`Sample` 做双线性 / 三线性
  插值；`Gradient` 用中心差分从距离场梯度提取表面法线（等值面方向）。
- **光线行进**（`sdf/raymarch.go`）：`RayMarch2` / `RayMarch3` 每步按
  当前点 SDF 值安全跨越最大空闲距离，极少步数收敛于碰撞表面；
  `RayMarchGrid2` / `RayMarchGrid3` 直接对采样网格行进（带 0.9 安全
  系数防止插值误差导致的越界）。

## 运行样例

```sh
go run ./cmd/demo
```

输出（场景：原点处半径 1 的球 + x=2.2 处单位盒，64³ 网格）：

```
rasterized 64x64x64 grid: 262144 samples, sdf(origin)=-0.918 sdf(0,0,2)=1.001
ray 0: analytic hit=true t=4.000 steps=2 | grid hit=true t=4.002 steps=6
        hit point {0 0 1}, grid normal (0.00, 0.00, 1.00)
ray 1: analytic hit=true t=5.164 steps=5 | grid hit=true t=5.165 steps=8
        hit point {0.811 0.487 0.324}, grid normal (0.81, 0.49, 0.32)
ray 2: analytic hit=true t=4.500 steps=3 | grid hit=true t=4.500 steps=6
        hit point {2.2 0.5 0}, grid normal (0.00, 1.00, 0.00)
ray 3: analytic hit=false (miss)
```

正对球面的光线仅需 2 步即收敛，体现 sphere tracing 按 SDF 值安全
跨越最大空闲距离的特性。

## 测试

```sh
go test ./... -v
```

13 个测试全部通过，覆盖：图元距离正确性（符号、表面为零）、CSG 组合、
64×64 / 64³ 栅格化与插值精度、梯度法线方向、任意起点光线行进的命中
位置 / 步数上限 / 未命中情形，以及对采样网格的光线行进。

```
ok  	github.com/example/sdf/sdf	2.282s
```
