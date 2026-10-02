# LSM 读放大与 Bloom Filter 误判率监控器

LSM 存储引擎性能度量诊断器（TypeScript，零依赖，Node.js 内置测试）：

- 统计每次 `Get` 实际读取的 SSTable 数量（**读放大**）
- 统计 Bloom Filter 的 **False Positive 比率**
- 指标越限时即时告警，并动态建议：**提高 Compaction 并发/权重** 或 **重建 Bloom Filter**

## 环境要求

Node.js ≥ 23.6（原生 TypeScript 类型擦除，无需编译、无第三方依赖）。

## 结构

- `src/bloomFilter.ts` — Bloom Filter（FNV-1a + murmur3 终结混合 + 双哈希）
- `src/sstable.ts` — SSTable，支持运行时关闭/重建 Bloom Filter
- `src/lsmEngine.ts` — 极简 LSM 引擎：MemTable → flush → L0 SSTable → compaction
- `src/monitor.ts` — 滑动窗口诊断器：读放大 / FP 率统计、告警与调优建议
- `src/demo.ts` — 验收场景演示
- `test/monitor.test.ts` — `node:test` 测试

## 运行

```bash
npm run demo    # 或 node src/demo.ts
npm test        # 或 node --test test/*.test.ts
```

## 运行样例（`npm run demo`）

```
=== 写入 512 个随机散列 Key（8 张 SSTable） ===
  SSTable 数量: 8

=== 阶段一：Bloom Filter 开启，执行 200 次 Get ===
  读放大 = 1.06，Bloom 误判率 = 1.49%

=== 阶段二：故意关闭部分 SSTable 的 Bloom Filter ===
  已关闭 7/8 张表的 Bloom Filter
  [ALERT:critical] 读放大越限：平均每次 Get 读取 4.20 张 SSTable（阈值 2）
  [建议] 建议提高 Compaction 并发/权重（增大 compactionFanIn、降低 compactionTrigger），加速合并 SSTable 以压制读延迟；若大量表关闭了 Bloom Filter，请同时重建 Filter。
  读放大 = 4.48，Bloom 误判率 = 0.00%
  监控告警数 = 1

=== 阶段三：采纳建议 —— 重建 Filter 并提高 Compaction 权重 ===
  Compaction 后 SSTable 数量: 1
  读放大回落至 1.00，告警数 = 0
```

## 测试结果（`npm test`）

```
✔ BloomFilter: 已添加的 key 绝不漏报（无假阴性）
✔ BloomFilter: 误判率维持在目标附近
✔ 引擎: Bloom 全开时读放大 ≈ 1（基线 1.05 以下）
✔ 验收: 关闭部分 Bloom Filter 后读放大跃升至 ~4.2 并触发 Compaction 告警
✔ 监控: Bloom 误判率越限时建议重建 Filter
✔ 修复闭环: 重建 Filter + 提高 Compaction 权重后读放大回落
✔ 引擎: 覆盖写后 compaction 保留最新值
ℹ tests 7  ℹ pass 7  ℹ fail 0
```
