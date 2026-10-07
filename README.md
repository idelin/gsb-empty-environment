# bezier-constraint-projection

矢量编辑器的投影库：移动三次贝塞尔控制柄后，把控制点**精确**投影回约束流形——
端点固定、连接点 C0 连续且一阶导数相等（C1），最小化到编辑位置的加权平方距离。
全程使用有理数精确运算（BigInt），无任何浮点误差。

## 问题形式

至多 10 段三次贝塞尔曲线，每段 4 个控制点。约束：

- **端点固定**：整条曲线的首端点 `P[0][0]` 与末端点 `P[n-1][3]` 始终钉住（即使误标为 free）；
- **C0**：`P[i][3] = P[i+1][0]`；
- **C1**：`P[i][3] - P[i][2] = P[i+1][1] - P[i+1][0]`（一阶导数相等）。

仅移动标为 `free: true` 的点，最小化 `Σ w_i · |P_i - Q_i|²`，其中 `Q_i` 为编辑后位置，
`w_i` 为非负有理权重。求解 KKT 系统 `[2W Aᵀ; A 0][x;λ] = [2Wq; b]` 的精确有理数解。

## 用法

```ts
import { projectControlPoints } from "./src/index.ts";

const result = projectControlPoints({
  segments: [
    [
      { free: false, position: { x: 0, y: 0 } },              // 固定端点
      { free: true, weight: "2/3", position: { x: 1, y: 2 } }, // 自由点 + 编辑目标 + 权重
      { free: true, weight: 1, position: { x: 2, y: 3 } },
      { free: true, weight: 1, position: { x: 4, y: 1 } },     // 连接点（自由）
    ],
    [
      { free: true, weight: 1, position: { x: 5, y: 2 } },
      { free: true, weight: 1, position: { x: 6, y: 1 } },
      { free: true, weight: 1, position: { x: 7, y: 2 } },
      { free: false, position: { x: 9, y: 0 } },              // 固定端点
    ],
  ],
});
```

权重/坐标接受 `Rational | bigint | number | string`（如 `"3/4"`、`"0.625"`）。

## 三种结果

- **`status: "unique"`** — 唯一最优解。`points` 为每段控制点位置（`Rational` 坐标），
  `objective` 为精确最小目标值，`verification` 含代回验证：
  - `constraintsSatisfied`：代回 `A·x = b` 残差精确为 0；
  - `gradientSatisfied`：最优性梯度 `2W(x−q) + Aᵀλ` 精确为 0。
- **`status: "non-unique"`** — 最优解不唯一（仅当某些自由点权重为 0 时可能）。
  附 `directions`（零空间精确基，沿其移动不改变目标值）与 `zeroWeightPoints` 诊断。
- **`status: "conflict"`** — 固定条件本身矛盾（如两个固定点被约束强制相等却坐标不同）。
  返回 Farkas 型线性矛盾证书 `certificate`：乘子 `y` 满足 `yᵀA = 0` 且
  `yᵀb = certificateRhs ≠ 0`，可用 `constraints` 中的行精确验证。

## 运行测试

```sh
npm test    # node --test（Node ≥ 23，直接执行 TypeScript）
```

## 结构

- `src/rational.ts` — 基于 BigInt 的精确有理数（最简分数、四则运算、解析）。
- `src/linalg.ts` — 精确 Gauss-Jordan 消元：RREF、线性求解、零空间、矛盾证书。
- `src/projection.ts` — 约束组装、KKT 精确求解、唯一性判定、代回与梯度验证。
