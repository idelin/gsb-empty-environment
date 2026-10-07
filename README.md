# bezier-constraint-projection

矢量编辑场景：拖动三次贝塞尔控制柄后，把编辑后的控制点**加权最小二乘投影**回约束流形，
恢复「端点固定 + 连接点一阶导数相等（C1）」的约束。全程使用 BigInt 有理数精确运算，
无浮点误差。

## 数学模型

- 输入 1–10 段三次贝塞尔曲线，第 `s` 段控制点 `(P0,P1,P2,P3)`，每点含 `(x,y)`。
- 约束（对 x、y 两个坐标分别建立同构的线性系统 `M x = c`）：
  - 连接点重合：`D_i - A_{i+1} = 0`；
  - 连接点一阶导数相等：三次曲线端点导数为 `3(B-A)`、`3(D-C)`，等导数化简为
    `-C_i + D_i + A_{i+1} - B_{i+1} = 0`；
  - 固定点行：`P = 输入位置`（`free=false` 的点，以及默认钉住的链首 `P0` 与链尾 `P3`）。
- 目标：仅对标记为自由的点最小化 `Σ w_p · ||P_p - P̃_p||²`，`w_p` 为非负有理权重。
- 求解：先精确求 `M x = c` 的特解 `x0` 与零空间基 `N`，再在零空间内解法方程
  `(NᵀWN) z = Nᵀ W (x̃ - x0)`（高斯-若尔当精确消元）。

## 结果形态

- `unique`：唯一解。权重全正时必然唯一。
- `non-unique`：零权重在约束零空间内留下零代价方向。返回一个最优解及
  `nullDirections`（每个方向给出其支撑点集），解不唯一的诊断信息。
- `infeasible`：固定条件相互冲突（如连接两侧被固定在不同位置）。返回线性矛盾
  证书：约束行的非零组合 `y` 满足 `y·M = 0` 但 `y·c ≠ 0`，并附带自检 `verified`。

## 验证（每个解都附带）

- **代回方程**：`M x* - c` 逐行精确为零（`maxConstraintResidual`）。
- **最优性梯度**：梯度 `2W(x* - x̃)` 与 `null(M)` 正交，并解出拉格朗日乘子
  `λ` 使 `Mᵀλ = -∇f`（`maxLagrangeResidual` 为零）。
- `objectiveValue` 为精确有理数最优值。

## 用法

```ts
import { projectBezier } from 'bezier-constraint-projection';

const result = projectBezier({
  segments: [
    { points: [{x:0,y:0}, {x:1,y:0}, {x:2,y:1}, {x:3,y:1}] },
    { points: [{x:3,y:-1}, {x:4,y:-1}, {x:5,y:0}, {x:6,y:0}] },
  ],
  weights: [[1,1,1,1], [1,1,1,1]],      // 也接受 '1/3'、0.5、3n 等
  free:    [[true,false,true,true],
            [true,true,false,true]],     // 仅自由点可移动
  // pinEndpoints: true (默认) 钉住链首 P0 与链尾 P3
});

if (result.status === 'unique') {
  console.log(result.points, result.verification.objectiveValue.toString());
}
```

权重、坐标均可为 `number | string | bigint | Rational`；字符串支持
`"3/4"`、`"-2"`、`"1.25"`、`"1e-3"` 等精确形式。

## 运行

Node ≥ 23（原生 TypeScript 类型擦除），无第三方依赖：

```sh
npm test
```
