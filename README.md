# texsample

软件渲染器的纹理在远处闪烁，边缘出现色带。给Go渲染器实现供片元着色调用的纹理采样包：在线性光下构建mipmap，正确处理非2的幂尺寸；采样支持最近、双线性和三线性，按屏幕空间导数计算LOD，寻址支持重复、镜像和钳制；sRGB纹理先转线性再插值。与高精度参考实现比较误差，并测试奇数尺寸的各级mipmap。

## 实现

- `texsample.go`：`Texture`/`Level`、`Filter`、`WrapMode` 与 mip 尺寸/层数计算。
- `srgb.go`：sRGB ↔ 线性传输函数（float64 求值后转 float32）。
- `mipmap.go`：`NewTexture` 构建 mip 链。sRGB 纹素先解码到线性空间（alpha 保持线性直通），再做加权 2x2 box 降采样；奇数尺寸下边界纹素按实际覆盖数计权，尺寸按 `ceil(n/2)` 收缩（如 5→3→2→1）。
- `sampler.go`：`Sampler.SampleGrad` 按屏幕空间导数计算
  `LOD = log2(max(|d(uv)/dx|, |d(uv)/dy|))`（纹素单位），支持
  `FilterNearest`/`FilterBilinear`/`FilterTrilinear` 与
  `WrapRepeat`/`WrapMirror`/`WrapClamp`，另含 `LODBias`、`MinLOD`、`MaxLOD`。
  最近与双线性共享半纹素内缩约定，纹素中心位于 `(i+0.5)/n`。
- `reference.go`：同一套管线的独立 float64 实现，用于度量 float32 路径的数值误差。

## 验证

`go test ./...`（13 个测试，覆盖率 95.7%）覆盖：

- 奇数/NPOT 尺寸（5x3、7x4、3x1 等）的 mip 尺寸链与各级 box 加权结果；
- sRGB 先转线性再插值（0/1 两纹素均值为线性 0.5 而非 sRGB 灰）；
- 三种寻址模式，含负坐标与镜像周期边界；
- 最近/双线性纹素中心与半纹素边界、导数 LOD 选级、三线性分数 LOD 混合、bias 与 clamp；
- 与 float64 参考实现逐纹素对比：mip 最大误差约 6e-08，采样最大误差约 8.4e-08。
