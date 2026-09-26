# CPU 图层合成内核

纯 TypeScript / Node.js 实现的 CPU 图层合成器，不依赖 Canvas 滤镜或任何图像处理库。
利用 Node 23 内置的 TypeScript 类型擦除直接运行，测试使用 Node 内置测试运行器。

## 运行

```sh
npm test    # node --test 'test/*.test.ts'
```

## 架构

- `src/color.ts` — sRGB ↔ 线性光转换函数（IEC 61966-2-1 传递曲线）。
- `src/pixels.ts` — 固定大小 RGBA float32 瓦片缓冲池，统计实际分配（`allocations`）、
  池复用（`reuses`）与存活数（`live`）。
- `src/layer.ts` — 图层模型：独立色彩空间（`srgb`/`linear`）、预乘标记、裁剪矩形、
  偏移、不透明度、混合算子（`source-over`/`lighter`/`multiply`）、依赖边（`inputs`）。
  `layerBounds` 给出裁剪+偏移后与画布求交的保守包围盒。
- `src/graph.ts` — 把图层集合编译成合成计划：DFS 三色标记拒绝循环依赖；按
  “插入序取首个依赖就绪者”拓扑定序；任一图层为线性光则整体在线性空间合成，
  否则直接 sRGB；按需标记颜色转换（`convert`）；已预乘图层 `premultiply` 恒为
  false——绝不重复预乘。
- `src/compositor.ts` — 瓦片化渲染与版本化发布：
  - 编辑图层（`upsertLayer`/`removeLayer`）记录旧+新包围盒为保守脏区；
  - `commit()` 只重绘脏区覆盖的 tile，干净 tile 以引用计数共享给新版本；
  - 渲染全部在新缓冲上完成，任何中途失败（如未知色彩空间转换抛错）都会归还
    已分配缓冲并放弃发布——原子提交，绝无半幅图；
  - 每次提交产生不可变版本，旧快照可随时 `readPixel`/`snapshotPixels` 读取，
    `releaseVersion` 引用计数归零后缓冲回池复用。

## 测试与实际运行结果

`test/` 下 9 个用例全部通过（Node v23.9.0）：

- `test/blend.test.ts`
  - 全透明红叠蓝（straight 与 premultiplied 两种来源）逐像素断言红色通道为 0，
    蓝底完好——预乘路径不会把透明红“染”到结果里。
  - 相同输入（50% 白叠黑）两条路径实测：sRGB 直接混合 = 0.5000，
    线性光混合 = 0.7354，差异 > 0.2，明确不同。
- `test/graph.test.ts` — 循环依赖被拒绝且不发布版本；依赖边决定合成顺序；
  未知依赖/混合算子抛错；已预乘图层不重复预乘、颜色转换按需插入。
- `test/dirty.test.ts` — 8x8 画布 4 tile：移动半透明层后仅重绘 2/4 tile，
  旧位置恢复纯背景，新位置重叠层按叠序重算，v1 旧快照仍可读。
- `test/atomic.test.ts` — 坏色彩空间图层在第二个 tile 渲染中途抛错：当前版本
  不前进、存活缓冲数回到提交前（无泄漏）、旧快照未被部分写入污染；移除坏
  图层后可正常恢复提交。
- `test/reference.test.ts` — 8 组随机场景（随机画布 5~15、随机 tile 1/2/3/4/8、
  随机色彩空间/预乘/裁剪/混合/依赖），三个连续版本逐一与独立逐像素参考实现
  （`test/helpers.ts` 的 `referenceRender`，不复用合成器代码）比对一致
  （容差 1e-3）；重绘 tile 数精确等于测试侧独立计算的保守脏区；释放旧快照后
  缓冲被复用（`reuses > 0`）。

最近一次运行诊断输出：

```text
✔ 9 tests, 0 fail
50% 白叠黑: sRGB=0.5000, linear-light=0.7354
移动后重绘 tile: 2/4
恢复提交重绘 tile: 2/2
trial 0: 11x8  tile=8 layers=5 repaint v2=2/2  v3=1/2  alloc=5   reuse=1 live=3
trial 1: 5x12  tile=4 layers=5 repaint v2=6/6  v3=1/6  alloc=13  reuse=2 live=8
trial 2: 5x15  tile=3 layers=5 repaint v2=4/10 v3=2/10 alloc=18  reuse=6 live=18
trial 3: 12x10 tile=2 layers=2 repaint v2=7/30 v3=2/30 alloc=39  reuse=3 live=33
trial 4: 7x14  tile=8 layers=4 repaint v2=1/2  v3=1/2  alloc=4   reuse=1 live=3
trial 5: 5x6   tile=3 layers=5 repaint v2=1/4  v3=2/4  alloc=7   reuse=2 live=6
trial 6: 13x6  tile=1 layers=4 repaint v2=6/78 v3=3/78 alloc=102 reuse=9 live=102
trial 7: 11x8  tile=4 layers=5 repaint v2=2/6  v3=2/6  alloc=12  reuse=4 live=12
```
