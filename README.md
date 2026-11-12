# taskpkg：可实例化三角网格的世界空间射线拾取

本包面向 Go 场景宿主。场景由共享三角网格和可逆仿射实例变换组成；拾取结果按**世界射线参数**排序，而不是按局部空间参数或归一化局部距离排序。

## 快速使用

```go
mesh, err := taskpkg.NewMesh(vertices, indices) // vertices []taskpkg.Vec3, indices []uint32
if err != nil {
	// 处理无效网格
}

scene := taskpkg.NewScene()
nearID, err := scene.AddInstance(mesh, taskpkg.NewAffine(
	taskpkg.Mat3{{2, 0, 0}, {0, 0.5, 0}, {0, 0, 1}},
	taskpkg.Vec(0, 0, 0),
))

ray := taskpkg.NewRay(taskpkg.Vec(0, 0, 10), taskpkg.Vec(0, 0, -1))
hit, ok := scene.Pick(ray)
if ok {
	// hit.T       世界射线参数
	// hit.Point   世界命中点
	// hit.Normal  逆转置变换后的单位世界法线
	// hit.InstanceID / hit.TriangleID
}
```

`Mat3` 为行主序，向量按列向量处理。`Ray.Direction` 不要求归一化；`Hit.T` 始终使用调用方传入的 `Direction` 单位，因此不同实例间可直接比较。

## API

- `NewMesh(vertices, indices)`：构建不可变网格及其共享局部 BLAS。`indices` 为空时把顶点解释为三角形汤。
- `NewTriangleMesh(triangles)`：直接由三角形构建网格。
- `Scene.AddInstance(mesh, transform)`：自动分配实例 ID，可多次引用同一 `*Mesh`。
- `Scene.AddInstanceWithID(id, mesh, transform)`：使用稳定且唯一的显式 ID。
- `Scene.UpdateInstance(id, transform)` / `SetInstanceTransform`：更新可逆仿射矩阵并 refit 世界 TLAS，不重建共享网格 BLAS。
- `Scene.Rebuild()`：显式重建 TLAS 拓扑；用于大量实例移动后恢复层次质量。
- `Scene.Pick(ray)`：最近命中。
- `Scene.PickWithOptions(ray, options)`：可设置 `TMin`、`MaxT`、背面/正面剔除及同距容差。
- `Scene.PickAll(ray, options)`：返回全部命中，按世界参数排序。
- `Scene.InstanceInfo(id)` / `InstanceTransform` / `InstanceMesh` / `InstanceWorldBounds`：宿主侧查询。

## 关键语义

- **世界参数**：局部求交使用 `o_l = M^-1(o_w - b)`、`d_l = M^-1 d_w`（`b` 为仿射平移），不做局部方向归一化；命中后再把局部点变换回世界并投影到原世界射线上，得到 `Hit.T`。所有候选距离比较都使用该世界参数。
- **法线**：面法线按 `M^-T n_l` 变换并归一化；非均匀缩放下不会错误地直接乘线性矩阵。
- **负行列式**：镜像实例保持网格的逻辑正面，等价于渲染器对负行列式翻转 front-face winding。`FrontFace` 和 `CullBackfaces`/`CullFrontfaces` 都基于这一逻辑正反面；`Normal` 仍由逆转置得到，指向该逻辑正面。
- **同距决胜**：世界参数在相对容差内相等时，依次比较 `(InstanceID, TriangleID)`，较小者获胜。默认相对容差为 `1e-12`；`PickOptions.TieTolerance < 0` 可强制精确浮点相等。
- **两级 BVH**：`Mesh` 持有一次构建的局部 BLAS；`Scene` 持有世界空间实例 TLAS。实例变换更新只更新逆矩阵、世界 AABB 并 refit TLAS，共享网格节点和三角形顺序不变。

## 验证

`go test ./...` 包含独立的逐三角形世界空间参考实现，并覆盖：

- 非均匀缩放下最近命中的世界距离排序；
- 同距时实例 ID、三角形 ID 的确定性决胜；
- 镜像实例的法线、正面与背面剔除；
- `1e-9`、`1e-12`、`1e-15` 级极小可逆缩放；
- 随机旋转/非均匀缩放/镜像组合下与暴力世界空间参考的逐射线核对；
- 实例变换更新后 TLAS refit，同时确认共享网格 BLAS 未被修改。
