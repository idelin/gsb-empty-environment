# binary-contours

从二值图提取外轮廓与洞，输出**闭合、无重复顶点的游走环**及像素边界坐标。
纯 TypeScript 实现，零依赖，不使用 Canvas 滤镜或任何图像处理库；
验证用 RGBA 小图由代码手工构建（`src/rgba-image.ts`）。

## 算法

1. **二值化**（`src/mask.ts`）：RGBA 像素 alpha > 0 且亮度（Rec.601）≥ 阈值
   判为前景。
2. **裂缝跟踪**（`src/contours.ts`）：每个前景像素在与背景/图外相邻的边上
   发出一条有向边，方向保证前景始终位于行进方向右侧。每个顶点入度等于出度；
   在对角接触产生的二度顶点处，按"最急右转"（右转 > 直行 > 左转）固定配对，
   等价于前景 4-连通、背景 8-连通，因此：
   - 对角接触的像素拆成各自独立的简单环，顶点不重复；
   - 对角接触的背景不会形成"伪洞"；
   - 贴边对象的环沿图像边界闭合。
3. **方向判别**：外轮廓为顺时针（y 向下的屏幕坐标），`signedArea > 0`；
   洞为逆时针，`signedArea < 0`。
4. **填回校验**（`src/fill.ts`）：对所有环计算像素中心的环绕数
   （winding number，外环 +1、洞 -1），非零即为前景，可逐像素还原原图。

## 输出结构

```ts
interface Ring {
  kind: "outer" | "hole"; // 外轮廓 / 洞
  closed: true;           // 闭合环：末顶点连回首顶点
  vertices: Point[];      // 网格线坐标 (0..w, 0..h)，无重复顶点
  pixels: Point[];        // 沿边界的前景像素坐标（游走顺序）
  signedArea: number;     // 外环为正、洞为负
  perimeter: number;      // 边数
}
```

## 运行

需要 Node.js ≥ 23.6（原生类型擦除运行 TypeScript，无需编译与任何依赖）。

```sh
npm test        # node --test，运行内置测试
npm run demo    # node examples/demo.ts，运行演示样例
```

## 运行样例（`npm run demo`）

输入为手工构建的 16x12 RGBA 图：带 1x2 洞的矩形、贴上/右边缘的长条、
一对对角接触的像素。

```
input mask (# = foreground):
.........#######
.######..#######
.##..##.........
.######.........
.######.........
.######...#.....
...........#....
................

extracted 5 closed ring(s):
#0 outer  area=14  perimeter=18     # 贴边长条
#1 outer  area=30  perimeter=22     # 矩形外轮廓
#2 hole   area=-2  perimeter=6      # 矩形内的洞（逆时针，面积为负）
#3 outer  area=1   perimeter=4      # 对角像素 1
#4 outer  area=1   perimeter=4      # 对角像素 2

OK: filling the rings reproduces the original mask
```

## 测试结果（`npm test`，Node v25.5.0）

```
✔ empty image yields no contours
✔ RGBA to mask: opaque bright = foreground, transparent/dark = background
✔ single pixel forms a closed ring of four edges
✔ solid block yields one outer ring with correct area and perimeter
✔ ring direction distinguishes holes from outer contours
✔ diagonal contact splits into separate simple rings, no phantom hole
✔ objects touching the image edge are closed along the border
✔ concave shape with a hole round-trips exactly
✔ multiple disjoint objects each get their own outer ring
ℹ tests 9
ℹ pass 9
ℹ fail 0
```

覆盖要点：空图无轮廓；单像素形成四边闭合环；环方向区分外轮廓与洞；
对角接触拆分且无伪洞；贴边对象沿边界闭合；所有用例填回后与原图逐像素一致。

## 文件结构

- `src/mask.ts` — RGBA → 二值掩码、掩码比较与 ASCII 渲染
- `src/contours.ts` — 裂缝跟踪轮廓提取（核心）
- `src/fill.ts` — 环填回（环绕数光栅化，用于验证）
- `src/rgba-image.ts` — 手工构建 RGBA 测试图的小工具
- `test/contours.test.ts` — Node.js 内置测试（`node:test`）
- `examples/demo.ts` — 演示样例
