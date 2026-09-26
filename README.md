# spill-tile-convolver

面向"内存预算受限"场景的 RGBA (float32) 栅格卷积器, 纯 TypeScript 实现, 不依赖 Canvas 滤镜或任何图像处理库。

## 特性

- **可分离核串联**: 任意级数, 奇/偶长度核, 每级独立锚点 (anchor) 与 X/Y 边界策略 (`clamp` / `mirror` / `wrap` / `zero`)
- **正确的 halo 推导**: 融合执行的总 halo = 各级 halo 之和; 区域经 `mappedInterval` 只在图像真实边缘应用边界策略, tile 边界绝不当作图像边界
- **两种执行方式**: `fused` (单 tile 一次取齐链式 halo, 不物化中间图) 与 `stepwise` (每级 H/V 中间图物化到可溢写 store, 每步只需自身 halo)
- **可溢写 tile 缓存**: 内存 = 缓存 + scratch 预留, 超预算时 LRU 溢写到临时文件, 按需回读
- **核热替换**: 版本号递增 + 惰性局部重算; 计算期间换核抛 `VersionConflictError`, 部分结果作废, 重试不混用新旧版本
- **增量重算**: 修改单个输入 tile, 按链式 halo (含 wrap 对侧条带) 精确传播脏 tile, 只重算受影响输出
- **取消/失败清理**: 释放缓存并删除全部临时溢写文件; 只有成功才原子发布根索引 (tmp + rename)
- **预算守卫**: 预算 < 最小 halo 需求时抛 `BudgetError` 明确拒绝; 统计计算量 (MACs) 与峰值内存

## 运行

需要 Node ≥ 22.18 (原生 TypeScript 类型擦除), 推荐 23.6+。

```sh
npm test       # node --test (内置测试运行器)
npm run demo   # 可运行样例: 参考对比 / 溢写 / 增量 / 预算拒绝 / 统计输出
```

## 关键设计

- `out(x) = Σ_j k[j] · in(x + j - anchor)`, 单级 halo = `(anchor, len-1-anchor)`
- 融合区域规划 (`planChain`): 从输出 tile 反向逐级外扩, 每级先 Y 后 X, 经 `mappedInterval` 把越界采样区间映射回图像域包围盒 (wrap/mirror 会正确跳到对侧/反射侧)
- 峰值内存上界 = `minBudgetBytes` = halo 窗口 scratch (输入+输出缓冲) + 2 个 tile 缓存; 运行时 `Stats.peakBytes` 实测不超过该预算
