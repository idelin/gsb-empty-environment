# 带几何证据的时域重投影

`taskpkg` 为 Go 渲染管线实现带几何证据的时域抗锯齿重投影。它在移动物体
交叉遮挡时拒绝后层历史，避免把错误表面混入前层。

## 输入与数据约定

- `Frame.Depth` 是正的相机空间深度，沿相机前向轴（相机看向 `-Z`）。
- `Frame.Color` 是线性、预乘 RGBA；`A<=AlphaEpsilon` 按零 alpha 处理。
- `Frame.Normals` 是世界空间法线；重投影时用仿射线性部分的逆转置变换。
- `Frame.ObjectIDs` 与 `Frame.Generations` 同时匹配才允许历史贡献。
- `Camera.View` 为世界到视图，`Camera.Projection` 为视图到裁剪空间。
- `motions` 是对象从前帧到当前帧的世界空间可逆仿射矩阵。缺失项按静态
  对象的单位矩阵处理；非仿射、奇异或行列式为负的矩阵被拒绝。
- 像素中心为 `(x+0.5, y+0.5)`，双线性足迹落在四个相邻像素上。

## 核心规则

1. 从前帧像素中心按深度反投影到世界，应用对象仿射，再正向投影到当前帧。
2. 历史颜色按当前帧双线性足迹分发；每个目标像素独立校验对象 ID/代次、
   当前深度有效性、深度误差和法线夹角。
3. 同一目标上的碰撞先按当前相机深度、再按对象 ID、代次和源索引排序。
   只有最前表面的同对象同代次贡献参与权重归一。
4. 归一后的历史颜色裁剪到当前帧同对象 3×3 邻域的逐通道范围。
5. 最终颜色为当前颜色与裁剪后历史的加权平均：
   `t = historyWeight / (historyWeight + CurrentWeight)`。无可用历史时直接
   返回当前颜色的精确副本。
6. 近平面穿越、负尺度、零 alpha、无效深度/法线均有显式拒绝路径并计入
   `ReprojectionStats`。

`Result.HistoryColor` 暴露独立的历史解析结果，便于调用方使用自己的时域
混合；`Result.Frame` 是可直接作为下一帧历史的完整新帧。

## 使用示例

```go
cfg := taskpkg.DefaultConfig()
cfg.DepthTolerance = 0.03
cfg.MaxNormalAngle = 25 * math.Pi / 180

result, err := taskpkg.Reproject(previous, current, motions, cfg)
if err != nil {
    return err
}
nextHistory = result.Frame
```

需要双缓冲时使用 `HistoryBuffer`：

```go
history, _ := taskpkg.NewHistoryBuffer(previous)
result, version, err := history.Resolve(current, motions, cfg)
if err == nil {
    err = history.Commit(version, result)
}
```

`Resolve` 在工作前对历史做深拷贝；`Commit` 只接受仍匹配的版本。因此旧历史
不会在新帧完整解析前被覆写，过期结果返回 `ErrStaleHistory`。

## 并行与确定性

`Config.TilesX/TilesY` 控制图块划分，`Workers` 控制并发度。源像素投影和
目标证据校验只做一次，结果按目标图块分发；每个目标像素的候选集合和排序
规则与整帧处理完全相同，因此图块并行结果逐位一致。

## 验证

```sh
go test ./...
go test -race ./...
```

测试覆盖静态单位变换、合成双层遮挡、相机抖动、近平面穿越、负尺度、零
alpha、深度/法线证据、同对象邻域裁剪、透视相机往返、历史双缓冲以及图块
并行等价。
