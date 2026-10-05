# pixel-contours

从二值掩码提取闭合像素轮廓的 TypeScript 库：裂缝（crack）追踪，输出外边界与
孔洞的嵌套层级，轮廓使用整数像素边（格点）坐标，全链路整数运算。

## 约定

- **连通性**：前景按四邻域连通，背景按八邻域连通。这是 Jordan 曲线定理在
  离散网格上成立的标准配对：对角接触的前景属于不同分量（各自成环），对角
  缝隙允许背景穿过（不产生孔洞）。
- **坐标系**：像素 `(x, y)` 占据 `[x, x+1] × [y, y+1]`，y 轴向下。轮廓顶点
  是像素角点的整数坐标。
- **方向**：追踪时前景始终在行进方向左侧，因此外边界二倍有向面积
  `signedArea2 < 0`，孔洞 `signedArea2 > 0`。
- **歧义消解**：对角接触顶点处有两条候选出边，按确定性的转向规则
  `(dx, dy) -> (dy, -dx)` 配对，使两条对角前景各自成环。规则是纯组合式的，
  不依赖任何浮点点内判断。

## API

```ts
import { extractContours, traceContours, buildHierarchy, rasterize, masksEqual } from 'pixel-contours';

const mask = {
  width: 3,
  height: 3,
  data: new Uint8Array([
    1, 1, 1,
    1, 0, 1,
    1, 1, 1,
  ]),
};

const { contours, nodes, roots } = extractContours(mask);
// contours[0]: 外边界（isHole=false, signedArea2=-18）
// contours[1]: 孔洞  （isHole=true,  signedArea2=+2, nodes[i].parent 指向外边界）

const rebuilt = rasterize(contours, mask.width, mask.height);
masksEqual(rebuilt, mask); // true
```

- `traceContours(mask): Contour[]` — 仅提取轮廓。
- `buildHierarchy(contours): Hierarchy` — 仅构建嵌套层级（`parent/children/depth`）。
- `extractContours(mask): ContourSet` — 一步完成以上两者。
- `rasterize(contours, width, height): Mask` — 扫描线奇偶填充，由轮廓重建掩码。
- `masksEqual(a, b): boolean` — 掩码比较。

层级中 `depth` 为嵌套深度：0 外边界、1 孔洞、2 孔中岛，依此类推。
点内判断使用二倍整数坐标（像素中心 / 边中点）做射线法，奇偶性保证不会
落在边或顶点上，无浮点退化。

## 构建与测试

本仓库源码只使用“可擦除类型”子集。有 tsc 的环境：

```sh
npm run build:tsc   # 使用 tsconfig.json 编译
```

无 tsc 的环境（如离线 CI）可用内置的极简类型擦除器：

```sh
npm test            # node scripts/strip-types.mjs && node test/run.mjs
```

测试覆盖：单像素、对角接触、一像素宽前景/背景通道、孔洞与孔中岛层级、
非法输入，以及随机掩码的性质测试（重建核对、前景四连通/背景八连通分量
计数一致性、面积守恒）。
