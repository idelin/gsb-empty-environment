# exact-rasterize

可能自交的闭合折线的**精确**栅格化库（TypeScript）：分别按**奇偶（even-odd）**与
**非零绕数（non-zero）**规则生成像素掩码，并输出两种规则判定不同的区域。

## 规则（显式规定）

- **采样点**：像素 `(i, j)` 覆盖区域 `[i, i+1) × [j, j+1)`，采样点固定为像素中心
  `(i+0.5, j+0.5)`。
- **坐标约定**：顶点为整数坐标（像素单位），x 轴向右、y 轴向下；折线隐式闭合
  （末点自动连回首点）。两种填充规则的结果均与顶点环绕方向无关。
- **边界规则**：恰好落在多边形边界（边或顶点）上的采样点，在两种规则下**均判定为内部**。
  由于顶点坐标为整数、采样点为半整数，该情形只会出现在穿过像素中心的斜边上
  （以及退化输入的零面积边上）。
- **水平边**：不参与射线穿越计数（由半开区间规则自然排除）。
- **顶点**：自采样点向右的射线恰好经过顶点时，按半开区间 `[yMin, yMax)` 计数，
  保证每个顶点恰好被计算一次，不会重复计数或漏计。
- **精确性**：所有坐标放大 2 倍后在整数格上计算（像素中心变为奇数整数），交点横坐标
  以精确有理数做符号比较，全程无浮点除法。坐标规模较小（放大后绝对值 ≤ 2²⁴）时使用
  `number`（仍为精确整数运算，中间量 ≤ 2⁵¹，远在 2⁵³ 安全范围内），更大时自动切换
  `BigInt`，两条路径结果完全一致。
- **填充规则**：
  - `even-odd`：向右射线与边界的穿越次数为奇数 → 内部；
  - `non-zero`：有向绕数（向上穿越 +1、向下穿越 −1）非零 → 内部。

## 安装与使用

```bash
npm install
npm run build
npm test
npm run demo
```

```ts
import { rasterize, maskToString } from 'exact-rasterize';

// 同一正方形沿同一方向绕两圈：内部绕数为 ±2
const result = rasterize(
  [
    { x: 2, y: 2 }, { x: 10, y: 2 }, { x: 10, y: 10 }, { x: 2, y: 10 },
    { x: 2, y: 2 }, { x: 10, y: 2 }, { x: 10, y: 10 }, { x: 2, y: 10 },
  ],
  { width: 13, height: 13 },
);

console.log(maskToString(result.evenOdd, 13, 13)); // 奇偶规则：内部为空
console.log(maskToString(result.nonZero, 13, 13)); // 非零规则：内部填充
console.log(maskToString(result.diff, 13, 13));    // 两规则不同的区域 = 内部
```

## API

- `rasterize(vertices, { width, height }): RasterizeResult`
  返回 `{ width, height, evenOdd, nonZero, diff }`，掩码为行优先的 `Uint8Array`（0/1），
  `diff = evenOdd XOR nonZero` 即两种规则判定不同的像素集合。
- `classifySample(vertices, x, y): SampleClassification`
  对单个采样点分类（坐标须为 0.5 的整数倍），返回 `onBoundary`、`crossingCount`、
  `windingNumber` 及两种规则下的内外判定，主要用于测试与调试。
- `maskToString(mask, width, height, on?, off?): string`
  将掩码渲染为 ASCII 字符串。

## 实现说明

核心为射线法：对每个像素中心向右发射线，与所有边做精确有理数交点比较。
水平边由半开区间 `[yMin, yMax)` 自然排除；顶点被恰好一条邻边计入；采样点恰好
落在边上时由叉积符号判定并走显式边界规则。`number` 快速路径与 `BigInt` 兜底路径
共享同一套泛型算法（`Arith<T>` 抽象），保证任意坐标规模下结果一致。
