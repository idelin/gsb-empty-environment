# incremental-cc-labeler

对二值图维护 4/8 连通分量标签：局部像素修改后只增量更新受影响的分量，
支持合并与拆分，并保证稳定的代表 ID。纯 TypeScript 实现，不依赖 Canvas
滤镜或任何图像处理库——RGBA 图就是一个 `Uint8ClampedArray`。

## 环境

- Node.js >= 22.6（推荐 23+，直接以类型擦除方式运行 `.ts`，无需编译步骤）
- 测试使用 Node 内置 `node:test`，零第三方依赖

```sh
npm test     # node --test "test/*.test.ts"
npm run demo # node examples/demo.ts
```

## 结构

- `src/rgba-image.ts` — 自建 RGBA 小图（`RGBAImage`），含 ASCII 艺术构造函数
  `RGBAImage.fromAscii()`。前景判定：alpha >= 128 且 RGB 均值 < 128。
- `src/labeler.ts` — `ComponentLabeler`，增量连通分量标注。
- `test/labeler.test.ts` — `node:test` 测试。
- `examples/demo.ts` — 可运行的演示样例。

## API

```ts
const img = RGBAImage.fromAscii(["##.##", "##.##"]);
const labeler = new ComponentLabeler(img, 4); // 或 8 连通

img.setForeground(2, 0, true);      // 修改图像（自建 RGBA，无 Canvas）
labeler.updatePixel(img, 2, 0);     // 增量更新：合并两个分量

labeler.labelAt(x, y);              // 分量 ID，0 = 背景
labeler.componentCount;
labeler.componentSize(id);
labeler.representativeOf(id);       // 代表（锚点）像素坐标
labeler.rebuild(img);               // 全量重标（扫描序分配 ID）
```

## 稳定代表 ID 策略

- ID 取自单调递增计数器，0 表示背景；每个分量有一个锚点像素作为代表。
- **合并**（像素置为前景并连接 k 个分量）：最大的分量保留其 ID
  （并列取最小 ID），其余分量被重标号。
- **拆分**（像素置为背景导致分量断开）：包含锚点像素的子分量保留原 ID，
  其余子分量获得新 ID；若被删像素本身就是锚点，则最大子分量保留 ID。
- **无关分量不受影响**：每次更新只重写被触及分量的像素标签。

## 运行样例（`npm run demo` 输出节选）

```
== diagonal pixels, 4-connectivity (separate) ==     == diagonal pixels, 8-connectivity (joined) ==
A.                                                   A.
.B                                                   .A
A=id1(size=1)  B=id2(size=1)                         A=id1(size=2)

== initial image, 4-connectivity ==
AA.BB....
AA.BB....
.........
.......CC
.......CC
A=id1(size=4)  B=id2(size=4)  C=id3(size=4)

>> paint bridge pixel (2,0): blocks A and B merge
== after adding bridge (2,0) ==
AAAAA....
AA.AA....
.........
.......BB
.......BB
A=id1(size=9)  B=id3(size=4)          <- A、B 合并为 id1，C 的 id3 不变

>> erase bridge pixel (2,0): component splits
== after removing bridge (2,0) ==
AA.BB....
AA.BB....
.........
.......CC
.......CC
A=id1(size=4)  B=id4(size=4)  C=id3(size=4)   <- 锚点侧保留 id1，新侧得 id4，C 仍为 id3
```

## 测试结果（`npm test`，Node v23.9.0）

```
✔ RGBA foreground predicate: opaque dark = on, transparent/light = off
✔ 4-connectivity: diagonal pixels stay in separate components
✔ 8-connectivity: diagonal pixels form one component
✔ removing a bridge pixel splits the component in two
✔ adding a bridge pixel merges components; the larger keeps its ID
✔ unrelated components keep stable IDs across local edits
✔ splitting off the representative pixel hands the ID to the largest part
✔ isolated pixel add/remove creates and retires component IDs
✔ updatePixel reports no change when the image matches tracked state
✔ 8-connectivity: removing a diagonal hinge splits the component
✔ rebuild reassigns deterministic scan-order IDs
ℹ tests 11  ℹ pass 11  ℹ fail 0
```
