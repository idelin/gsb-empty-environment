# tile-pyramid：逐层降采样瓦片金字塔（TypeScript / 零依赖）

从空仓库构建的瓦片金字塔：2×2 盒式逐层降采样、局部编辑的祖先链失效、
带字节额度的 LRU 缓存、MVCC 读快照隔离、并发去重与取消释放。
不使用 Canvas 滤镜或任何图像处理库；验证图全部为程序自建的 RGBA 小图。

## 运行环境

- Node.js ≥ 23.6（原生运行 TypeScript），或 ≥ 22.6（需 `--experimental-strip-types`）。
- 无任何 npm 依赖；测试使用 Node 内置 `node:test`。

```bash
npm test    # node --experimental-strip-types --test test/pyramid.test.ts
npm run demo
```

## 设计

**层与瓦片**（`src/pyramid.ts`）
- 第 L 层尺寸 `ceil(W/2^L) × ceil(H/2^L)`，直到 1×1；3×3 图 -> 3×3 -> 2×2 -> 1×1。
- 瓦片边长 `tileSize`（默认 4），边缘瓦片按实际尺寸存储，精确覆盖。
- 降采样权重 = 2×2 块被图像边界裁剪后的实际像素数（1/2/4），边缘不补零、不变暗。
- 高层瓦片由父层瓦片拼接源区域后逐像素计算，天然跨层选瓦片。

**版本链 + 代际（MVCC）**
- 每次 `setPixel` 推进全局代际 `generation`，仅对每一层包含该像素投影的
  瓦片放置“墓碑”版本（祖先链），其余分支的版本完全不动。
- 每个瓦片键保存按代际升序的版本链；读按快照代际可见性查询：
  快照后计算但其间无墓碑的版本同样可用。
- 瓦片缓冲不可变，读快照期间所需旧版本被“钉住”，LRU 不得淘汰 ——
  局部改动的祖先失效与读快照原子隔离；旧版本不可得时抛 `SnapshotExpiredError`。

**并发与取消**
- 同一瓦片的并发请求共享一个在飞任务（引用计数），只计算并发布一次，
  所有等待者拿到同一缓冲。
- 任务完成时若该瓦片已出现更高代际墓碑（旧代际任务），结果直接丢弃，
  旧像素不会复活；调用方自动按当前代际重试。
- 所有等待者取消（AbortSignal）后任务中止，已分配的临时缓冲立即释放并
  计入 `releasedBytes` / `cancels`。

**缓存额度与统计**
- `maxCacheBytes` 字节额度，超限按 LRU 淘汰未钉住版本；
  统计 `hits/misses/computes/evictions/invalidations/cancels/releasedBytes/currentBytes/peakBytes`。

**区域读**
- `readRegion(level,x,y,w,h)`：按快照代际跨瓦片拼装，矩形裁剪到层边界，
  只复制相交片段（精确边缘覆盖）。
- `readScaled(x,y,w,h,outW,outH)`：自动选择分辨率不低于输出的最粗层取瓦片，
  再最近邻重采样。

**独立参考核验**（`src/image.ts`）
- `downsampleReference` / `referencePyramid` 是与瓦片管线分离的整图逐像素
  直接实现，测试对多种奇数尺寸 × 多种 tileSize × 全部层做逐字节比对。

## 运行样例（`npm run demo`，9×7 渐变图）

```
== 层尺寸（ceil(n / 2^L)）==
9x7 -> 5x4 -> 3x2 -> 2x1 -> 1x1

== L1 红色通道（5x4，右/下边缘按实际覆盖加权）==
   14  70 126 182 224
   14  70 126 182 224
   14  70 126 182 224
   14  70 126 182 224

== 与独立参考实现逐字节比对 ==
L0..L4: MATCH 全部层一致

== 局部编辑 (8,6) -> 红色，仅失效祖先链 ==
失效瓦片: 0:2:1 1:1:0 2:0:0 3:0:0 4:0:0
本次读取重算瓦片数: 2（祖先链长度 5，其余分支未动）

== 跨层区域读：基准层 (0,0,9,7) 输出 3x2 ==
选取层: L2（3x2）

== 缓存/并发统计 ==
hits=22 misses=14 computes=14 evictions=0
invalidations=5 cancels=0 releasedBytes=0
currentBytes=420 peakBytes=420
```

## 测试结果（`npm test`，14 项全部通过）

```
✔ 层尺寸按 ceil(n / 2^L) 取整（3x3 与 7x5）
✔ 奇数尺寸边缘权重：纯白图各层不变暗
✔ 奇数尺寸边缘权重：单点亮度按实际覆盖像素加权
✔ 瓦片管线与独立逐像素参考实现一致（奇数尺寸多层传播）
✔ 局部编辑只失效祖先链，不相关分支不重建
✔ 读快照与编辑失效原子隔离：进行中的读看到旧像素
✔ 固定快照在旧版本不可得时抛 SnapshotExpiredError
✔ 并发生成同一瓦片只计算并发布一次
✔ 旧代际任务不能复活旧像素
✔ 缓存额度：LRU 淘汰、峰值统计
✔ 缓存 LRU：最近访问的瓦片不被淘汰
✔ 取消：所有等待者取消后任务中止、缓冲释放、统计入账
✔ 任意区域读：跨瓦片精确边缘覆盖 + 越界裁剪
✔ 跨层选择：readScaled 选取分辨率足够的最粗层
ℹ pass 14  fail 0
```
