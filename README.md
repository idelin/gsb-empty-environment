# contour-extract

整数标量网格的等值线提取库（marching squares），TypeScript 实现，零依赖。

## 规则约定

- **内侧判定**：严格大于阈值（`value > threshold`）的格点视为内侧。
- **交点**：网格边上的交点参数 `t = (h - v0) / (v1 - v0)` 以 `Rational`
  （bigint 分数，自动约分）精确保存；等值顶点处的交点恰好落在顶点上
  （`t = 0/1`），并按有理坐标全局去重。
- **共享边**：每条网格边至多求值一次，交点被两侧单元共享，只生成一次
  （见 `stats.edgeInterpolations`）。
- **鞍点**：case 5 / 10 按双线性中心值 `c = (v00+v10+v01+v11)/4` 决胜，
  与阈值的比较用整数精确完成（`sum·den` 对 `4·num`）；`c == h` 时使用
  固定对角规则——始终沿 TL–BR 对角连接（线段贴 TR、BL 角），保证互补
  情形（5 与 10）产生完全一致的几何。
- **退化防护**：两个交点坍缩到同一等值顶点时丢弃零长线段
  （`stats.degenerateSegmentsDropped`）；平坦边两侧区域产生的重合等值
  线段只保留一份（`stats.duplicateSegmentsDropped`），不会产生悬空或
  重复线段。
- **拼接**：线段按端点邻接贪心拼接为开链（`closed: false`）或环
  （`closed: true`）；环不重复首尾点。

## 用法

```ts
import { extractContours } from "./src/index";

const result = extractContours(
  { width: 3, height: 3, data: [0,0,0, 0,4,0, 0,0,0] },
  2, // 阈值：整数、bigint 或 Rational
);
// result.contours -> [{ closed: true, points: [{x: 1/2, y: 1}, ...] }]
// result.points / result.segments -> 去重后的交点与线段
// result.stats -> 求值与去重计数
```

坐标以 `Rational` 表示，`toString()` 得 `"3/2"`，`toNumber()` 转浮点。

## 测试

```sh
bun test
```

覆盖：共享边交点唯一性、有理坐标精确性、等值顶点与平坦边的退化/重复
防护、鞍点三种决胜（中心高于/低于/等于阈值）、环与开链拼接，以及
600 组随机网格的结构不变量（点唯一、线段唯一、拼接恰好消费全部线段、
无精确命中时奇度顶点只出现在边界）。
