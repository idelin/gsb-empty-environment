# 三维链式逆运动学包（Go）

面向动画宿主的三维链式 IK 求解器，保证角色脚部贴合目标位置，并通过局部锥角
限制阻止膝关节反折。纯标准库实现，无外部依赖。

## 能力

- 输入骨段长度、固定根位置、末端目标与每个关节的局部锥角限制（`JointLimit`）。
- 约束 FABRIK：前向 + 后向位置迭代，每次投影都恢复骨长并把骨方向裁剪进锥域。
- 极轴退化（方向反平行等）使用固定参考轴 `ReferenceAxis` 确定性求解，避免 NaN。
- 输出关节位置与局部四元数旋转，并给出重算末端残差、可达性与收敛标志。
- 不可达目标返回沿根→目标完全伸展的最小残差候选。
- 迭代预算耗尽时如实返回 `Converged == false` 和当前最优候选。
- 另附两骨解析解 `SolveTwoBone`（余弦定律，含弯曲侧提示与退化处理）。

## 快速上手

```go
package main

import (
    "fmt"
    "math"

    "example.org/domainreissue/q062"
)

func main() {
    // 4 节骨，带每关节锥角限制。HalfAngle=math.Pi 表示不限制。
    req := taskpkg.ChainRequest{
        Lengths: []float64{1, 1, 1, 1},
        Root:    taskpkg.Vec3{Y: 2},
        Target:  taskpkg.Vec3{X: 3.2, Y: 0.8, Z: 0.4},
        RestPositions: []taskpkg.Vec3{
            {Y: 2}, {X: 1, Y: 2}, {X: 2, Y: 2}, {X: 3, Y: 2}, {X: 4, Y: 2},
        },
        Limits: []taskpkg.JointLimit{
            {HalfAngle: math.Pi},
            {HalfAngle: 25 * math.Pi / 180, PoleHint: taskpkg.Vec3{Z: 1}},
            {HalfAngle: 25 * math.Pi / 180},
            {HalfAngle: math.Pi},
        },
        MaxIterations: 32,
        Tolerance:     1e-6,
        ReferenceAxis: taskpkg.Vec3{Y: 1},
    }

    res, err := taskpkg.SolveChain(req)
    if err != nil {
        panic(err)
    }
    fmt.Println("tip residual:", res.Residual, "converged:", res.Converged)
    // res.Positions 为 n+1 个关节点；res.Rotations[i] 为局部四元数。
}
```

两骨（大腿/小腿）解析解：

```go
res, err := taskpkg.SolveTwoBone(taskpkg.TwoBoneRequest{
    UpperLength:   1.0,
    LowerLength:   1.0,
    Root:          taskpkg.Vec3{},
    Target:        taskpkg.Vec3{X: 1.2, Y: -0.4},
    BendHint:      taskpkg.Vec3{Z: 1},   // 选择膝盖弯曲侧
    ReferenceAxis: taskpkg.Vec3{Y: 1},  // 极轴退化时的固定参考
})
```

## 坐标与旋转约定

- `ConeAxis` 为零向量时表示该骨的休息姿势方向；非根关节的局部向量通过父骨
  旋转带到当前姿势，根关节帧固定于休息姿势。
- `PoleHint` 只用于极轴退化时选择旋转平面，为零时回退到 `ReferenceAxis`。
- `Rotations[0]` 是根的世界旋转；`Rotations[i]`（i>0）是相对父关节的局部旋转。
  宿主可用 `q_world_i = q_world_(i-1) * q_local_i` 重建世界旋转并做 FK 校验。

## 验证

```sh
go test ./... -count=1 -race
go test -cover ./...
```

测试覆盖：两骨解析解（含不可达、过近折叠、根目标重合退化）、多节已知姿势的
骨长与 FK 重算误差、目标扫掠穿越锥边界时的限制保持、膝关节防反折、不可达伸展
候选以及迭代预算耗尽时的未收敛返回。
