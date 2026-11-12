# 实时阴影的级联边界随相机微动闪烁

实时阴影的级联边界随相机微动闪烁。给Go渲染宿主实现方向光阴影视锥规划包，输入相机投影、视图、光方向及显式深度分割；把每段视锥投到固定光空间，以视锥角点均值为球心、最大角点距离为半径确定尺寸，再按阴影贴图像素单位量化中心，加入给定施法者深度区间。反向Z由显式参数选择，零长光向量拒绝。用相机亚像素移动、旋转和极端纵横比验证所有视锥角点被覆盖、量化前后单位稳定及矩阵往返误差。

## `shadow` 包

模块 `example.org/domainreissue/q038/shadow` 提供方向光级联阴影的视锥规划，
输出每级的光视图、正交投影与合并矩阵，可直接写入阴影贴图的常量缓冲。

### 设计要点

- 固定光空间：光旋转只依赖光方向，相机平移/旋转不会改变光基向量。
- 包围球：以每段视锥 8 个角点的均值为球心、最大角点距离为半径。
- 纹素量化：球心在光空间 xy 按 `2*radius/ShadowMapSize` 量化，亚像素相机
  移动只能让中心保持在同一纹素格或整体跳整纹素，消除连续闪烁。
- 施法者区间：`CasterRange` 以 `dot(position, lightDir)` 的世界单位给出，
  被并入正交近远平面，不影响 xy 包围盒。
- 反向 Z：由 `Config.ReverseZ` 显式选择，同时作用于角点反投影与光投影。
- 零长光向量返回 `ErrZeroLightVector`，奇异相机矩阵返回 `ErrSingularMatrix`。

### 用法

```go
res, err := shadow.Plan(shadow.Config{
    Projection:     proj,
    View:           view,
    LightDirection: shadow.Vec3{X: -0.4, Y: -1, Z: -0.25},
    Splits:         []float64{0.1, 0.3, 1.0},
    ShadowMapSize:  2048,
    ReverseZ:       false,
    CasterRange:    &shadow.CasterRange{Min: -40, Max: 60},
})
for _, c := range res.Cascades {
    // 上传 c.LightViewProjection
    _ = c
}
```

### 验证

`go test ./...` 覆盖：零长向量/非法分割/非法施法者区间拒绝、亚像素平移下的
纹素单位稳定与量化、相机旋转与极端纵横比下全部角点 NDC 覆盖、施法者区间并入、
正反向 Z 深度映射、相机与光矩阵往返误差，以及 300 组随机配置模糊测试。
