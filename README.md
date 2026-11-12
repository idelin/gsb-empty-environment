# 资产编辑器允许用户拖动少量控制顶点，其余网格应保持局部细节

资产编辑器允许用户拖动少量控制顶点，其余网格应保持局部细节。写供Go建模宿主调用的拉普拉斯变形包，输入连通三角网格、固定控制点和目标位置，使用非负均匀邻接权重保存原拉普拉斯坐标，最小化坐标残差并严格满足控制点；每个连通分量必须有锚点，缺失则拒绝求解。用稀疏求解并返回残差，支持只更新控制目标复用分解。与小矩阵稠密解比较，验证平移等变、多个分量及奇异输入，不做物理仿真。

## 实现

代码位于 `laplacian/` 子包（纯标准库，无外部依赖）：

- `laplacian.go`：网格校验、BFS 连通分量、均匀权重拉普拉斯矩阵 `L`（对角 1，邻接 `-1/degree`），法方程 `LfᵀLf xf = Lfᵀ(δ−Lδ₀)` 的装配与求解，残差统计。
- `sparse.go`：CSC 稀疏矩阵与自研 simplicial LDLᵀ 分解；锚点作为硬约束被消元，每个连通分量至少一个锚点否则返回 `ErrComponentWithoutAnchor`。
- 控制点位置为硬约束（`Result.AnchorMax` 数值上为 0）；`SetTargets` 仅更新目标，下一次 `Deform` 复用已分解的 LDLᵀ。

## 用法

```go
import "example.org/domainreissue/q028/laplacian"

editor, err := laplacian.NewEditor(mesh, anchorIDs, targetPositions)
if err != nil { /* ErrInvalidMesh / ErrNoAnchors / ErrComponentWithoutAnchor ... */ }

result := editor.Deform()              // 首次：构建并分解
_ = editor.SetTargets(newTargets)      // 拖动控制点
result2 := editor.Deform()             // 复用分解，result2.ReusedFactorization == true
```

`Result` 返回变形后坐标、逐顶点与 RMS/最大拉普拉斯残差以及锚点违约量。

## 测试

`go test ./...` 覆盖：与稠密部分主元 LU 参考解对比、平移等变、恒等变形零残差、多连通分量、孤立顶点与缺锚点拒绝、非法/非有限输入、奇异主元、重复锚点去重、全锚点边界，以及分解复用一致性；另含基准测试对比首次构建与复用分解的开销。
