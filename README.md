# rational-scale-ticks

TypeScript 刻度库：以**精确有理数**（bigint 分子/分母）为定义域，映射到像素范围，支持线性与对数两种模式。

## 特性

- **有理定义域**：域值以 `Rational`（`num`/`den`，恒约分、`den > 0`）精确表示；`rational(0.1)` 恰好是 1/10。
- **线性 / 对数模式**：`mode: 'linear' | 'log'`。
- **对数模式显式拒绝非正值**：创建时域端点 ≤ 0 抛 `LogDomainError`；`map`/`clamp` 遇到非正值同样抛出。
- **逆映射与裁剪分离**：`invert` 从不裁剪，越界像素得到越界域值；`clamp` / `clampPixel` 是独立操作。
- **倒置像素范围合法**：`range: [500, 100]` 使映射方向翻转；倒置定义域亦可。
- **刻度标签不重复**：标签是精确值的规范字符串（可终止小数或 `num/den`），不同值必得不同标签，并有防御性重复检查。刻度返回 `{ value, label, pixel }`，其中 `value` 是精确原值。

## 用法

```ts
import { createScale, makeRational } from 'rational-scale-ticks';

const scale = createScale({
  domain: [makeRational(1n, 3n), makeRational(22n, 7n)],
  range: [0, 600],
  mode: 'linear',
});

scale.map(makeRational(1n, 2n));   // -> 像素（不裁剪）
scale.invert(300);                 // -> 精确 Rational（不裁剪）
scale.clamp(scale.invert(9999));   // -> 裁剪是独立步骤

for (const tick of scale.ticks(10)) {
  console.log(tick.label, tick.pixel, tick.value); // 显示标签 + 精确原值
}
```

## 精确性说明

- 线性 `invert` 全程使用精确有理运算（像素 double 按其二进制值精确转为有理数）。
- 对数 `invert` 经过 `exp/log`，本质上是近似的，返回最接近结果 double 的最短十进制有理数。
- 端点特判保证 `map(d0) === p0`、`invert(p0) === d0` 等边界严格相等。

## 构建与测试

```sh
npm install   # 安装 devDependency typescript
npm test      # tsc 编译并运行 test/ 下的测试
```

测试覆盖：线性/对数往返（含 1e-30、1e-300、相对跨度 1e-9 的极小跨度）、单调性（含倒置像素范围）、边界端点、非正值拒绝、标签唯一性与刻度值精确性。
