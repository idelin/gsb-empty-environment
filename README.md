# pixel-contours

从二值掩码提取闭合像素轮廓的 TypeScript 库：前景四邻域、背景八邻域，
输出外边界与孔洞的嵌套层级，轮廓顶点全部使用整数像素边坐标，
不依赖任何浮点点内判断来修补歧义。

## 坐标与连通性约定

- 像素 `(x, y)` 占据正方形区域 `[x, x+1] × [y, y+1]`，轮廓顶点是整数角点。
- 前景按**四邻域**连通、背景按**八邻域**连通（二者互为对偶）：
  - 对角接触的前景像素 → 各自独立的轮廓；
  - 对角接触的背景 → 保持连通，不会形成伪孔洞。
- 有向边界边始终保持“前景在左手侧”：外边界在图像坐标系（y 向下）中
  为顺时针（`twiceArea < 0`），孔洞为逆时针（`twiceArea > 0`）。

## 算法

1. **有向边生成**：每个前景像素最多产生 4 条与背景相邻的有向像素边。
2. **确定性跟随**：每个顶点按“最左转弯优先 → 直行 → 右转”选择后继边。
   该配对规则是边集上的置换，天然分解为闭合环；对角接触处的配对方式
   恰好实现前景四连通 / 背景八连通，无需事后修补。
3. **层级判定**：取每条轮廓首边内侧的相邻像素作为种子（外边界为前景
   像素、孔洞为背景像素），用纯整数射线法（水平射线只与竖直边相交，
   半整数测试点永不落在边上）判定包含关系，面积最小的反向包含者即父轮廓。
4. **重建核对**：`maskFromContours` 对所有轮廓做奇偶填充，
   可与原掩码逐像素比对。

## API

```ts
import { extractContours, maskFromContours } from './src/pixel-contours.js';

const res = extractContours({ width, height, data }); // data: Uint8Array | number[]
// res.contours: Contour[] —— id / points / orientation / twiceArea /
//               parent / children / depth / seedX / seedY
const mask = maskFromContours(width, height, res.contours); // 重建掩码
```

- `points`：闭合环的拐角顶点（共线点已压缩，不重复首点）。
- `parent` / `children` / `depth`：外边界与孔洞交替的嵌套树，
  最外层外边界 `depth === 0`、`parent === null`。
- `seedX` / `seedY`：轮廓内部紧邻边界的像素，可直接用作填充种子。

## 构建与测试

本环境无网络、无 tsc，因此自带零依赖的离线类型擦除器
（`tools/strip-types.mjs`，针对受控 TS 子集，详见文件头注释）：

```sh
npm test        # 擦除类型到 dist/ 并用 Node 运行全部测试
npm run build   # 仅生成 dist/
```

若装有 TypeScript，可直接 `npm run typecheck`（`tsc --noEmit`）做完整类型检查。

测试覆盖：单像素、对角接触（前景分离 / 背景连通）、一像素宽通道与
蛇形沟槽、孔洞嵌套岛、棋盘格，以及 3x3 全枚举（512）、4x4 全枚举
（65536）和多尺寸多密度随机掩码的“提取 → 重建 → 逐像素核对”。
