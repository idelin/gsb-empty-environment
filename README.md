# LSM 读放大 / Bloom Filter 误判率监控诊断器

面向 LSM 存储引擎的性能度量诊断器，TypeScript 实现，零第三方依赖：

- **读放大度量**：统计每次 `Get` 实际读取（打开并检索）的 SSTable 数量；
- **误判率度量**：统计 Bloom Filter 的 False Positive 比率（误判次数 / 键实际不存在的判定次数）；
- **动态建议**：滑动窗口越限时即时告警 —— 读放大过高 → 建议提高 Compaction 并发/权重压制读延迟；误判率过高 → 建议重建 Bloom Filter；
- **故障注入**：`SSTable.disableBloomFilter()` 可故意关闭部分 Filter，用于验收监控灵敏度。

## 环境要求

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，测试使用内置 `node:test`，无需任何依赖安装）。

```bash
npm run demo   # 运行演示
npm test       # 运行内置测试
```

## 结构

| 文件 | 职责 |
| --- | --- |
| `src/bloomFilter.ts` | Bloom Filter（FNV-1a 双哈希，只误判不漏判） |
| `src/sstable.ts` | SSTable：键区间 + 可关闭/重建的 Bloom Filter |
| `src/engine.ts` | 简化 LSM 引擎：MemTable + L0 表链，Get 全路径追踪 |
| `src/monitor.ts` | 滑动窗口诊断器：读放大 / 误判率告警与处置建议 |
| `src/scenario.ts` | 验收场景构造（种子化，可复现） |
| `src/demo.ts` | 三阶段演示：健康 → 故障注入 → Compaction 恢复 |
| `test/` | `node:test` 单元测试 + 验收测试 |

## 运行样例（`npm run demo` 实际输出）

```
=== LSM 读放大 / Bloom Filter 误判率监控演示 ===

写入 5400 个随机散列 Key，形成 9 张 L0 SSTable（全部启用 Bloom Filter）

阶段 A：Bloom Filter 全部启用，执行 3000 次随机 Get …
  [阶段A] 全阶段平均读放大=1.03  监控窗口(最近500次): 读放大=1.03  Bloom误判率=0.60%

>>> 故障注入：故意关闭 7/9 张 SSTable 的 Bloom Filter

阶段 B：同样负载再执行 3000 次随机 Get …
  ⚠️  [WARN] HIGH_READ_AMPLIFICATION: 读放大 2.00 超过阈值 2.00（每次 Get 平均实际读取 2.00 个 SSTable）
      👉 建议提高 Compaction 并发/权重（如 compactionConcurrency+1）以加速 SSTable 合并、压制读延迟；同时检查是否存在被关闭的 Bloom Filter 并重建
  （监控在阶段 B 第 158 次查询时即时捕捉到指标跃升）
  [阶段B] 全阶段平均读放大=4.25  监控窗口(最近500次): 读放大=4.40  Bloom误判率=0.96%

>>> 处置：按建议执行 Compaction 合并 SSTable，并重建 Bloom Filter …

  Compaction 完成：SSTable 数量=1，被关闭的 Filter=0

阶段 C：恢复后再执行 3000 次随机 Get …
  [阶段C] 全阶段平均读放大=1.00  监控窗口(最近500次): 读放大=1.00  Bloom误判率=0.00%

=== 演示结束 ===
```

读放大从 ≈1.05（实测 1.03）跃升至 ≈4.2（实测 4.25），监控在故障注入后第 158 次查询即越限告警并建议提高 Compaction 权重；按建议合并 SSTable 并重建 Filter 后读放大回落至 1.00。

## 测试结果（`npm test`）

```
✔ 已添加的键绝不漏判（无 false negative）
✔ 误判率维持在理论上界内（10 bits/key → <3%）
✔ 提高 bitsPerKey 可降低误判率
✔ 写入后可读回，且新值覆盖旧值
✔ Bloom Filter 启用时读放大接近 1，关闭后显著上升
✔ 追踪指标：Bloom 否定不计入读放大，误判计入 falsePositives
✔ compactAll 合并为单张 SSTable 且数据不丢失
✔ 健康负载不触发告警
✔ 读放大越限触发 Compaction 建议告警（边沿触发，仅一次）
✔ 误判率越限触发重建 Filter 建议告警
✔ 样本不足时不评估
✔ 滑动窗口：恢复健康后指标回落
✔ 验收：关闭部分 Bloom Filter 后读放大跃升并触发 Compaction 告警

ℹ tests 13
ℹ pass 13
ℹ fail 0
```

## 监控规则

| 告警码 | 触发条件（滑动窗口，默认 500 样本） | 建议 |
| --- | --- | --- |
| `HIGH_READ_AMPLIFICATION` | 平均读放大 > 2.0（>4.0 升级 critical） | 提高 Compaction 并发/权重，压制读延迟 |
| `HIGH_BLOOM_FP_RATE` | 误判率 > 5%（>10% 升级 critical） | 重建 Bloom Filter（提高 bitsPerKey） |

告警为边沿触发并带 80% 迟滞，指标越限时只报告一次，回落前不重复刷屏。
