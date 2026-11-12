# 曲面细分渲染的自适应网格在相邻面使用不同细分级别时开裂

曲面细分渲染的自适应网格在相邻面使用不同细分级别时开裂。为Go建模宿主实现四边形补丁拼接包，输入共享边拓扑和每补丁2的整数次幂细分级别；先传播限制使邻面级别最多差一，再生成确定的过渡三角形，边点由共享边ID及有理参数唯一标识。只处理可定向流形网格，非流形拒绝。用闭合盒体和环形补丁验证每条内部边恰邻两个三角形、共享位置位级相等且法向方向一致。

## 用法

```go
topo, err := taskpkg.NewTopology(patches) // 校验流形性/可定向性，非流形拒绝
if err != nil { /* ErrNonManifold / ErrNonOrientable / ErrInvalidLevel */ }
topo.PropagateLevels()                    // 邻面级别差收敛到 <= 1（幂等）

st := taskpkg.NewStitcher(topo, cornerPos, nil) // nil 使用双线性插值
mesh, err := taskpkg.Merge(st.Stitch())   // 合并并校验共享位置位级相等
if err != nil { /* 共享点位置不一致 */ }
if err := mesh.Validate(true); err != nil { /* 闭合网格：每条内部边恰邻两个三角形且法向一致 */ }
```

- 补丁级别为以 2 为底的指数（`LevelFromSegments` 可从段数转换并拒绝非 2 幂）。
- 边点 `PointID{Kind: EdgePoint, Edge, Num, Den}` 由共享边 ID 与有理参数 `Num/Den` 唯一标识，两侧补丁计算出位级相等的位置。
- 测试覆盖闭合盒体与环面补丁：`go test ./...`。
