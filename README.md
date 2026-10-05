# incremorph

纯 TypeScript 的灰度形态学 + 增量操作图引擎。零运行时依赖，不调用 Canvas
滤镜或任何现成图像处理库；验证全部基于自建 RGBA 小图。需要 Node >= 22.6
（利用原生 type-stripping 直接运行 `.ts`，开发环境为 Node 23.9）。

## 功能

- **自定义结构元素**（`src/se.ts`）：任意点集 + 独立原点。膨胀按"绘制方向"
  盖章（`out(x) = max_k in(x - k)`），腐蚀取对偶（`out(x) = min_k in(x + k)`），
  非对称 SE 方向严格保持。
- **膨胀 / 腐蚀 / 开 / 闭**（`src/ops.ts`）：每阶段独立的边界模式
  （`zero` / `max` / `clamp` / `skip`）与标签掩码（label=0 的像素直通）。
  默认边界为对偶对（膨胀补 0、腐蚀补 255），保证 **开不增前景、闭不减前景**。
- **掩码约束的地质重建**（`src/reconstruct.ts`）：膨胀型重建
  `f -> min(dilate(f), mask)` 的最大不动点，结果 **永不超出 mask**。
  逐次松弛传播，不受核半径限制；`budget` 限制扫描轮数，未收敛时如实报告。
- **操作图编译**（`src/graph.ts`）：`compileGraph` 拓扑排序并校验引用/环；
  阶段类型为 `dilate | erode | open | close | reconstruct`，各阶段有独立的
  边界、原点（SE 内）与标签掩码定义。
- **增量维护**：`Engine.setPixel` 后，局部算子只重算按 SE 半径扩张的脏窗口；
  重建阶段区分两种路径——
  - 输入仅增大：从旧不动点继续松弛（单调增长）；
  - 输入出现减小：在"旧 mask ∪ 新 mask"支撑集的受影响连通分量内从 marker
    重算，**撤销失效的传播支持**（如断开通道后远端回退），不触碰域外像素。
  重建预算耗尽时 **保留原发布版本**（事务性发布，报告 `budgetExceeded`）。
- **瓦片缓存上限**（`src/tiles.ts`）：所有源图与阶段输出存于瓦片存储，
  读写全部经过一个有 `maxTiles` 上限的 LRU 缓存（脏瓦片写回后备存储）。
  重建不动点传播以像素坐标为单位推进，缓存再小也只会变慢、不会截断。
- **参考核验**（`src/reference.ts`）：整图直接求值作为 ground truth，
  测试在每次编辑后逐阶段比对增量引擎与参考完全一致。

## 运行

```sh
nvm use            # 或任何 >= 22.6 的 node
npm test           # node --test（Node 内置测试运行器）
npm run demo       # 可运行样例 examples/demo.ts
```

## 样例场景

`examples/demo.ts` 构建 48x14 RGBA 场景：一条 2px 高、被 1px 缺口断开的长
通道 + 孤立噪点。图为 `open(非对称角形SE) -> reconstruct(marker=种子, mask)`：

1. 全量运行：开运算去噪，重建从种子传播到缺口为止（报告收敛与扫描轮数）；
2. 两个像素填平缺口：影响传播到 36px 外的远端（远超核半径 1）；
3. 再次断开：旧传播支持被撤销，远端回退，结果与全量参考一致；
4. 用极小预算重复该编辑：报告预算未完成，保留原发布版本；
5. 打印瓦片缓存统计（resident 始终 <= maxTiles）。

## 测试

`test/` 下 17 个用例（Node 内置 `node:test`）：

- `morphology.test.ts`：非对称 SE 方向（膨胀/腐蚀）、与独立朴素参考逐像素
  一致、四种边界模式、开/闭的单调性与幂等性、标签直通、RGBA 亮度往返；
- `reconstruct.test.ts`：传播距离远超核半径且不超 mask、缺口精确阻断、
  不动点等式验证、预算未完成的报告、标签冻结；
- `incremental.test.ts`：单像素打通/断开长通道（远端更新与撤销）、预算
  失败保留旧版本、多阶段图逐次编辑后与全量参考逐阶段一致、
  `maxTiles=1` 时结果仍正确且发生逐出、RGBA 端到端、图编译错误检测。
