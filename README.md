# rle-label-raster

多标签栅格的逐行游程（RLE）存储，TypeScript 实现，零依赖。

## 功能

- 逐行游程存储：行内相邻同标签段自动合并；跨行同值永不合并，行级校验杜绝越界段
- 矩形读写 `readRect` / `writeRect` / `fillRect`：写入在游程层面拼接，小编辑不解压整图
- 重叠区域平移 `translate`：先整体读入缓冲区再写回，严格按修改前快照语义
- 布尔 mask 编辑 `applyMask`
- 历史快照 `snapshot` / `restore`：行数组写时复制，快照间共享未修改的块（`sharedBlocks` 统计）
- 可逆补丁：每次更新产生带基线摘要（尺寸 + 块像素总数 + FNV 校验和）的 `Patch`，`applyPatch(patch, { reverse: true })` 可撤销；基线不匹配时拒绝应用
- 块索引维护像素总数，`validate()` 可检出损坏块
- 解码前先校验游程总长度与标签合法性（`DecodeError` / `LabelError`）
- 事务式写入：中途发现非法标签时全量回滚，已有快照与补丁历史不受影响

## 运行要求

Node.js >= 23.6（原生 TypeScript 类型擦除）。Node 22.6+ 可加
`--experimental-strip-types` 运行。

```sh
npm test     # node --test（内置测试运行器）
npm run demo # 可运行样例
```

## 结构

- `src/rle.ts` — 游程的校验 / 解码 / 切片 / 拼接 / 合并 / 校验和
- `src/raster.ts` — `Raster` 主类（矩形读写、平移、mask、快照、补丁、块索引）
- `src/rgba.ts` — 自建 RGBA 小图与调色板工具（验证用，无 Canvas / 图像库）
- `test/raster.test.ts` — `node:test` 测试
- `examples/demo.ts` — 端到端样例
