# 纹理以斜角映射到屏幕时，各向同性mipmap会糊掉长条细节

纹理以斜角映射到屏幕时，各向同性mipmap会糊掉长条细节。写供Go软件着色宿主调用的椭圆过滤包，输入纹理坐标及其屏幕导数，构造像素足迹二次型，沿短轴选择mip级并在椭圆内按exp(-2r²)-exp(-2)加权，r²≤1才参与；长短轴比按给定上限裁剪，退化足迹退为双线性。寻址支持周期与钳制，所有颜色在线性域预乘。用常量、棋盘与旋转足迹的高采样参考核对权重归一和接缝。

## 用法

包名为 `taskpkg`，采样路径不产生堆分配。

```go
import "example.org/domainreissue/q018"

// 线性域、预乘 alpha 的基础纹素，row-major。
base := make([]taskpkg.ColorRGBA, width*height)
for i := range base {
    base[i] = taskpkg.Premultiplied(r, g, b, a)
}

// NewTexture 以 2x2 盒平均构建完整 mip 链；两个轴可独立选择寻址。
tex := taskpkg.NewTexture(width, height, base,
    taskpkg.AddressWrap, taskpkg.AddressClamp)

color := taskpkg.Sample(tex, taskpkg.SampleParams{
    U: u, V: v,
    DuDx: duDx, DvDx: dvDx, // 右邻像素的纹理坐标导数
    DuDy: duDy, DvDy: dvDy, // 下邻像素的纹理坐标导数
    MaxAnisotropy: 8,       // 0 或负数使用默认上限 8
})
```

## 过滤流程

1. 屏幕像素的单位圆足迹经雅可比 `J = d(texel)/d(screen)` 映射为纹理空间椭圆，二次型 `G = J Jᵀ`，`r² = dᵀ G⁻¹ d`。
2. 对 `G` 做 2x2 特征分解：短轴长度决定 mip 级 `floor(log2(minor))`，并钳制到 `[0, levels-1]`。
3. 按 `MaxAnisotropy` 抬高短轴特征值以裁剪长短轴比，再重建对称矩阵，保持椭圆方向不变。
4. 在椭圆轴对齐包围盒内枚举纹素，仅对 `r² ≤ 1` 的纹素以 `exp(-2r²)-exp(-2)` 加权累加，并按权重和归一化。
5. 零面积或数值退化的足迹直接返回基础级双线性结果；wrap/clamp 在每个纹素 tap 上生效。

## 验证

`go test ./...` 覆盖：

- 常量纹理下任意足迹的权重归一（输出恒等于输入颜色）。
- 大足迹离散权重和与解析圆盘积分一致。
- 棋盘纹理在周期接缝处的高采样参考比对，以及平移整数周期的严格不变性。
- 0°/30°/45°/120° 旋转椭圆与高采样高斯参考的吻合。
- 钳制寻址、轴比裁剪、短轴 mip 选择、退化双线性回退与预乘 alpha 保持。
