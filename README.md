# rle-raster

多标签栅格的逐行游程（RLE）存储，TypeScript 实现，零依赖。Node.js ≥ 23.6 直接以类型剥离方式运行 `.ts`，无需编译或安装。

## 功能

- 逐行游程存储：行内相邻同标签段自动合并；跨行同值绝不合并成越界段（每行游程长度和恒等于图宽）。
- 矩形读写 `readRect` / `writeRect`、重叠区域平移 `translateRegion`（先整体读修改前像素再写，原地重叠安全）、布尔 mask 编辑 `applyMask`。
- 块索引：行按 `blockRows` 分块，每块维护像素总数与逐标签计数；`validate()` 校验游程和与索引一致性，可发现损坏块。
- 历史快照：`snapshot()` / `restoreSnapshot()` 基于不可变块结构共享；`compareWithSnapshot()` 统计共享块数。
- 可逆补丁：每次修改返回 `Patch`，基线摘要含尺寸、序号、受影响行数及修改前后行哈希；`applyPatch(p)` / `applyPatch(p, true)` 应用与逆应用，基线不匹配抛 `BaselineMismatchError`。
- 解码校验：`decodeRow` 先校验游程长度与标签合法性再展开；写操作事务化，中途发现非法标签整体回滚，旧快照不受影响。
- 小编辑只解码受影响行（`stats.rowsDecoded` 可观测），不解压整图。

## 运行

```sh
npm test     # node --test（内置测试运行器）
npm run demo # 可运行样例：RGBA 小图 -> 标签 -> 编辑 -> 逐像素比对
```

## 结构

- `src/raster.ts` — 核心：游程编解码、RasterImage、补丁、快照、块索引。
- `src/rgba.ts` — 自建 RGBA 小图构造与 palette 互转（无 Canvas / 图像库）。
- `src/refgrid.ts` — 朴素二维数组参考模型，用于逐像素比对。
- `test/raster.test.ts` — 14 个用例：合并规则、块索引、小编辑解码行数、重叠平移、回滚、零宽区域、极长行、损坏块、逆补丁、解码校验、共享块统计、随机操作对拍。
- `examples/demo.ts` — 端到端样例。
