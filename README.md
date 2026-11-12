# 模型烘焙需要把开放网格展成二维纹理坐标，不能接受悄悄翻面的结果

模型烘焙需要把开放网格展成二维纹理坐标，不能接受悄悄翻面的结果。为Go资产管线实现调和参数化包，输入定向流形三角网格且拓扑为圆盘，将唯一边界按累计边长映到单位圆，其余顶点取相邻顶点均值，求稀疏线性系统；输出每面UV面积符号、边界对应与最大残差。多边界或非流形输入拒绝，发现翻面返回失败证据。用小网格稠密参考、不同顶点顺序和细长三角形验证唯一解及正向面积。

## 包结构（包名 `taskpkg`）

- `mesh.go`：流形与圆盘拓扑校验（索引、边入射数、连通性、边界环计数、`V-E+F==1`）。
- `sparse.go`：CSR 稀疏矩阵、内部顶点均匀拉普拉斯系统组装、Jacobi 预条件共轭梯度求解。
- `dense.go`：带部分主元的稠密高斯消元，作为小网格的独立参考求解器。
- `param.go`：`Parameterize` 入口、边界累计边长映射、每面有符号 UV 面积与翻面证据。

## 用法

```go
res, err := taskpkg.Parameterize(mesh)
switch {
case errors.Is(err, taskpkg.ErrBoundary):     // 多边界 / 无边界
case errors.Is(err, taskpkg.ErrNonManifold):  // 非流形 / 朝向不一致 / 不连通
case errors.Is(err, taskpkg.ErrNotDisk):      // 亏格非零
case errors.Is(err, taskpkg.ErrFlip):
    var flip *taskpkg.FlipError
    errors.As(err, &flip) // flip.Evidence 给出翻面/退化面与面积
case err != nil:
    // ErrInvalidMesh 或 ErrSolver
}
// res.UV / res.Boundary / res.FaceAreas / res.MaxResidual
```

成功时每面 `FaceArea.Area > 0`；边界点在单位圆上，`T` 为归一化弧长、`Angle` 为极角。

## 验证

`go test ./...` 覆盖：

- 稀疏 PCG 解与稠密高斯消元参考解逐分量一致（小网格）。
- 随机顶点置换后解仅相差整体旋转，半径、相位对齐后一致，面积不变。
- 细长三角形（高宽比约 1e6、含 z 抖动）仍全部正向、残差达标。
- 重复求解确定性一致（唯一解）、内部顶点满足邻域均值条件。
- 多边界、非流形边、反向绕序、封闭四面体、坏索引、不连通输入均被拒绝。
- 零长度边界边产生退化面时返回 `FlipError` 证据。
