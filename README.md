# animblend

角色动画要把跑步与持枪上半身动画分层混合。给Go动画运行时实现骨骼姿势混合包：局部旋转用四元数沿最短路径插值并归一化，按骨骼遮罩分层叠加，加法动画相对参考姿势计算，两段循环动画按相位同步混合；再由局部姿势计算世界矩阵，父骨骼先于子骨骼处理。用已知姿势核对世界变换，并检查四元数对跖时不翻转。

## 包内容

- `quat.go` / `vec.go` / `mat4.go`：四元数、向量与列主序 4x4 矩阵。
  - `Slerp` 先把目标翻到同一半球保证最短弧，近重合退化为 nlerp，对跖
    （点积约 -1）改用正交轴兜底，结果始终有限且归一化，不翻转、无 NaN。
- `pose.go`：`Skeleton`（校验父索引且父骨骼在前）、`LocalPose`、`BoneMask`。
  - `BlendPoses` 全身混合；`BlendLayered` 按每骨骼遮罩权重分层叠加。
  - `WorldMatrices` 按拓扑顺序累加 `world = parent * local`。
- `additive.go`：`MakeAdditive` 以 `clip * inverse(reference)` 计算旋转差、
  平移做差；`ApplyAdditive` 按强度从单位旋转 slerp 后左乘到基础姿势。
- `clip.go`：`SampleClip` 处理循环包裹/非循环钳制；`BlendLooping` 用同一
  归一化相位采样两段循环动画，再做最短路径混合，时长不同也保持同步。

## 验证

`go test ./...` 覆盖：已知骨骼链的手算世界变换、对跖与近对跖不翻转、
遮罩分层、加法相对参考姿势、循环相位同步，以及跑步+持枪上半身集成用例。
