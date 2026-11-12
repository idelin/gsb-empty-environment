# 角色网格采用线性骨骼蒙皮时，关节大幅旋转会像拧毛巾一样收缩

角色网格采用线性骨骼蒙皮时，关节大幅旋转会像拧毛巾一样收缩。实现供Go动画渲染宿主调用的双四元数蒙皮包，骨骼仅允许刚体变换，每顶点权重非负且和为一，先以最大权重骨骼为参照统一四元数符号，再加权并约束归一化实部与对偶部正交；位置和法线分别变换，不混入平移到法线。零总权重及非刚体矩阵拒绝。用单骨骼解析解、对跖表示与180度关节旋转验证刚体长度、法线单位性及输入骨骼排列不变性。

## API

```go
import taskpkg "example.org/domainreissue/q027"

// 从行主序 4x4 矩阵构建骨骼；[16]float64 与列主序布局也可识别。
bone, err := taskpkg.FromMatrix4(boneMatrix)
if err != nil {
    // 缩放、剪切、反射、投影等非刚体矩阵会被拒绝。
}

// 位置与法线分别蒙皮。权重非负且总和为 1，零总权重返回错误。
position, err := taskpkg.SkinPoint(weights, bones, inputPosition)
normal, err := taskpkg.SkinNormal(weights, bones, inputNormal)
```

也可以先取得逐顶点混合后的双四元数，再复用于多个属性：

```go
blend, err := taskpkg.BlendDualQuaternions(weights, bones)
position := blend.TransformPoint(inputPosition)
normal := blend.TransformNormal(inputNormal) // 只旋转并归一化，不含平移
```

主要保证：

- 混合前以最大权重骨骼为参照统一 `q` 与 `-q` 的符号。
- 混合后约束 `|real| = 1` 且 `real·dual = 0`，结果仍是刚体变换。
- 骨骼输入顺序与权重顺序同步重排时，蒙皮结果不变。
- 位置经过旋转加平移；法线只经过旋转并归一化。
- 非有限输入、非刚体骨骼、负权重、非归一化权重及零总权重均被拒绝。
